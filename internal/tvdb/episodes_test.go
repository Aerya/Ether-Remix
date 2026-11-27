package tvdb

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// A durable page set must survive a client restart, then change on use when
// an update invalidates it. The recorded episode rows are trimmed and split
// into pages from tests/fixtures/api/series-121361-episodes-season1.json at
// github.com/predakanga/tvdb-v4-php (68cb27e3ea2c1c0c625792e9eb498ec6b833d0a9).
func TestSeasonPagesSurviveRestartAndRefreshOnUse(t *testing.T) {
	raw, err := os.ReadFile("testdata/episodes.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	settings := func() (string, string) { return "fixture-key", "en-CA" }
	prefix := episodePrefix(121361, "official", 1)
	ctx := t.Context()
	newClient := func(network string) *Client {
		c := NewSource(dir, settings)
		transport := newTransport()
		transport.http.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
			body := ""
			status := http.StatusOK
			if network == "offline" {
				status = http.StatusServiceUnavailable
			} else {
				switch r.URL.Path {
				case "/v4/login":
					body = `{"status":"success","data":{"token":"fixture-token"}}`
				case "/v4" + strings.SplitN(prefix, "?", 2)[0]:
					if r.URL.Query().Get("page") == "0" {
						body = string(fixture["first"])
					} else if network == "updated" {
						body = string(fixture["updated"])
					} else {
						body = string(fixture["second"])
					}
				case "/v4/updates":
					now := time.Now().Add(-time.Hour).Unix()
					b, _ := json.Marshal(response{Status: "success", Data: updateForTest(now)})
					body = string(b)
				default:
					status = http.StatusNotFound
				}
			}
			return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
		})
		c.transport = transport
		return c
	}
	show := reference(121361, core.TypeShow)
	order := core.EpisodeOrder{Source: core.MetadataTVDB, Kind: core.OrderAired}
	assertEpisodes := func(ctx context.Context, c *Client, want ...string) {
		t.Helper()
		episodes, err := c.Episodes(ctx, show, order, 1)
		if err != nil {
			t.Fatal(err)
		}
		if len(episodes) != len(want) {
			t.Fatalf("got %d episodes, want %d", len(episodes), len(want))
		}
		for i, episode := range episodes {
			if episode.Title != want[i] || episode.Episode != i+1 || episode.IDs.TVDB <= 0 {
				t.Errorf("episode %d = %+v, want %s", i, episode, want[i])
			}
		}
	}
	original := newClient("original")
	assertEpisodes(ctx, original, "Winter Is Coming", "The Kingsroad")
	assertEpisodes(ctx, newClient("offline"), "Winter Is Coming", "The Kingsroad")
	for _, page := range []string{"0", "1"} {
		record, ok := original.readCache(sha256.Sum256([]byte(prefix + "&page=" + page)))
		if !ok {
			t.Fatal("missing durable page")
		}
		record.Fetched = time.Now().Add(-3 * time.Hour)
		record.Checked = record.Fetched
		original.writeCache(record)
	}
	assertEpisodes(ctx, newClient("updated"), "Winter Is Coming", "The Kingsroad", "Lord Snow")
}

func updateForTest(stamp int64) json.RawMessage {
	raw, _ := json.Marshal([]entityUpdate{{RecordID: 3254641, SeriesID: 121361, Stamp: stamp}})
	return raw
}
