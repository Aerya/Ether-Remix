package music

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
)

type dmmTransport func(*http.Request) (*http.Response, error)

func (f dmmTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func dmmFixture(t *testing.T, status int, body string) *DMMFinder {
	t.Helper()
	return &DMMFinder{HTTP: &http.Client{Transport: dmmTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "debridmediamanager.com" || r.URL.Path != "/api/music/search" {
			t.Errorf("unexpected endpoint: %s", r.URL)
		}
		if r.URL.Query().Get("q") != "Artist Album & More" || r.URL.Query().Get("limit") != "50" {
			t.Error("query encoding lost")
		}
		if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
			t.Error("credential sent to DMM")
		}
		return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}}
}

func TestDMMFinderValidatesDeduplicatesAndBuildsMagnets(t *testing.T) {
	hash := strings.Repeat("a", 40)
	body := fmt.Sprintf(`{"albums":[{"hash":%q,"artist":"Artist","album":"Album & More","totalBytes":123},
 {"hash":%q,"artist":"Artist","album":"Duplicate"},
 {"hash":"not-a-hash","artist":"Artist","album":"Album"},
 {"hash":%q,"artist":"","album":"Album"}]}`, strings.ToUpper(hash), hash, strings.Repeat("b", 40))
	f := dmmFixture(t, 200, body)
	hits, err := f.FindAlbum(t.Context(), "Artist Album & More")
	if err != nil || len(hits) != 1 {
		t.Fatalf("%v %v", hits, err)
	}
	h := hits[0]
	if h.InfoHash != hash || h.Magnet != "magnet:?xt=urn:btih:"+hash || h.Size != 123 || h.Title != "Artist - Album & More" {
		t.Fatalf("%+v", h)
	}
	got, err := f.Magnet(t.Context(), h)
	if err != nil || got != h {
		t.Fatal("magnet changed")
	}
}

func TestDMMFinderDistinguishesEmptyCatalogueAndFailures(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
		empty  bool
	}{
		{"empty", 200, `{"albums":[]}`, true},
		{"missing", 200, `{"error":"private upstream message"}`, false},
		{"invalid", 200, `not json`, false},
		{"rate limit", 429, `private upstream message`, false},
		{"outage", 503, `private upstream message`, false},
		{"oversize", 200, strings.Repeat(" ", (2<<20)+1), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hits, err := dmmFixture(t, tc.status, tc.body).FindAlbum(t.Context(), "Artist Album & More")
			if tc.empty {
				if err != nil || len(hits) != 0 {
					t.Fatal(hits, err)
				}
				return
			}
			if err == nil || strings.Contains(err.Error(), "private upstream") {
				t.Fatalf("failure not sanitized: %v", err)
			}
		})
	}
}

func TestDMMSettingsActivationPlanAndFallback(t *testing.T) {
	t.Setenv("PSE_MUSIC_DMM", "on")
	if !LoadSettings().DMM {
		t.Fatal("setting ignored")
	}
	sources := DebridSources{
		{Kind: DMMSource, Name: "DMM · Real-Debrid", Resolver: Resolvers{}},
		{Kind: "realdebrid", Name: "Real-Debrid", Resolver: staticResolver{s: Stream{URL: "https://rd.example/fallback"}}},
	}
	cfg := Settings{DMM: true, RealDebrid: true}
	if !cfg.Enabled() || cfg.Mode() != "debrid" {
		t.Fatal("DMM not enabled")
	}
	_, plan := chain(cfg, sources, nil)
	if len(plan) == 0 {
		t.Fatal("missing plan")
	}
	found := false
	for _, p := range plan {
		if p.Kind == DMMSource && p.Active {
			found = true
		}
	}
	if !found {
		t.Fatal("DMM absent from diagnostic")
	}
	onlyDMM := Settings{DMM: true}.NewSource(sources).(*DeezerSource)
	if _, err := onlyDMM.Streams.Resolve(context.Background(), Track{}); err == nil {
		t.Fatal("disabled indexer source used")
	}
	source := cfg.NewSource(sources).(*DeezerSource)

	// La même chaîne utilisée en lecture continue après une absence DMM.
	st, err := source.Streams.Resolve(context.Background(), Track{})
	if err != nil || st.URL != "https://rd.example/fallback" {
		t.Fatal(st, err)
	}
	sources[0].NoAccount = true
	_, plan = chain(Settings{DMM: true}, sources, nil)
	for _, p := range plan {
		if p.Kind == DMMSource && (p.Active || p.Reason == "") {
			t.Fatal("missing account not explained")
		}
	}
}

func TestDMMSampleAlbumsUsesDMMCatalogueAndLogsNoSecrets(t *testing.T) {
	var logs bytes.Buffer
	log := slog.New(slog.NewTextHandler(&logs, nil))
	f := DMMFinder{Log: log, HTTP: &http.Client{Transport: dmmTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/api/music/library" || r.URL.Query().Get("summary") != "1" || r.URL.Query().Get("limit") != "5" {
			t.Fatal(r.URL)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(fmt.Sprintf(`{"albums":[{"hash":%q,"artist":"Artist","album":"Album"}]}`, strings.Repeat("a", 40))))}, nil
	})}}
	hits, err := f.SampleAlbums(t.Context())
	if err != nil || len(hits) != 1 {
		t.Fatal(hits, err)
	}
	if !strings.Contains(logs.String(), "stage=library") || !strings.Contains(logs.String(), "albums=1") || strings.Contains(logs.String(), "magnet:") {
		t.Fatal(logs.String())
	}
}
