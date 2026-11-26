package releasedb

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

func TestPlayableRebuildCancellationPreservesPublishedCounts(t *testing.T) {
	e := newFreshEnv(t)
	e.stub.rels["a"] = []core.Release{frRelease("Film.2020.FRENCH.1080p-A1")}
	e.index(t, 1)
	e.index(t, 2)
	st := e.s.Store()
	old := NewPlayable(1, "lang=fr", false)
	if _, err := st.SyncPlayable(e.ctx, old); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(e.ctx)
	defer cancel()
	// Interrupt after a title's releases were read, before publication.
	interrupted := NewStore(st.db)
	interrupted.now = func() time.Time { cancel(); return e.now }
	if _, err := interrupted.SyncPlayable(ctx, NewPlayable(1, "min=2160", false)); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
	for _, key := range []core.RatingKey{1, 2} {
		if got := e.playable(t, key); got != 1 {
			t.Fatalf("key %d: got %d", key, got)
		}
	}
	if n, err := st.SyncPlayable(e.ctx, old); err != nil || n != 0 {
		t.Fatalf("old signature lost: %d, %v", n, err)
	}
	if n, err := st.SyncPlayable(e.ctx, NewPlayable(1, "min=2160", false)); err != nil || n != 2 {
		t.Fatalf("retry: %d, %v", n, err)
	}
	for _, key := range []core.RatingKey{1, 2} {
		if got := e.playable(t, key); got != 0 {
			t.Fatalf("key %d after retry: %d", key, got)
		}
	}
}

func TestPlayablePublicationRollsBackLaterBatchAndSignatureFailure(t *testing.T) {
	for _, failure := range []string{"batch", "signature"} {
		t.Run(failure, func(t *testing.T) {
			e := newFreshEnv(t)
			st := e.s.Store()
			if err := st.publishPlayable(e.ctx, []playableCount{{key: 9999, releases: 7, anyLang: 8}}, "old"); err != nil {
				t.Fatal(err)
			}
			trigger := `CREATE TRIGGER fail_publication BEFORE INSERT ON pse_playable WHEN NEW.title_key=251 BEGIN SELECT RAISE(ABORT,'batch failure'); END`
			if failure == "signature" {
				trigger = `CREATE TRIGGER fail_publication BEFORE INSERT ON pse_state BEGIN SELECT RAISE(ABORT,'signature failure'); END`
			}
			if _, err := st.db.ExecContext(e.ctx, trigger); err != nil {
				t.Fatal(err)
			}
			counts := make([]playableCount, 501)
			for i := range counts {
				counts[i] = playableCount{key: core.RatingKey(i + 1), releases: 2, anyLang: 3}
			}
			if err := st.publishPlayable(e.ctx, counts, "new"); err == nil {
				t.Fatal("expected failure")
			}
			var n, anyLang int
			var sig string
			if err := st.db.QueryRowContext(e.ctx, `SELECT count(*),max(any_lang) FROM pse_playable`).Scan(&n, &anyLang); err != nil {
				t.Fatal(err)
			}
			if n != 1 || anyLang != 8 || e.playable(t, 9999) != 7 {
				t.Fatal("old counts changed")
			}
			if err := st.db.QueryRowContext(e.ctx, `SELECT v FROM pse_state WHERE k='playable_signature'`).Scan(&sig); err != nil || sig != "old" {
				t.Fatalf("signature: %q, %v", sig, err)
			}
			if _, err := st.db.ExecContext(e.ctx, `DROP TRIGGER fail_publication`); err != nil {
				t.Fatal(err)
			}
			if err := st.publishPlayable(e.ctx, counts, "new"); err != nil {
				t.Fatal(err)
			}
			if err := st.db.QueryRowContext(e.ctx, `SELECT count(*) FROM pse_playable`).Scan(&n); err != nil || n != 501 {
				t.Fatalf("retry: %d %v", n, err)
			}
		})
	}
}

