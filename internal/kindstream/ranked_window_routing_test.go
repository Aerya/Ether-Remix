package kindstream

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/resolver"
)

type trackedMediaSource struct {
	*bytes.Reader
	id     string
	name   string
	closed atomic.Int32
}

func (s *trackedMediaSource) Name() string      { return s.name }
func (s *trackedMediaSource) ReleaseID() string { return s.id }
func (s *trackedMediaSource) Close() error {
	s.closed.Add(1)
	return nil
}

func testMKVPayload() []byte {
	return append([]byte{0x1a, 0x45, 0xdf, 0xa3}, bytes.Repeat([]byte{0x22}, 512)...)
}

func testFLACPayload() []byte {
	return append([]byte("fLaC"), bytes.Repeat([]byte{0x42}, 512)...)
}

func tierPtr(v int) *int { return &v }

func hashSource(id, hash string, tier *int, cached bool) core.PlayableSource {
	s := core.PlayableSource{
		ID:      id,
		Pattern: PatternRelease,
		MIME:    "video/x-matroska",
		Tier:    tier,
		Release: &core.ReleaseHint{InfoHash: hash},
	}
	if cached {
		s.Release.Candidate = &core.SourceCandidate{
			InfoHash: hash,
			Cached:   true,
		}
	}
	return s
}

func nzbSource(id string, tier *int) core.PlayableSource {
	return core.PlayableSource{
		ID:      id,
		Pattern: PatternRelease,
		MIME:    "video/x-matroska",
		Tier:    tier,
		Release: &core.ReleaseHint{NZBURL: "https://idx.test/" + id + ".nzb"},
	}
}

func torrentURLSource(id string, tier *int) core.PlayableSource {
	return core.PlayableSource{
		ID:      id,
		Pattern: PatternRelease,
		MIME:    "video/x-matroska",
		Tier:    tier,
		Release: &core.ReleaseHint{TorrentURL: "https://idx.test/" + id + ".torrent"},
	}
}

func newTestResolver(pref string, alwaysFirst bool) *resolver.Service {
	cfg := config.Defaults("")
	cfg.Resolver.SourcePreference = pref
	cfg.Resolver.SourceAlwaysFirst = alwaysFirst
	return resolver.New(nil, nil, nil, cfg, resolver.Options{})
}

func TestOpenSourcesRankedWindowOrderAndGrace(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		payload := testMKVPayload()
		started := map[string]chan struct{}{
			"a": make(chan struct{}),
			"b": make(chan struct{}),
			"c": make(chan struct{}),
		}
		releaseA := make(chan struct{})
		releaseB := make(chan struct{})
		srcB := &trackedMediaSource{Reader: bytes.NewReader(payload), id: "b", name: "b.flac"}

		sources := []core.PlayableSource{
			hashSource("a", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", nil, true),
			hashSource("b", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", nil, true),
			hashSource("c", "cccccccccccccccccccccccccccccccccccccccc", nil, true),
		}

		type result struct {
			bs  ByteSource
			win core.PlayableSource
			err error
		}
		done := make(chan result, 1)
		go func() {
			bs, win, err := OpenSources(context.Background(), sources, Deps{
				OpenReleaseSource: func(ctx context.Context, src core.PlayableSource) (core.MediaSource, error) {
					close(started[src.ID])
					switch src.ID {
					case "a":
						select {
						case <-releaseA:
							return &trackedMediaSource{Reader: bytes.NewReader(payload), id: "a", name: "a.flac"}, nil
						case <-ctx.Done():
							return nil, ctx.Err()
						}
					case "b":
						<-releaseB
						return srcB, nil
					default:
						return nil, errors.New("c should not start")
					}
				},
			})
			done <- result{bs: bs, win: win, err: err}
		}()

		// Window starts the first two candidates in order, not the third.
		<-started["a"]
		<-started["b"]
		synctest.Wait()
		select {
		case <-started["c"]:
			t.Fatal("third candidate started while window was full")
		default:
		}

		// Second candidate succeeds first; lead still owns the grace period.
		close(releaseB)
		synctest.Wait()
		time.Sleep(resolver.RankedSourceGrace / 2)
		synctest.Wait()
		select {
		case r := <-done:
			if r.bs != nil {
				_ = r.bs.Close()
			}
			t.Fatalf("second candidate overtook lead before grace elapsed: win=%q err=%v", r.win.ID, r.err)
		default:
		}

		// Lead succeeds within grace and wins; losing second reader is closed.
		close(releaseA)
		r := <-done
		if r.err != nil {
			t.Fatalf("OpenSources failed: %v", r.err)
		}
		defer r.bs.Close()
		if r.win.ID != "a" {
			t.Fatalf("winner = %q, want a", r.win.ID)
		}
		if srcB.closed.Load() != 1 {
			t.Fatalf("losing ByteSource Close count = %d, want 1", srcB.closed.Load())
		}
		select {
		case <-started["c"]:
			t.Fatal("third candidate should never have started")
		default:
		}
	})
}

