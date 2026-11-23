package livetv

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestM3UPlaylistHeaderGuideDiscoveryAndOwnerOverride(t *testing.T) {
	ctx := context.Background()

	var leakedAuth atomic.Bool
	epgOne := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			leakedAuth.Store(true)
		}
		_, _ = w.Write([]byte(xmltvFixture(4, "news.us=News Network")))
	}))
	defer epgOne.Close()

	epgTwo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			leakedAuth.Store(true)
		}
		_, _ = w.Write([]byte(xmltvFixture(4, "sport.uk=Sport One")))
	}))
	defer epgTwo.Close()

	ownerEPG := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(xmltvFixture(6, "news.us=News Network")))
	}))
	defer ownerEPG.Close()

	playlistBody := `#EXTM3U x-tvg-url="` + epgOne.URL + `/epg1.xml, ` + epgTwo.URL + `/epg2.xml" url-tvg="` + epgOne.URL + `/epg1.xml"` + "\n" +
		`#EXTINF:-1 tvg-id="news.us",News Network` + "\nhttps://streams.invalid/news.ts\n" +
		`#EXTINF:-1 tvg-id="sport.uk",Sport One` + "\nhttps://streams.invalid/sport.ts\n"

	playlistSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, p, ok := r.BasicAuth()
		if !ok || u != "owner" || p != "secret" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(playlistBody))
	}))
	defer playlistSrv.Close()

	s := testService(t, Options{
		HTTPClient: playlistSrv.Client(),
		Now:        func() time.Time { return guideTestNow },
	})

	got, err := s.ImportSource(ctx, Source{
		ID:          "m3u-epg",
		Name:        "Playlist with guide",
		PlaylistURL: playlistSrv.URL + "/list.m3u",
		Auth:        &SourceAuth{Type: "basic", Username: "owner", Password: "secret"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if leakedAuth.Load() {
		t.Fatal("playlist basic-auth credentials leaked to cross-origin x-tvg-url host")
	}
	if !got.PlaylistGuide || !got.HasGuide || got.GuideURL != "" || got.GuideChannelCount != 2 || got.ProgrammeCount != 8 {
		t.Fatalf("expected discovered playlist guide (2 guide channels, 8 programmes), got %+v", got)
	}
	listed := sourceByID(t, s, "m3u-epg")
	if !listed.PlaylistGuide || !listed.HasGuide || listed.GuideURL != "" {
		t.Fatalf("listed source missing playlistGuide flag: %+v", listed)
	}

	// Owner-set GuideURL wins and clears PlaylistGuide.
	overridden, err := s.ImportSource(ctx, Source{
		ID:          "m3u-epg",
		Name:        "Playlist with guide",
		PlaylistURL: playlistSrv.URL + "/list.m3u",
		GuideURL:    ownerEPG.URL + "/owner.xml",
		Auth:        &SourceAuth{Type: "basic", Username: "owner", Password: "secret"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if overridden.PlaylistGuide || overridden.GuideURL != ownerEPG.URL+"/owner.xml" || overridden.GuideChannelCount != 1 || overridden.ProgrammeCount != 6 {
		t.Fatalf("owner GuideURL was overridden by playlist header: %+v", overridden)
	}

	// Owner-set inline Guide also wins over the playlist's #EXTM3U header.
	inline, err := s.ImportSource(ctx, Source{
		ID:       "m3u-inline",
		Name:     "Inline guide override",
		Playlist: playlistBody,
		Guide:    xmltvFixture(2, "sport.uk=Sport One"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if inline.PlaylistGuide || !inline.HasGuide || inline.GuideChannelCount != 1 || inline.ProgrammeCount != 2 {
		t.Fatalf("owner inline Guide was overridden by playlist header: %+v", inline)
	}
}

func TestXtreamOnePasteURLShapesAndNeverLogsPassword(t *testing.T) {
	ctx := context.Background()
	const (
		user = "xt-owner"
		pass = "Sup3r$ecret!Pass/99"
	)

	var paths []string
	streams := `[{"num":1,"name":"News Network","stream_id":101,"epg_channel_id":"news.us","category_id":"3"}]`
	up := xtreamPanel(t, user, pass, &paths, &streams)

	var logs bytes.Buffer
	s := testService(t, Options{
		HTTPClient: up.Client(),
		Sealer:     upgradedSealer(t),
		Logger:     slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
		Now:        func() time.Time { return guideTestNow },
	})

	shapes := []struct {
		id         string
		pasted     string
		wantOutput string
	}{
		{
			id:         "xt-get",
			pasted:     up.URL + "/get.php?username=" + url.QueryEscape(user) + "&password=" + url.QueryEscape(pass) + "&type=m3u_plus&output=m3u8",
			wantOutput: "m3u8",
		},
		{
			id:     "xt-api",
			pasted: up.URL + "/player_api.php?username=" + url.QueryEscape(user) + "&password=" + url.QueryEscape(pass),
		},
		{
			id:         "xt-short",
			pasted:     up.URL + "/" + url.PathEscape(user) + "/" + url.PathEscape(pass) + "/101.m3u8",
			wantOutput: "m3u8",
		},
		{
			id:         "xt-live",
			pasted:     up.URL + "/live/" + url.PathEscape(user) + "/" + url.PathEscape(pass) + "/101.ts",
			wantOutput: "ts",
		},
	}

	for _, tc := range shapes {
		got, err := s.ImportSource(ctx, Source{
			ID:   tc.id,
			Name: tc.id,
			Auth: &SourceAuth{Type: "xtream", Server: tc.pasted},
		})
		if err != nil {
			t.Fatalf("%s: %v", tc.id, err)
		}
		if got.Auth == nil || got.Auth.Server != up.URL || got.Auth.Username != user || got.Auth.Password != redactedSecret {
			t.Fatalf("%s: unexpected auth after import: %+v", tc.id, got.Auth)
		}
		if tc.wantOutput != "" && got.Auth.Output != tc.wantOutput {
			t.Fatalf("%s: output=%q, want %q", tc.id, got.Auth.Output, tc.wantOutput)
		}
		raw := rawSource(t, s.o.DB, tc.id)
		for _, secret := range []string{pass, url.QueryEscape(pass), url.PathEscape(pass), user, "get.php", "player_api.php"} {
			if strings.Contains(raw, secret) {
				t.Fatalf("%s: leaked %q in stored source row: %s", tc.id, secret, raw)
			}
		}
	}

	// Failed login and unreachable host must never echo or log the password either.
	_, badErr := s.ImportSource(ctx, Source{
		ID:   "xt-bad",
		Name: "Bad creds",
		Auth: &SourceAuth{
			Type:   "xtream",
			Server: up.URL + "/get.php?username=" + url.QueryEscape(user) + "&password=" + url.QueryEscape(pass+"-wrong"),
		},
	})
	if badErr == nil || strings.Contains(badErr.Error(), "Sup3r") {
		t.Fatalf("expected clean error without password, got %v", badErr)
	}

	// Redacted placeholder inside a moved server URL must still be rejected.
	_, moveErr := s.ImportSource(ctx, Source{
		ID:   "xt-get",
		Name: "Moved",
		Auth: &SourceAuth{
			Type:     "xtream",
			Server:   "https://evil.invalid/get.php?username=" + url.QueryEscape(user) + "&password=" + url.QueryEscape(redactedSecret),
			Username: user,
			Password: redactedSecret,
		},
	})
	if !errors.Is(moveErr, errSourceSecretMoved) {
		t.Fatalf("expected errSourceSecretMoved, got %v", moveErr)
	}

	for _, secret := range []string{"Sup3r", pass, url.QueryEscape(pass), url.PathEscape(pass)} {
		if strings.Contains(logs.String(), secret) {
			t.Fatalf("password leaked into logs: %s", logs.String())
		}
	}
}
