package usenet

import (
	"context"
	"errors"
	"io"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/nzb"
	"github.com/braedonsaunders/ether/internal/sockerr"
	"github.com/braedonsaunders/ether/internal/speedtest"
)

// SpeedTest measures uncached BODY reads on exactly one saved provider.
func (f *Fetcher) SpeedTest(ctx context.Context, id string, doc *nzb.NZB, playback bool) speedtest.Result {
	var p *pool
	f.mu.RLock()
	for _, candidate := range f.pools {
		if candidate.cfg.ID == id {
			p = candidate
			break
		}
	}
	f.mu.RUnlock()
	r := speedtest.Result{Status: "unavailable", SpareOnly: playback}
	if p == nil {
		return r
	}
	type article struct {
		id     string
		groups []string
	}
	// Keep the sample bounded even for season packs. Use distinct articles
	// across the release so the result does not benchmark one hot server item.
	const maxArticles = 32768
	articles := make([]article, 0, min(maxArticles, 4096))
	seen := make(map[string]struct{})
	for _, file := range doc.Files {
		if file == nil {
			continue
		}
		for _, seg := range file.Segments {
			if seg.MessageID == "" {
				continue
			}
			if _, found := seen[seg.MessageID]; found {
				continue
			}
			seen[seg.MessageID] = struct{}{}
			articles = append(articles, article{seg.MessageID, file.Groups})
			if len(articles) == maxArticles {
				break
			}
		}
		if len(articles) == maxArticles || ctx.Err() != nil {
			break
		}
	}
	if len(articles) == 0 || ctx.Err() != nil {
		return r
	}

	poolState := p.sem.stats()
	r.SpareOnly = playback || poolState.InUse > 0
	reserve := 0
	if r.SpareOnly {
		reserve = 1
	}
	if p.brk.idleAllowed() != nil {
		r.Status = "paused"
		return r
	}
	start := time.Now()
	testCtx, stop := context.WithTimeout(ctx, speedtest.Duration)
	defer stop()
	var wg sync.WaitGroup
	var next, total, used, opened, refused, misses atomic.Int64
	var yielded atomic.Bool
	admitted := 0
	for range min(poolState.Size, len(articles)) {
		worker, cancel := context.WithCancel(testCtx)
		if !p.sem.trySpeedTest(&cancel, reserve) {
			cancel()
			r.SpareOnly = true
			break
		}
		admitted++
		wg.Go(func() {
			defer cancel()
			defer func() {
				if errors.Is(worker.Err(), context.Canceled) && testCtx.Err() == nil {
					yielded.Store(true)
				}
			}()
			c, fresh, err := p.checkout(worker)
			if err != nil {
				p.sem.doneSpeedTest(&cancel)
				p.sem.Release(core.PriorityLive)
				if errors.Is(err, ErrServiceUnavailable) || sockerr.IsConnRefused(err) {
					refused.Add(1)
				}
				return
			}
			defer func() {
				p.sem.doneSpeedTest(&cancel)
				p.put(c, core.PriorityLive)
			}()
			used.Add(1)
			if fresh {
				opened.Add(1)
			}
			for worker.Err() == nil && p.brk.idleAllowed() == nil {
				i := int(next.Add(1) - 1)
				if i >= len(articles) {
					return
				}
				a := articles[i]
				if p.o.UseGroup && len(a.groups) > 0 && !slices.Contains(a.groups, c.CurrentGroup()) {
					if err := c.Group(worker, a.groups[0]); err != nil && !errors.Is(err, ErrNoSuchGroup) {
						p.failed(err, !fresh, worker)
						return
					}
				}
				reader, err := c.Body(worker, a.id)
				if err == nil {
					var n int64
					n, err = io.Copy(io.Discard, reader)
					total.Add(n)
					_ = reader.Close()
				}
				switch {
				case err == nil:
					p.brk.success()
				case errors.Is(err, ErrServiceUnavailable):
					refused.Add(1)
					p.failed(err, !fresh, worker)
					return
				case errors.Is(err, ErrNoSuchArticle):
					misses.Add(1)
					p.failed(err, !fresh, worker)
				default:
					p.failed(err, !fresh, worker)
					return
				}
				fresh = false
			}
		})
	}
	wg.Wait()
	if admitted == 0 {
		r.Status = "busy"
	}
	r.SpareOnly = r.SpareOnly || yielded.Load()
	r.Bytes, r.Connections, r.Opened = total.Load(), int(used.Load()), int(opened.Load())
	r.Refused, r.Misses = int(refused.Load()), int(misses.Load())
	r.Measure(start)
	return r
}