func TestOpenSourcesRankedWindowFailFastAdvance(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		payload := testMKVPayload()
		started := map[string]chan struct{}{
			"a": make(chan struct{}),
			"b": make(chan struct{}),
			"c": make(chan struct{}),
			"d": make(chan struct{}),
		}
		bCancelled := make(chan struct{})
		failA := make(chan struct{})

		sources := []core.PlayableSource{
			hashSource("a", "1111111111111111111111111111111111111111", nil, true),
			hashSource("b", "2222222222222222222222222222222222222222", nil, true),
			hashSource("c", "3333333333333333333333333333333333333333", nil, true),
			hashSource("d", "4444444444444444444444444444444444444444", nil, true),
		}

		var failedIDs []string
		type result struct {
			bs  ByteSource
			win core.PlayableSource
			err error
		}
		done := make(chan result, 1)
		start := time.Now()
		go func() {
			bs, win, err := OpenSources(context.Background(), sources, Deps{
				OpenReleaseSource: func(ctx context.Context, src core.PlayableSource) (core.MediaSource, error) {
					close(started[src.ID])
					switch src.ID {
					case "a":
						<-failA
						return nil, core.ErrArticleMissing
					case "b":
						<-ctx.Done()
						close(bCancelled)
						return nil, ctx.Err()
					case "c":
						return &trackedMediaSource{Reader: bytes.NewReader(payload), id: "c", name: "c.flac"}, nil
					default:
						t.Errorf("unexpected speculative open: %s", src.ID)
						return nil, errors.New("unexpected")
					}
				},
				OnSourceFailed: func(_, _ int, src core.PlayableSource, _ time.Duration, _ error) {
					failedIDs = append(failedIDs, src.ID)
				},
			})
			done <- result{bs: bs, win: win, err: err}
		}()

		<-started["a"]
		<-started["b"]
		synctest.Wait()

		// Failing a immediately starts c while b is still in flight.
		close(failA)
		<-started["c"]

		r := <-done
		if r.err != nil {
			t.Fatalf("OpenSources failed: %v", r.err)
		}
		defer r.bs.Close()
		if r.win.ID != "c" {
			t.Fatalf("winner = %q, want c", r.win.ID)
		}
		if elapsed := time.Since(start); elapsed != resolver.RankedSourceGrace {
			t.Fatalf("grace overtake elapsed = %v, want %v", elapsed, resolver.RankedSourceGrace)
		}
		select {
		case <-bCancelled:
		default:
			t.Fatal("stalled lead b was not cancelled and joined before return")
		}
		select {
		case <-started["d"]:
			t.Fatal("fourth candidate should not start once c succeeded")
		default:
		}
		if len(failedIDs) != 1 || failedIDs[0] != "a" {
			t.Fatalf("OnSourceFailed ids = %v, want [a]", failedIDs)
		}
	})
}

