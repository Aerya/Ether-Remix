package debrid

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
)

func TestVerifiedFileNeverUsesMagnets(t *testing.T) {
	for _, gone := range []bool{false, true} {
		t.Run(fmt.Sprint(gone), func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.URL.Path != "/v4/link/unlock" || r.Method != "POST" {
					t.Errorf("unexpected API operation %s %s", r.Method, r.URL.Path)
					http.Error(w, "forbidden", 400)
					return
				}
				r.ParseForm()
				if r.Form.Get("link") != "https://alldebrid.com/f/verified" {
					t.Error("wrong link")
				}
				if gone {
					fmt.Fprint(w, `{"status":"error","error":{"code":"LINK_DOWN","message":"down"}}`)
					return
				}
				fmt.Fprint(w, `{"status":"success","data":{"link":"https://cdn.example/file.mkv","filesize":100000000}}`)
			}))
			defer server.Close()
			svc := NewService(config.Debrid{Providers: []config.DebridProvider{{ID: "ad", Kind: "alldebrid", APIKey: "test", Enabled: true}}}, Options{ProviderOptions: fastOpts(server.URL)})
			r := core.Release{ID: "alko:test", Name: "Film.2024.FRENCH.1080p.mkv", Size: 100000000, Protocol: core.ProtocolHTTP, StreamDebrid: "alldebrid", StreamURL: "https://alldebrid.com/f/verified"}
			idx := NewIndexer(nil, svc)
			if _, ok := idx.playable(r); !ok {
				t.Fatal("AD file filtered")
			}
			b, err := idx.FetchNZB(context.Background(), r)
			if gone {
				if err == nil {
					t.Fatal("dead file accepted")
				}
				if calls != 1 {
					t.Fatalf("calls %d", calls)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			p, err := DecodePayload(b)
			if err != nil {
				t.Fatal(err)
			}
			if !p.VerifiedFile || p.InfoHash != "" || p.Magnet != "" || p.TorrentID != "" {
				t.Fatal("torrent descriptor created")
			}
			if _, err = svc.Refresh(context.Background(), p, true); err != nil {
				t.Fatal(err)
			}
			if _, err = svc.alternateProvider(context.Background(), p); err == nil {
				t.Fatal("unexpected torrent fallback")
			}
			gone = true
			if _, err = svc.Refresh(context.Background(), p, true); err == nil {
				t.Fatal("dead refresh accepted")
			}
			if calls != 3 {
				t.Fatalf("wanted unlock only, calls=%d", calls)
			}
		})
	}
}

func TestVerifiedFileRequiresMatchingProviderAndSafeURL(t *testing.T) {
	svc := NewService(config.Debrid{}, Options{})
	r := core.Release{Protocol: core.ProtocolHTTP, StreamDebrid: "alldebrid", StreamURL: "https://alldebrid.com/f/valid", Size: 1}
	if _, ok := NewIndexer(nil, svc).playable(r); ok {
		t.Fatal("file playable without AD")
	}
	for _, u := range []string{"https://evil.example/f/a", "https://alldebrid.com@evil.example/f/a", "https://alldebrid.com/f/../a", "http://alldebrid.com/f/a", "https://alldebrid.com/f/a?key=x"} {
		r.StreamURL = u
		if _, err := svc.acquireFile(context.Background(), r); err != core.ErrReleaseRejected {
			t.Fatalf("unsafe URL not rejected: %v", err)
		}
	}
}

func TestVerifiedFileEngineReadsAndReusesPayload(t *testing.T) {
	h := newHarness(t, 256<<10, "alldebrid")
	ctx := context.Background()
	r := core.Release{ID: "alko-engine", Name: "Movie.2024.1080p.mkv", Size: int64(len(h.media.data)), Protocol: core.ProtocolHTTP, StreamDebrid: "alldebrid", StreamURL: "https://alldebrid.com/f/verified"}
	data, err := NewIndexer(nil, h.svc).FetchNZB(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	p := h.provs["alldebrid-0"]
	if p.callCount("link") != 1 {
		t.Fatal("missing unlock")
	}
	if _, err = h.svc.acquireFile(ctx, r); err != nil {
		t.Fatal(err)
	}
	if p.callCount("link") != 1 {
		t.Fatal("cached payload unlocked again")
	}
	engine := NewEngine(h.svc, EngineOptions{Cache: newMemCache(), Inner: &fakeInner{}, BlockSize: testBlock, HedgeDelay: -1, Logger: h.svc.log})
	defer engine.Close()
	src, err := engine.Open(ctx, r.ID, data)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	if got := readAll(t, src); !bytes.Equal(got, h.media.data) {
		t.Fatal("wrong streamed bytes")
	}
	for _, op := range []string{"find", "check", "acquire", "remove"} {
		if p.callCount(op) != 0 {
			t.Fatalf("unexpected %s", op)
		}
	}
}
