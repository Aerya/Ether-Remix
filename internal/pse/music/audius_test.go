package music

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAudiusPublicMatchingAndFailover(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
			t.Error("unexpected credentials")
		}
		if r.URL.Query().Get("query") != "Artist Song" {
			t.Error("missing identity query")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[
   {"id":"gated","title":"Song","duration":200,"user":{"name":"Artist"},"is_available":true,"is_streamable":true,"is_stream_gated":true},
   {"id":"wrong","title":"Song (Live)","duration":200,"user":{"name":"Artist"},"is_available":true,"is_streamable":true,"is_stream_gated":false},
   {"id":"noGateFlag","title":"Song","duration":200,"user":{"name":"Artist"},"is_available":true,"is_streamable":true},
   {"id":"absent","title":"Song","duration":200,"user":{"name":"Artist"},"is_streamable":true,"is_stream_gated":false},
   {"id":"long","title":"Song","duration":215,"user":{"name":"Artist"},"is_available":true,"is_streamable":true,"is_stream_gated":false},
   {"id":"other","title":"Song","duration":200,"user":{"name":"Other Artist"},"is_available":true,"is_streamable":true,"is_stream_gated":false},
   {"id":"good","title":"Song","duration":200,"user":{"name":"Artist"},"is_available":true,"is_streamable":true,"is_stream_gated":false}
  ]}`))
	}))
	defer srv.Close()
	a := NewAudius(srv.Client())
	a.base = srv.URL
	track := Track{Title: "Song", Duration: 200, Artist: Artist{Name: "Artist"}}
	st, err := a.Resolve(context.Background(), track)
	if err != nil || st.URL != srv.URL+"/tracks/good/stream?app_name=Accent" || st.Mime != "audio/mpeg" {
		t.Fatalf("%+v %v", st, err)
	}
	if _, err = a.Resolve(WithSkippedStreams(context.Background(), st.URL), track); !errors.Is(err, ErrNotFound) {
		t.Fatalf("skip: %v", err)
	}
}

func TestAudiusRateLimitAndInvalidResponse(t *testing.T) {
	for _, status := range []int{429, 401, 403, 500, 200} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			calls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.WriteHeader(status)
				_, _ = w.Write([]byte("invalid"))
			}))
			defer srv.Close()
			a := NewAudius(srv.Client())
			a.base = srv.URL
			if err := a.Check(context.Background()); err == nil {
				t.Fatal("accepted error response")
			}
			if status == 429 {
				_ = a.Check(context.Background())
				if calls != 1 {
					t.Fatal("ignored cooldown")
				}
			}
		})
	}
}

func TestAudiusSettingsOrder(t *testing.T) {
	for _, position := range []string{"first", "last"} {
		cfg := Settings{Audius: position, EclipseURLs: "https://example.com/manifest.json"}
		if !cfg.Enabled() {
			t.Fatal("disabled")
		}
		src := cfg.NewSource(nil).(*DeezerSource)
		rs := src.Streams.(Resolvers)
		i := 0
		if position == "last" {
			i = 1
		}
		if len(rs) != 2 {
			t.Fatalf("%d resolvers", len(rs))
		}
		if _, ok := rs[i].(*Audius); !ok {
			t.Fatal("wrong priority")
		}
	}
	if (Settings{Audius: "invalid"}).Enabled() {
		t.Fatal("invalid value enables source")
	}
}

// A real HTTP refusal after resolution must let the next resolver play.
func TestAudiusRelayFailure(t *testing.T) {
	hits := 0
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/tracks/search" {
			_, _ = w.Write([]byte(`{"data":[{"id":"public","title":"Song","duration":200,"user":{"name":"Artist"},"is_available":true,"is_streamable":true,"is_stream_gated":false}]}`))
			return
		}
		hits++
		w.WriteHeader(http.StatusForbidden)
	}))
	defer api.Close()
	a := NewAudius(api.Client())
	a.base = api.URL
	fallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "audio/mpeg")
		_, _ = w.Write([]byte{255, 251, 144, 0})
	}))
	defer fallback.Close()
	source := audiusTestSource{rs: Resolvers{a, fixedResolver{fallback.URL}}}
	server := NewServer(Options{Source: source})
	rec := httptest.NewRecorder()
	if err := server.relayAudio(rec, httptest.NewRequest("GET", "/rest/stream", nil), 1); err != nil {
		t.Fatal(err)
	}
	if hits != 1 || rec.Body.Len() != 4 {
		t.Fatalf("hits=%d bytes=%d", hits, rec.Body.Len())
	}
}

type audiusTestSource struct {
	Source
	rs Resolvers
}

func (s audiusTestSource) Stream(ctx context.Context, _ int64, _ string) (Stream, error) {
	return s.rs.Resolve(ctx, Track{Title: "Song", Duration: 200, Artist: Artist{Name: "Artist"}})
}