func TestOpenSourcesRankedWindowGraceWinAndTierBoundary(t *testing.T) {
	t.Run("same tier overtakes after grace and closes late lead", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			payload := testMKVPayload()
			lateLead := &trackedMediaSource{Reader: bytes.NewReader(payload), id: "lead", name: "lead.flac"}
			sources := []core.PlayableSource{
				hashSource("lead", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", tierPtr(1), true),
				hashSource("backup", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", tierPtr(1), true),
			}
			bs, win, err := OpenSources(context.Background(), sources, Deps{
				OpenReleaseSource: func(ctx context.Context, src core.PlayableSource) (core.MediaSource, error) {
					if src.ID == "lead" {
						// Simulate the tight race where lead returns a live
						// source right as cancellation arrives.
						<-ctx.Done()
						return lateLead, nil
					}
					return &trackedMediaSource{Reader: bytes.NewReader(payload), id: "backup", name: "backup.flac"}, nil
				},
			})
			if err != nil {
				t.Fatalf("OpenSources failed: %v", err)
			}
			defer bs.Close()
			if win.ID != "backup" {
				t.Fatalf("winner = %q, want backup", win.ID)
			}
			if lateLead.closed.Load() != 1 {
				t.Fatalf("late lead Close count = %d, want 1", lateLead.closed.Load())
			}
		})
	})

	t.Run("lower tier cannot overtake higher tier after grace", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			payload := testMKVPayload()
			backupSrc := &trackedMediaSource{Reader: bytes.NewReader(payload), id: "tier2", name: "tier2.flac"}
			sources := []core.PlayableSource{
				hashSource("tier1", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", tierPtr(1), true),
				hashSource("tier2", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", tierPtr(2), true),
			}
			bs, win, err := OpenSources(context.Background(), sources, Deps{
				OpenReleaseSource: func(_ context.Context, src core.PlayableSource) (core.MediaSource, error) {
					if src.ID == "tier1" {
						time.Sleep(resolver.RankedSourceGrace + 500*time.Millisecond)
						return &trackedMediaSource{Reader: bytes.NewReader(payload), id: "tier1", name: "tier1.flac"}, nil
					}
					return backupSrc, nil
				},
			})
			if err != nil {
				t.Fatalf("OpenSources failed: %v", err)
			}
			defer bs.Close()
			if win.ID != "tier1" {
				t.Fatalf("winner = %q, want tier1 (tier 2 must not overtake tier 1)", win.ID)
			}
			if backupSrc.closed.Load() != 1 {
				t.Fatalf("losing tier2 Close count = %d, want 1", backupSrc.closed.Load())
			}
		})
	})
}

func TestOpenSourcesRankedWindowWidening(t *testing.T) {
	t.Run("widens to four after two confirmed failures", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			payload := testMKVPayload()
			started := make([]chan struct{}, 6)
			for i := range started {
				started[i] = make(chan struct{})
			}
			fail0 := make(chan struct{})
			fail1 := make(chan struct{})
			finishRest := make(chan struct{})

			sources := make([]core.PlayableSource, 6)
			for i := range sources {
				sources[i] = hashSource(fmt.Sprintf("s%d", i), fmt.Sprintf("%040x", i+1), tierPtr(1), true)
			}

			type result struct {
				bs  ByteSource
				win core.PlayableSource
				err error
			}
			done := make(chan result, 1)
			go func() {
				bs, win, err := OpenSources(context.Background(), sources, Deps{
					OpenReleaseSource: func(ctx context.Context, src core.PlayableSource) (core.MediaSource, error) {
						var idx int
						_, _ = fmt.Sscanf(src.ID, "s%d", &idx)
						close(started[idx])
						switch idx {
						case 0:
							<-fail0
							return nil, core.ErrArticleMissing
						case 1:
							<-fail1
							return nil, core.ErrReleaseRejected
						case 5:
							<-finishRest
							return &trackedMediaSource{Reader: bytes.NewReader(payload), id: src.ID, name: "s5.flac"}, nil
						default:
							select {
							case <-finishRest:
								return nil, core.ErrArticleMissing
							case <-ctx.Done():
								return nil, ctx.Err()
							}
						}
					},
				})
				done <- result{bs: bs, win: win, err: err}
			}()

			// Initially width 2: s0 and s1.
			<-started[0]
			<-started[1]
			synctest.Wait()
			select {
			case <-started[2]:
				t.Fatal("s2 started before any failure")
			default:
			}

			// First confirmed failure -> width still 2 (s1 + s2).
			close(fail0)
			<-started[2]
			synctest.Wait()
			select {
			case <-started[3]:
				t.Fatal("s3 started after only 1 confirmed failure")
			default:
			}

			// Second confirmed failure -> widens to 4 (s2, s3, s4, s5 all in flight).
			close(fail1)
			<-started[3]
			<-started[4]
			<-started[5]

			close(finishRest)
			r := <-done
			if r.err != nil {
				t.Fatalf("OpenSources failed: %v", r.err)
			}
			defer r.bs.Close()
			if r.win.ID != "s5" {
				t.Fatalf("winner = %q, want s5", r.win.ID)
			}
		})
	})

	t.Run("transient failures do not widen", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			payload := testMKVPayload()
			started := make([]chan struct{}, 5)
			for i := range started {
				started[i] = make(chan struct{})
			}
			release2 := make(chan struct{})
			sources := make([]core.PlayableSource, 5)
			for i := range sources {
				sources[i] = hashSource(fmt.Sprintf("s%d", i), fmt.Sprintf("%040x", i+1), tierPtr(1), true)
			}
			type result struct {
				bs  ByteSource
				win core.PlayableSource
				err error
			}
			done := make(chan result, 1)
			go func() {
				bs, win, err := OpenSources(context.Background(), sources, Deps{
					OpenReleaseSource: func(ctx context.Context, src core.PlayableSource) (core.MediaSource, error) {
						var idx int
						_, _ = fmt.Sscanf(src.ID, "s%d", &idx)
						close(started[idx])
						switch idx {
						case 0, 1:
							return nil, core.ErrSourceUnavailable // transient
						case 2:
							<-release2
							return &trackedMediaSource{Reader: bytes.NewReader(payload), id: src.ID, name: "s2.flac"}, nil
						default:
							<-ctx.Done()
							return nil, ctx.Err()
						}
					},
				})
				done <- result{bs: bs, win: win, err: err}
			}()

			<-started[0]
			<-started[1]
			<-started[2]
			<-started[3]
			synctest.Wait()
			// Width must stay 2 (s2 and s3 in flight), so s4 must NOT start.
			select {
			case <-started[4]:
				t.Fatal("s4 started after transient failures (must not widen)")
			default:
			}
			close(release2)
			r := <-done
			if r.err != nil {
				t.Fatalf("OpenSources failed: %v", r.err)
			}
			defer r.bs.Close()
			if r.win.ID != "s2" {
				t.Fatalf("winner = %q, want s2", r.win.ID)
			}
		})
	})
}

