package resolver

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

func TestReadySourceWinsWithinBudget(t *testing.T) {
	for _, tc := range []struct {
		name  string
		score float64
		want  time.Duration
	}{
		{"equal rank", 100, 200 * time.Millisecond},
		{"better preferred score", 90, readySourceBudget},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				fallbackFinished := make(chan struct{})
				h := windowHealth(func(ctx context.Context, id string) (bool, error) {
					if id == "unneeded" {
						t.Error("unneeded candidate was checked")
					}
					delay := 200 * time.Millisecond
					if id == "usenet" {
						delay = 7600 * time.Millisecond
					}
					select {
					case <-time.After(delay):
					case <-ctx.Done():
						return false, ctx.Err()
					}
					if id == "usenet" {
						close(fallbackFinished)
						return false, core.ErrArticleMissing
					}
					return true, nil
				})
				s := New(&fakeIndexer{}, h, nil, testCfg(), Options{})
				defer s.Close()
				s.SetSourceSignals(cachedSet{"cached": true})
				ids := []string{"usenet"}
				for range maxHealthAttempts {
					ids = append(ids, "unneeded")
				}
				ids = append(ids, "debrid")
				cs, tr := windowCandidates(ids...)
				peer := len(cs) - 1
				cs[0].rel.Protocol = core.ProtocolUsenet
				cs[peer].rel.Protocol, cs[peer].rel.InfoHash, cs[peer].rel.Score = core.ProtocolTorrent, "cached", tc.score
				start := time.Now()
				winner, _, err := s.validateCandidates(context.Background(), cs, tr, 1)
				if err != nil || winner != peer {
					t.Fatalf("winner=%d err=%v", winner, err)
				}
				if elapsed := time.Since(start); elapsed != tc.want {
					t.Fatalf("decision took %s, want %s", elapsed, tc.want)
				}
				select {
				case <-fallbackFinished:
					t.Fatal("Play waited for fallback sampling")
				default:
				}
				time.Sleep(8 * time.Second)
				synctest.Wait()
				if !s.recentlyFailedHealth("usenet") {
					t.Fatal("background missing articles were not remembered")
				}
				if !tr.Candidates[peer].Healthy {
					t.Fatal("winner was not health verified")
				}
			})
		})
	}
}
