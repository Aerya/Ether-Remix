package app

import (
	"context"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/identity"
	"github.com/braedonsaunders/ether/internal/livetv"
)

func TestSurfaceLiveGuideWindowBatchPolicyAndRevision(t *testing.T) {
	ctx := context.Background()
	a := setupTestAppConfigured(t, true, Options{}, nil)
	local, _ := identity.NewLocal(a.db)
	viewerID, err := local.CreateViewer(ctx, "window-viewer", "password-1234")
	if err != nil {
		t.Fatal(err)
	}
	if err = a.users.RefreshViewers(ctx); err != nil {
		t.Fatal(err)
	}
	viewer, err := a.users.ActiveUser(ctx, viewerID)
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC().Truncate(time.Hour)
	fmtXML := func(tm time.Time) string { return tm.UTC().Format("20060102150405 -0700") }
	xml := `<tv>` +
		`<channel id="news"><display-name>News</display-name></channel>` +
		`<channel id="quiet"><display-name>Quiet</display-name></channel>` +
		`<channel id="restricted"><display-name>Restricted</display-name></channel>` +
		`<programme channel="news" start="` + fmtXML(now.Add(-15*time.Minute)) + `" stop="` + fmtXML(now.Add(45*time.Minute)) + `"><title>Headline Hour</title><icon src="https://example.invalid/card.svg?token=secret"/></programme>` +
		`<programme channel="restricted" start="` + fmtXML(now) + `" stop="` + fmtXML(now.Add(time.Hour)) + `"><title>Hidden Show</title></programme>` +
		`</tv>`
	playlist := "#EXTM3U\n" +
		"#EXTINF:-1 tvg-id=\"news\",News\nhttps://example.invalid/news\n" +
		"#EXTINF:-1 tvg-id=\"quiet\",Quiet\nhttps://example.invalid/quiet\n" +
		"#EXTINF:-1 tvg-id=\"restricted\",Restricted\nhttps://example.invalid/restricted\n"
	if _, err := a.livetv.ImportSource(ctx, livetv.Source{ID: "win-src", Name: "Window", Playlist: playlist, Guide: xml}); err != nil {
		t.Fatal(err)
	}

	channels, err := a.livetv.Channels(ctx)
	if err != nil {
		t.Fatal(err)
	}
	idsByName := map[string]string{}
	byName := map[string]livetv.Channel{}
	for _, c := range channels {
		idsByName[c.Name] = c.ID
		byName[c.Name] = c
	}

	// Hide "Restricted" from the vela app surface; viewer must see state=unavailable and zero programmes.
	restricted := byName["Restricted"]
	restricted.HiddenApps = []string{"vela"}
	if _, err := a.livetv.SaveChannel(ctx, restricted); err != nil {
		t.Fatal(err)
	}

	source := *a.artwork
	source.URLImages = testRemoteLogo{}
	facade := surfaceLiveTV{
		app:              a,
		surface:          "vela",
		channelArtwork:   source.ChannelImage,
		programmeArtwork: source.ProgrammeImages,
		artwork:          source,
	}

	got, err := facade.GuideWindowBatch(ctx, viewer, frontend.LiveGuideWindowQuery{
		Channels: []string{idsByName["Quiet"], idsByName["News"], idsByName["Restricted"]},
		From:     now,
		Until:    now.Add(2 * time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Channels) != 3 || got.Revision == "" || got.ValidUntil.IsZero() {
		t.Fatalf("unexpected batch response: %+v", got)
	}
	if got.Channels[0].State != frontend.LiveGuideWindowReady || len(got.Channels[0].Programmes) != 0 {
		t.Fatalf("Quiet channel = %+v, want ready []", got.Channels[0])
	}
	if got.Channels[1].State != frontend.LiveGuideWindowReady || len(got.Channels[1].Programmes) != 1 || len(got.Channels[1].Programmes[0].Images) != 1 {
		t.Fatalf("News channel = %+v, want ready with 1 artwork-enriched programme", got.Channels[1])
	}
	if got.Channels[2].State != frontend.LiveGuideWindowUnavailable || len(got.Channels[2].Programmes) != 0 {
		t.Fatalf("Restricted channel leaked listings: %+v", got.Channels[2])
	}

	// Updating guide data for requested channels must advance Revision.
	xml2 := `<tv>` +
		`<channel id="news"><display-name>News</display-name></channel>` +
		`<channel id="quiet"><display-name>Quiet</display-name></channel>` +
		`<channel id="restricted"><display-name>Restricted</display-name></channel>` +
		`<programme channel="news" start="` + fmtXML(now.Add(-15*time.Minute)) + `" stop="` + fmtXML(now.Add(45*time.Minute)) + `"><title>Updated Headline Hour</title></programme>` +
		`</tv>`
	if _, err := a.livetv.ImportSource(ctx, livetv.Source{ID: "win-src", Name: "Window", Playlist: playlist, Guide: xml2}); err != nil {
		t.Fatal(err)
	}
	after, err := facade.GuideWindowBatch(ctx, viewer, frontend.LiveGuideWindowQuery{
		Channels: []string{idsByName["Quiet"], idsByName["News"], idsByName["Restricted"]},
		From:     now,
		Until:    now.Add(2 * time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	if after.Revision == got.Revision {
		t.Fatalf("expected revision to change when guide listings changed (remained %q)", after.Revision)
	}
}
