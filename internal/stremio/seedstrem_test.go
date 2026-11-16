package stremio

import (
	"context"
	"errors"
	"github.com/braedonsaunders/ether/internal/debrid"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
)

func TestSeedstremUsesRemoteHTTPDownloader(t *testing.T) {
	hash := "0123456789abcdef0123456789abcdef01234567"
	magnet := "magnet:?xt=urn:btih:" + hash
	for _, prefix := range []string{"", "/private-access"} {
		link := "https://seedstrem.example" + prefix + "/stremio/play/" + hash + "?magnet=" + url.QueryEscape(magnet)
		got := Convert([]Stream{{Name: "seedstrem", Title: "Example.Movie.2025.1080p.MULTi.WEB-DL", URL: link}}, core.SearchRequest{Type: core.TypeMovie}, Lookup{}, ConvertOptions{Origin: "https://seedstrem.example", Instance: "seedstrem"})
		if len(got.Releases) != 1 {
			t.Fatalf("releases: %+v", got)
		}
		r := got.Releases[0]
		if r.Protocol != core.ProtocolHTTP || r.StreamURL != link || r.InfoHash != "" || r.MagnetURL != "" || !r.OpensOnDemand() {
			t.Fatalf("must preserve remote on-demand playback, not use a debrid account: %+v", r)
		}
	}
}

func TestSeedstremRouteDoesNotCaptureOtherAddonMagnets(t *testing.T) {
	hash := "0123456789abcdef0123456789abcdef01234567"
	magnet := url.QueryEscape("magnet:?xt=urn:btih:" + hash)
	for _, link := range []string{
		"https://addon.example/play/" + hash + "?magnet=" + magnet,
		"https://addon.example/stremio/play/ffffffffffffffffffffffffffffffffffffffff?magnet=" + magnet,
		"https://addon.example/stremio/play/" + hash,
	} {
		if seedstremPlay(link, hash) {
			t.Fatalf("unexpected Seedstrem match: %s", link)
		}
	}
}

func TestSeedstremBackgroundChecksNeverOpenDownloadURL(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); w.WriteHeader(http.StatusBadGateway) }))
	defer server.Close()
	hash := "0123456789abcdef0123456789abcdef01234567"
	link := server.URL + "/stremio/play/" + hash + "?magnet=" + url.QueryEscape("magnet:?xt=urn:btih:"+hash)
	got := Convert([]Stream{{Name: "seedstrem", Title: "Example.Movie.2025.1080p.MULTi.WEB-DL", URL: link}}, core.SearchRequest{Type: core.TypeMovie}, Lookup{}, ConvertOptions{Origin: server.URL, Instance: "seedstrem"})
	if len(got.Releases) != 1 {
		t.Fatal(got)
	}
	speculative, _ := core.WithSpeculative(context.Background())
	background := core.WithBackgroundBudget(context.Background(), func(context.Context, string, string, int64) error { return nil })
	for _, ctx := range []context.Context{speculative, background, core.WithoutOnDemandStreams(context.Background()), core.WithOnDemandStreamsForAvailability(context.Background()), core.WithIndexerPurpose(context.Background(), core.IndexerPurposeConsole)} {
		_, err := new(debrid.Service).DirectPayload(ctx, got.Releases[0])
		if !errors.Is(err, core.ErrForegroundOnly) {
			t.Fatalf("expected foreground-only, got %v", err)
		}
	}
	if requests.Load() != 0 {
		t.Fatalf("background checks opened Seedstrem %d times", requests.Load())
	}
}
