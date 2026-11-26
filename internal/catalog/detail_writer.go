package catalog

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

// Detail fills arrive in clumps (WarmDetails keeps eight fetches in flight,
// a Home build hydrates a page of titles), and a transaction per title
// rewrote the same shared index pages over and over: on the lab catalogue
// each page a fill touched was written about five times. Detail writes are
// group-committed: one that finds no flush running commits at once (no
// added latency), and those that arrive while a flush runs commit together
// as soon as it ends. A group holds as many titles as fit in
// backgroundWriteHold at the hold measured so far (write_hold.go), at most
// detailBatchMax: a fixed count held the writer 1-5 s at a time on a small
// Docker host (#804). A failed group is retried title by title, so one bad
// title fails only its own write.
const detailBatchMax = 32

// detailWriteBudget bounds a group of several detail titles: one that
// runs over is rolled back and retried as smaller groups, so queued viewer
// writes get their turn. A single title is not budgeted: rolling it back
// would only repeat the same hold on the next fetch, every time (#804).
// This bounds SQL execution, not an uninterruptible OS fsync.
const detailWriteBudget = 2 * time.Second

type detailWrites struct {
	mu       sync.Mutex
	pending  []detailWrite
	flushing bool
}

type detailWrite struct {
	ctx  context.Context // the writer's, for its values (query stats)
	item upsertItem
	// extra is written after the title commits, as summaries (the
	// recommendations a detail fetch carried), before the writer returns;
	// a failure of extra never fails item.
	extra *detailExtra
	done  chan error
}

// detailExtra is more writes that follow a detail write.
type detailExtra struct {
	items []upsertItem
	after func(context.Context, *sql.Tx) error
}

// writeDetail stores one fetched title, batched with concurrent detail
// writes. It returns once the title's transaction committed (or failed).
func (c *Catalog) writeDetail(ctx context.Context, t core.Title, o upsertOpts, extra ...*detailExtra) error {
	w := detailWrite{ctx: ctx, item: upsertItem{t: t, o: o}, done: make(chan error, 1)}
	if len(extra) > 0 {
		w.extra = extra[0]
	}
	dw := &c.detailWrites
	dw.mu.Lock()
	dw.pending = append(dw.pending, w)
	start := !dw.flushing
	dw.flushing = true
	dw.mu.Unlock()
	if start {
		go c.flushDetailsLoop()
	}
	select {
	case err := <-w.done:
		return err
	case <-ctx.Done():
		// The write goes ahead; the caller stops waiting for it.
		return ctx.Err()
	}
}

func (c *Catalog) flushDetailsLoop() {
	dw := &c.detailWrites
	for {
		dw.mu.Lock()
		n := min(len(dw.pending), c.holds.detail.size(detailHold))
		if n == 0 {
			dw.flushing = false
			dw.mu.Unlock()
			return
		}
		batch := append([]detailWrite(nil), dw.pending[:n]...)
		dw.pending = append(dw.pending[:0], dw.pending[n:]...)
		dw.mu.Unlock()
		c.flushDetails(batch)
	}
}

func (c *Catalog) flushDetails(batch []detailWrite) {
	c.flushDetailsBudget(batch, detailWriteBudget)
}

func (c *Catalog) flushDetailsBudget(batch []detailWrite, budget time.Duration) {
	if len(batch) == 0 {
		return
	}
	// Not cancelled with any one caller: a cancelled request must not fail
	// the others' writes. The first writer's values (its query stats) see
	// the batch's work. Bounded like a background refresh.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(batch[0].ctx), time.Minute)
	defer cancel()
	var flush func([]detailWrite)
	flush = func(ws []detailWrite) {
		items := make([]upsertItem, len(ws))
		for i, w := range ws {
			items[i] = w.item
		}
		// upsertTx gives every attempt a fresh execution budget, without
		// extending this group's original one-minute lifetime.
		b := budget
		if len(ws) == 1 {
			b = 0
		}
		held, err := c.upsertTx(ctx, items, b)
		c.holds.detail.observe(detailHold, len(ws), held)
		if err != nil && len(ws) > 1 && ctx.Err() == nil {
			// upsertTx has rolled back and released admission before
			// this split. Never replay a partially committed group.
			n := len(ws) / 2
			flush(ws[:n])
			flush(ws[n:])
			return
		}
		// A failed primary write is reported; it is never counted as a
		// successful touch or silently discarded.
		if err != nil {
			for _, w := range ws {
				w.done <- err
			}
			return
		}
		// The recommendations follow as summaries, in transactions sized
		// like any summary write, so a title's hold does not carry a few
		// dozen more rows. Each list row commits after its titles, and
		// the callers return once they are stored, as when they shared
		// the title's transaction.
		var extra []upsertItem
		var after []func(context.Context, *sql.Tx) error
		for _, w := range ws {
			if w.extra != nil {
				extra = append(extra, w.extra.items...)
				if w.extra.after != nil {
					after = append(after, w.extra.after)
				}
			}
		}
		if len(extra) > 0 || len(after) > 0 {
			if err := c.upsertSummaryChunks(ctx, extra, budget, after...); err != nil && ctx.Err() == nil {
				c.log.Debug("catalog: detail recommendations not stored", "err", err)
			}
		}
		for _, w := range ws {
			w.done <- nil
		}
	}
	flush(batch)
}

// upsertSeasonRows publishes provider season payloads in short transactions.
// The season header and episode payload already commit separately; each chunk
// is independently readable, and a later refresh can finish an interrupted fill.
// In particular guest-star credits must not hold the sole writer for an entire
// long anime season: chunks are sized by measured hold (write_hold.go), and a
// timed-out chunk rolls back before smaller retries. A single row is not
// budgeted, so a heavy episode is stored rather than retried forever.
func (c *Catalog) upsertSeasonRows(ctx context.Context, titles []core.Title, o upsertOpts) error {
	var write func([]core.Title) error
	write = func(titles []core.Title) error {
		items := make([]upsertItem, len(titles))
		for i, t := range titles {
			items[i] = upsertItem{t: t, o: o}
		}
		budget := detailWriteBudget
		if len(titles) == 1 {
			budget = 0
		}
		held, err := c.upsertTx(ctx, items, budget, func(ctx context.Context, tx *sql.Tx) error {
			for _, t := range titles {
				var ts int64
				if !t.Released.IsZero() {
					ts = t.Released.Unix()
				}
				if _, err := tx.ExecContext(ctx, `INSERT INTO catalog_episode_provider_air(key,air_ts) VALUES (?,?)
 ON CONFLICT(key) DO UPDATE SET air_ts=excluded.air_ts`, t.Key, ts); err != nil {
					return err
				}
			}
			return nil
		})
		c.holds.seasonRows.observe(seasonRowHold, len(titles), held)
		if !errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil || len(titles) <= 1 {
			return err
		}
		n := len(titles) / 2
		if err := write(titles[:n]); err != nil {
			return err
		}
		return write(titles[n:])
	}
	for start := 0; start < len(titles); {
		n := min(c.holds.seasonRows.size(seasonRowHold), len(titles)-start)
		if err := write(titles[start : start+n]); err != nil {
			return err
		}
		start += n
	}
	return nil
}
