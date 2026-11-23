package livetv

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestProviderRefusalEpisodeHasHardCeiling(t *testing.T) {
	for _, status := range []int{403, 509} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			t.Parallel()
			var count atomic.Int32
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { count.Add(1); w.WriteHeader(status) }))
			defer up.Close()
			s := testService(t, Options{HTTPClient: up.Client()})
			g := &inputGrant{origin: up.URL, headers: http.Header{}}
			for range 40 {
				ctx, cancel := context.WithCancel(context.Background())
				res, _ := s.relayFetch(ctx, cancel, g, up.URL, &http.Request{Header: http.Header{}})
				if res != nil {
					res.Body.Close()
				}
				cancel()
			}
			if count.Load() != 28 {
				t.Fatalf("provider saw %d requests", count.Load())
			}
		})
	}
}