func TestOpenSourcesGrabHedgeDelay(t *testing.T) {
	t.Run("healthy NZB costs one grab", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			mkv := append([]byte{0x1a, 0x45, 0xdf, 0xa3}, bytes.Repeat([]byte{0x22}, 512)...)
			var secondStarted atomic.Bool
			sources := []core.PlayableSource{nzbSource("n1", nil), nzbSource("n2", nil)}
			bs, win, err := OpenSources(context.Background(), sources, Deps{
				OpenReleaseSource: func(_ context.Context, src core.PlayableSource) (core.MediaSource, error) {
					if src.ID == "n2" {
						secondStarted.Store(true)
					}
					time.Sleep(200 * time.Millisecond)
					return &trackedMediaSource{Reader: bytes.NewReader(mkv), id: src.ID, name: "m.mkv"}, nil
				},
			})
			if err != nil {
				t.Fatalf("OpenSources failed: %v", err)
			}
			defer bs.Close()
			if win.ID != "n1" || secondStarted.Load() {
				t.Fatalf("winner=%q secondStarted=%v, want n1 with no second grab", win.ID, secondStarted.Load())
			}
		})
	})

	t.Run("slow NZB hedges after DefaultHealthHedgeDelay", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			mkv := append([]byte{0x1a, 0x45, 0xdf, 0xa3}, bytes.Repeat([]byte{0x22}, 512)...)
			var hedgeAt time.Duration
			start := time.Now()
			sources := []core.PlayableSource{nzbSource("n1", nil), nzbSource("n2", nil)}
			bs, win, err := OpenSources(context.Background(), sources, Deps{
				OpenReleaseSource: func(ctx context.Context, src core.PlayableSource) (core.MediaSource, error) {
					if src.ID == "n1" {
						<-ctx.Done()
						return nil, ctx.Err()
					}
					hedgeAt = time.Since(start)
					return &trackedMediaSource{Reader: bytes.NewReader(mkv), id: src.ID, name: "m.mkv"}, nil
				},
			})
			if err != nil {
				t.Fatalf("OpenSources failed: %v", err)
			}
			defer bs.Close()
			if win.ID != "n2" {
				t.Fatalf("winner = %q, want n2", win.ID)
			}
			if hedgeAt != resolver.DefaultHealthHedgeDelay {
				t.Fatalf("second grab started at %v, want %v", hedgeAt, resolver.DefaultHealthHedgeDelay)
			}
		})
	})

	t.Run("torrentURL without hash also hedges", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			mkv := append([]byte{0x1a, 0x45, 0xdf, 0xa3}, bytes.Repeat([]byte{0x22}, 512)...)
			var hedgeAt time.Duration
			start := time.Now()
			sources := []core.PlayableSource{torrentURLSource("t1", nil), torrentURLSource("t2", nil)}
			bs, win, err := OpenSources(context.Background(), sources, Deps{
				OpenReleaseSource: func(ctx context.Context, src core.PlayableSource) (core.MediaSource, error) {
					if src.ID == "t1" {
						<-ctx.Done()
						return nil, ctx.Err()
					}
					hedgeAt = time.Since(start)
					return &trackedMediaSource{Reader: bytes.NewReader(mkv), id: src.ID, name: "m.mkv"}, nil
				},
			})
			if err != nil {
				t.Fatalf("OpenSources failed: %v", err)
			}
			defer bs.Close()
			if win.ID != "t2" || hedgeAt != resolver.DefaultHealthHedgeDelay {
				t.Fatalf("winner=%q hedgeAt=%v, want t2 at %v", win.ID, hedgeAt, resolver.DefaultHealthHedgeDelay)
			}
		})
	})

	t.Run("hash torrent joins immediately beside NZB lead", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			mkv := append([]byte{0x1a, 0x45, 0xdf, 0xa3}, bytes.Repeat([]byte{0x22}, 512)...)
			hashStarted := make(chan struct{})
			sources := []core.PlayableSource{
				nzbSource("n1", nil),
				hashSource("h1", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", nil, true),
			}
			bs, win, err := OpenSources(context.Background(), sources, Deps{
				OpenReleaseSource: func(ctx context.Context, src core.PlayableSource) (core.MediaSource, error) {
					if src.ID == "n1" {
						<-hashStarted
						return &trackedMediaSource{Reader: bytes.NewReader(mkv), id: "n1", name: "m.mkv"}, nil
					}
					close(hashStarted)
					<-ctx.Done()
					return nil, ctx.Err()
				},
			})
			if err != nil {
				t.Fatalf("OpenSources failed: %v", err)
			}
			defer bs.Close()
			if win.ID != "n1" {
				t.Fatalf("winner = %q, want n1", win.ID)
			}
		})
	})

	t.Run("learned startup stats adjust hedge delay", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			svc := newTestResolver(resolver.PreferAuto, false)
			defer svc.Close()
			for range 5 {
				svc.ObserveSourceStartup(core.Release{Protocol: core.ProtocolUsenet}, 800*time.Millisecond)
			}
			mkv := append([]byte{0x1a, 0x45, 0xdf, 0xa3}, bytes.Repeat([]byte{0x22}, 512)...)
			var hedgeAt time.Duration
			start := time.Now()
			sources := []core.PlayableSource{nzbSource("n1", nil), nzbSource("n2", nil)}
			bs, win, err := OpenSources(context.Background(), sources, Deps{
				SourceRouter: svc,
				OpenReleaseSource: func(ctx context.Context, src core.PlayableSource) (core.MediaSource, error) {
					if src.ID == "n1" {
						<-ctx.Done()
						return nil, ctx.Err()
					}
					hedgeAt = time.Since(start)
					return &trackedMediaSource{Reader: bytes.NewReader(mkv), id: src.ID, name: "m.mkv"}, nil
				},
			})
			if err != nil {
				t.Fatalf("OpenSources failed: %v", err)
			}
			defer bs.Close()
			if win.ID != "n2" || hedgeAt != time.Second {
				t.Fatalf("winner=%q hedgeAt=%v, want n2 at 1s (800ms*1.25)", win.ID, hedgeAt)
			}
		})
	})
}