func TestPlayableRefreshWaitIsCancellableAcrossStores(t *testing.T) {
	e := newFreshEnv(t)
	unlock, err := e.s.Store().lockPlayable(e.ctx)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(e.ctx)
	cancel()
	other := NewStore(e.s.Store().db)
	if err := other.RefreshPlayable(ctx, 1, Playable()); !errors.Is(err, context.Canceled) {
		t.Fatalf("refresh: %v", err)
	}
	if _, err := other.SyncPlayable(ctx, Playable()); !errors.Is(err, context.Canceled) {
		t.Fatalf("sync: %v", err)
	}
	unlock()
	if err := other.RefreshPlayable(e.ctx, 1, Playable()); err != nil {
		t.Fatal(err)
	}
	playableLocks.Lock()
	defer playableLocks.Unlock()
	if _, ok := playableLocks.byDB[other.db]; ok {
		t.Fatal("lock retained after last user")
	}
}

func TestPlayableRebuildKeepsConcurrentRefreshAndNewTitle(t *testing.T) {
	e := newFreshEnv(t)
	e.stub.rels["a"] = []core.Release{frRelease("Film.2020.FRENCH.1080p-A1")}
	e.index(t, 1)
	st := e.s.Store()
	started, resume := make(chan struct{}), make(chan struct{})
	released := false
	defer func() {
		if !released {
			close(resume)
		}
	}()
	rebuilding := NewStore(st.db)
	rebuilding.now = func() time.Time { close(started); <-resume; return e.now }
	done := make(chan error, 1)
	go func() { _, err := rebuilding.SyncPlayable(e.ctx, Playable()); done <- err }()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("rebuild did not start")
	}
	// The rebuild has already read the old release. Publish a newer zero count
	// and a newly indexed title without waiting for the full calculation.
	if _, err := st.db.ExecContext(e.ctx, `UPDATE pse_releases SET languages='english' WHERE title_key=1`); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(e.ctx, 2*time.Second)
	defer cancel()
	if err := st.RefreshPlayable(ctx, 1, Playable()); err != nil {
		t.Fatal(err)
	}
	e.index(t, 2)
	close(resume)
	released = true
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if got := e.playable(t, 1); got != 0 {
		t.Fatalf("concurrent refresh overwritten: %d", got)
	}
	if got := e.playable(t, 2); got != 1 {
		t.Fatalf("new title lost: %d", got)
	}
}

func TestPlayablePublicationLargeCatalogue(t *testing.T) {
	e := newFreshEnv(t)
	counts := make([]playableCount, 27329)
	for i := range counts {
		counts[i] = playableCount{key: core.RatingKey(i + 1), releases: 2, anyLang: 3}
	}
	start := time.Now()
	if err := e.s.Store().publishPlayable(e.ctx, counts, "large"); err != nil {
		t.Fatal(err)
	}
	t.Logf("publication de %d titres : %s", len(counts), time.Since(start))
	var n int
	if err := e.s.Store().db.QueryRowContext(e.ctx, `SELECT count(*) FROM pse_playable WHERE releases=2 AND any_lang=3`).Scan(&n); err != nil || n != len(counts) {
		t.Fatalf("counts: %d %v", n, err)
	}
}

func TestPlayableRebuildDoesNotUndoInvalidation(t *testing.T) {
	e := newFreshEnv(t)
	e.stub.rels["a"] = []core.Release{frRelease("Film.2020.FRENCH.1080p-A1")}
	e.index(t, 1)
	st := e.s.Store()
	rebuilding := NewStore(st.db)
	rebuilding.now = func() time.Time {
		if err := st.ForgetPlayable(e.ctx); err != nil {
			t.Fatal(err)
		}
		return e.now
	}
	if _, err := rebuilding.SyncPlayable(e.ctx, NewPlayable(1, "min=2160", false)); err == nil {
		t.Fatal("invalidated rebuild published")
	}
	if got := e.playable(t, 1); got != 1 {
		t.Fatalf("old count lost: %d", got)
	}
	var n int
	if err := st.db.QueryRowContext(e.ctx, `SELECT count(*) FROM pse_state WHERE k='playable_signature'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("invalidation lost: %d %v", n, err)
	}
}