func TestOpenSourcesTierRoutingAndStartupStats(t *testing.T) {
	t.Run("untiered sources keep exact plugin order", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			svc := newTestResolver(resolver.PreferDebrid, true)
			defer svc.Close()
			mkv := append([]byte{0x1a, 0x45, 0xdf, 0xa3}, bytes.Repeat([]byte{0x22}, 512)...)
			sources := []core.PlayableSource{
				nzbSource("usenet-first", nil),
				hashSource("debrid-second", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", nil, true),
			}
			bs, win, err := OpenSources(context.Background(), sources, Deps{
				SourceRouter: svc,
				OpenReleaseSource: func(_ context.Context, src core.PlayableSource) (core.MediaSource, error) {
					return &trackedMediaSource{Reader: bytes.NewReader(mkv), id: src.ID, name: "m.mkv"}, nil
				},
			})
			if err != nil {
				t.Fatalf("OpenSources failed: %v", err)
			}
			defer bs.Close()
			if win.ID != "usenet-first" {
				t.Fatalf("untiered winner = %q, want usenet-first (plugin order preserved)", win.ID)
			}
		})
	})

	t.Run("explicit owner preference reorders within tier and hedges other protocol", func(t *testing.T) {
		for _, tc := range []struct {
			name      string
			pref      string
			sources   []core.PlayableSource
			wantOrder []string
		}{
			{
				name: "prefer debrid",
				pref: resolver.PreferDebrid,
				sources: []core.PlayableSource{
					nzbSource("u1", tierPtr(1)),
					nzbSource("u2", tierPtr(1)),
					hashSource("d1", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", tierPtr(1), true),
				},
				wantOrder: []string{"d1", "u1", "u2"},
			},
			{
				name: "prefer usenet",
				pref: resolver.PreferUsenet,
				sources: []core.PlayableSource{
					hashSource("d1", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", tierPtr(1), true),
					hashSource("d2", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", tierPtr(1), true),
					nzbSource("u1", tierPtr(1)),
				},
				wantOrder: []string{"u1", "d1", "d2"},
			},
		} {
			t.Run(tc.name, func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					svc := newTestResolver(tc.pref, false)
					defer svc.Close()
					var mu sync.Mutex
					var launched []string
					// Fail every candidate cleanly so we verify the exact routed order.
					_, _, err := OpenSources(context.Background(), tc.sources, Deps{
						SourceRouter: svc,
						OpenReleaseSource: func(_ context.Context, src core.PlayableSource) (core.MediaSource, error) {
							mu.Lock()
							launched = append(launched, src.ID)
							mu.Unlock()
							// Stagger slightly so candidate 0 fails before candidate 2 starts.
							return nil, core.ErrArticleMissing
						},
					})
					if err == nil {
						t.Fatal("expected all-failed error")
					}
					if len(launched) != len(tc.wantOrder) {
						t.Fatalf("launched = %v, want %v", launched, tc.wantOrder)
					}
					// First source routed to index 0 must win when it succeeds.
					mkv := append([]byte{0x1a, 0x45, 0xdf, 0xa3}, bytes.Repeat([]byte{0x22}, 512)...)
					bs, win, err := OpenSources(context.Background(), tc.sources, Deps{
						SourceRouter: svc,
						OpenReleaseSource: func(_ context.Context, src core.PlayableSource) (core.MediaSource, error) {
							return &trackedMediaSource{Reader: bytes.NewReader(mkv), id: src.ID, name: "m.mkv"}, nil
						},
					})
					if err != nil {
						t.Fatalf("OpenSources failed: %v", err)
					}
					defer bs.Close()
					if win.ID != tc.wantOrder[0] {
						t.Fatalf("winner = %q, want %q", win.ID, tc.wantOrder[0])
					}
					// Verify slot 1 is the cross-protocol hedge by failing slot 0.
					bs2, win2, err := OpenSources(context.Background(), tc.sources, Deps{
						SourceRouter: svc,
						OpenReleaseSource: func(_ context.Context, src core.PlayableSource) (core.MediaSource, error) {
							if src.ID == tc.wantOrder[0] {
								return nil, core.ErrArticleMissing
							}
							return &trackedMediaSource{Reader: bytes.NewReader(mkv), id: src.ID, name: "m.mkv"}, nil
						},
					})
					if err != nil {
						t.Fatalf("OpenSources hedge failed: %v", err)
					}
					defer bs2.Close()
					if win2.ID != tc.wantOrder[1] {
						t.Fatalf("second winner (hedge) = %q, want %q", win2.ID, tc.wantOrder[1])
					}
				})
			})
		}
	})

	t.Run("distinct tiers never cross boundaries", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			// Even with SourceAlwaysFirst, tier 1080 must never jump ahead of tier 2160.
			svc := newTestResolver(resolver.PreferDebrid, true)
			defer svc.Close()
			mkv := append([]byte{0x1a, 0x45, 0xdf, 0xa3}, bytes.Repeat([]byte{0x22}, 512)...)
			sources := []core.PlayableSource{
				nzbSource("usenet-4k", tierPtr(2160)),
				hashSource("debrid-1080", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", tierPtr(1080), true),
			}
			bs, win, err := OpenSources(context.Background(), sources, Deps{
				SourceRouter: svc,
				OpenReleaseSource: func(_ context.Context, src core.PlayableSource) (core.MediaSource, error) {
					return &trackedMediaSource{Reader: bytes.NewReader(mkv), id: src.ID, name: "m.mkv"}, nil
				},
			})
			if err != nil {
				t.Fatalf("OpenSources failed: %v", err)
			}
			defer bs.Close()
			if win.ID != "usenet-4k" {
				t.Fatalf("winner = %q, want usenet-4k (tier boundary preserved)", win.ID)
			}
		})
	})

	t.Run("plugin opens feed shared startup stats and drive auto routing", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			svc := newTestResolver(resolver.PreferAuto, false)
			defer svc.Close()
			mkv := append([]byte{0x1a, 0x45, 0xdf, 0xa3}, bytes.Repeat([]byte{0x22}, 512)...)

			// Record 5 slow Usenet opens (900ms) and 5 fast debrid opens (100ms)
			// through OpenSources itself.
			for i := range 5 {
				bs, _, err := OpenSources(context.Background(), []core.PlayableSource{nzbSource(fmt.Sprintf("u%d", i), nil)}, Deps{
					SourceRouter: svc,
					OpenReleaseSource: func(_ context.Context, src core.PlayableSource) (core.MediaSource, error) {
						time.Sleep(900 * time.Millisecond)
						return &trackedMediaSource{Reader: bytes.NewReader(mkv), id: src.ID, name: "m.mkv"}, nil
					},
				})
				if err != nil {
					t.Fatalf("usenet open %d: %v", i, err)
				}
				_ = bs.Close()

				bs, _, err = OpenSources(context.Background(), []core.PlayableSource{hashSource(fmt.Sprintf("d%d", i), fmt.Sprintf("%040x", i+1), nil, true)}, Deps{
					SourceRouter: svc,
					OpenReleaseSource: func(_ context.Context, src core.PlayableSource) (core.MediaSource, error) {
						time.Sleep(100 * time.Millisecond)
						return &trackedMediaSource{Reader: bytes.NewReader(mkv), id: src.ID, name: "m.mkv"}, nil
					},
				})
				if err != nil {
					t.Fatalf("debrid open %d: %v", i, err)
				}
				_ = bs.Close()
			}

			// Non-release opens (PatternFile) must not pollute startup stats.
			bsFile, _, err := OpenSources(context.Background(), []core.PlayableSource{{
				ID:      "local",
				Pattern: PatternFile,
				MIME:    "audio/flac",
				Path:    "/tmp/a.flac",
			}}, Deps{
				SourceRouter: svc,
				OpenFile: func(_ context.Context, _ core.PlayableSource) (ByteSource, error) {
					return &memByteSource{data: testFLACPayload(), name: "a.flac", mime: "audio/flac"}, nil
				},
			})
			if err != nil {
				t.Fatalf("file open: %v", err)
			}
			_ = bsFile.Close()

			stats := svc.SourceStartups()
			byProto := map[string]resolver.SourceStartup{}
			for _, st := range stats {
				byProto[st.Protocol] = st
			}
			if byProto[core.ProtocolUsenet].Samples != 5 || byProto[core.ProtocolUsenet].Average != 900*time.Millisecond {
				t.Fatalf("usenet startup stats = %+v, want 5 samples @ 900ms", byProto[core.ProtocolUsenet])
			}
			if byProto[core.ProtocolTorrent].Samples != 5 || byProto[core.ProtocolTorrent].Average != 100*time.Millisecond {
				t.Fatalf("torrent startup stats = %+v, want 5 samples @ 100ms", byProto[core.ProtocolTorrent])
			}

			// Now auto routing within a tier picks cached debrid ahead of Usenet.
			sources := []core.PlayableSource{
				nzbSource("usenet", tierPtr(1)),
				hashSource("debrid", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", tierPtr(1), true),
			}
			bs, win, err := OpenSources(context.Background(), sources, Deps{
				SourceRouter: svc,
				OpenReleaseSource: func(_ context.Context, src core.PlayableSource) (core.MediaSource, error) {
					return &trackedMediaSource{Reader: bytes.NewReader(mkv), id: src.ID, name: "m.mkv"}, nil
				},
			})
			if err != nil {
				t.Fatalf("auto-routed OpenSources failed: %v", err)
			}
			defer bs.Close()
			if win.ID != "debrid" {
				t.Fatalf("auto winner = %q, want debrid (measured faster)", win.ID)
			}

			// Reverse case: when Usenet is measured faster than debrid, auto
			// routes Usenet ahead of debrid within the tier.
			svcFastU := newTestResolver(resolver.PreferAuto, false)
			defer svcFastU.Close()
			for i := range 5 {
				svcFastU.ObserveSourceStartup(core.Release{Protocol: core.ProtocolUsenet}, 100*time.Millisecond)
				svcFastU.ObserveSourceStartup(core.Release{Protocol: core.ProtocolTorrent, ID: fmt.Sprintf("d%d", i)}, 900*time.Millisecond)
			}
			bsU, winU, err := OpenSources(context.Background(), []core.PlayableSource{
				hashSource("debrid", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", tierPtr(1), true),
				nzbSource("usenet", tierPtr(1)),
			}, Deps{
				SourceRouter: svcFastU,
				OpenReleaseSource: func(_ context.Context, src core.PlayableSource) (core.MediaSource, error) {
					return &trackedMediaSource{Reader: bytes.NewReader(mkv), id: src.ID, name: "m.mkv"}, nil
				},
			})
			if err != nil {
				t.Fatalf("auto-routed fast-usenet OpenSources failed: %v", err)
			}
			defer bsU.Close()
			if winU.ID != "usenet" {
				t.Fatalf("auto winner (fast usenet) = %q, want usenet", winU.ID)
			}
		})
	})

	t.Run("stall demotion demotes stalled source within tier", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			svc := newTestResolver(resolver.PreferUsenet, false)
			defer svc.Close()
			mkv := append([]byte{0x1a, 0x45, 0xdf, 0xa3}, bytes.Repeat([]byte{0x22}, 512)...)

			usenet := nzbSource("usenet-stalled", tierPtr(1))
			usenet.Size = 4 << 30
			usenet.DurationSec = 3600 // ~1.19 MB/s needed
			debrid := hashSource("debrid-healthy", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", tierPtr(1), true)
			debrid.Size = 4 << 30
			debrid.DurationSec = 3600

			// Record a Usenet playback stall below 1.19 MB/s / 0.75.
			svc.ObserveSourceStall(ReleaseForSource(usenet), 500_000)

			bs, win, err := OpenSources(context.Background(), []core.PlayableSource{usenet, debrid}, Deps{
				SourceRouter: svc,
				OpenReleaseSource: func(_ context.Context, src core.PlayableSource) (core.MediaSource, error) {
					return &trackedMediaSource{Reader: bytes.NewReader(mkv), id: src.ID, name: "m.mkv"}, nil
				},
			})
			if err != nil {
				t.Fatalf("OpenSources failed: %v", err)
			}
			defer bs.Close()
			if win.ID != "debrid-healthy" {
				t.Fatalf("winner after Usenet stall = %q, want debrid-healthy", win.ID)
			}

			// Stall demotion also works when a plugin omits size / durationSec.
			torStalled := hashSource("tor-stalled", "1111111111111111111111111111111111111111", tierPtr(1), true)
			torHealthy := hashSource("tor-healthy", "2222222222222222222222222222222222222222", tierPtr(1), true)
			svc.ObserveSourceStall(ReleaseForSource(torStalled), 200_000)

			bs2, win2, err := OpenSources(context.Background(), []core.PlayableSource{torStalled, torHealthy}, Deps{
				SourceRouter: svc,
				OpenReleaseSource: func(_ context.Context, src core.PlayableSource) (core.MediaSource, error) {
					return &trackedMediaSource{Reader: bytes.NewReader(mkv), id: src.ID, name: "m.mkv"}, nil
				},
			})
			if err != nil {
				t.Fatalf("OpenSources (unsized stall) failed: %v", err)
			}
			defer bs2.Close()
			if win2.ID != "tor-healthy" {
				t.Fatalf("winner after unsized torrent stall = %q, want tor-healthy", win2.ID)
			}
		})
	})
}

type memByteSource struct {
	data []byte
	name string
	mime string
}

func (m *memByteSource) ReadAt(p []byte, off int64) (int, error) {
	return bytes.NewReader(m.data).ReadAt(p, off)
}
func (m *memByteSource) Size() int64  { return int64(len(m.data)) }
func (m *memByteSource) Name() string { return m.name }
func (m *memByteSource) MIME() string { return m.mime }
func (m *memByteSource) Close() error { return nil }
