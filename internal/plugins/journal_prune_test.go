package plugins

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/store"
)

// Journal retention drops settled entries past JournalSettledMaxAge and trims
// each plugin's live undo journal to the reachable window (age, entry count,
// and cumulative inverse byte budget) in bounded batches without affecting
// other plugins (#766).
func TestPruneJournalBoundsLiveEntries(t *testing.T) {
	ctx := context.Background()
	db := store.OpenTest(t)
	now := time.Unix(1_800_000_000, 0)
	st := &storage{db: db, now: func() time.Time { return now }}

	insert := func(plugin, op, inverse string, shadow, undone int, created int64) {
		t.Helper()
		if _, err := db.ExecContext(ctx,
			`INSERT INTO plugins_journal(plugin,op,inverse,shadow,undone,created) VALUES(?,?,?,?,?,?)`,
			plugin, op, inverse, shadow, undone, created); err != nil {
			t.Fatal(err)
		}
	}

	settledOld := now.Add(-JournalSettledMaxAge - time.Hour).Unix()
	liveExpired := now.Add(-JournalLiveMaxAge - time.Hour).Unix()
	liveRecent := now.Add(-time.Hour).Unix()

	// Settled + live age bounds on plugin "age".
	insert("age", "live-expired", `{}`, 0, 0, liveExpired)
	insert("age", "undone-old", `{}`, 0, 1, settledOld)
	insert("age", "shadow-old", `{}`, 1, 0, settledOld)
	insert("age", "undone-new", `{}`, 0, 1, now.Unix())
	insert("age", "live-recent", `{}`, 0, 0, liveRecent)

	// Entry-count bound on plugin "count": 7 live entries with maxEntries=3
	// and batchSize=2 exercises multi-batch oldest-first deletion.
	for i := 1; i <= 7; i++ {
		insert("count", "op-"+string(rune('0'+i)), `{}`, 0, 0, liveRecent+int64(i))
	}

	// Byte-budget bound on plugin "bytes": 4 live entries of 60 bytes each
	// with maxBytes=130 keeps only the newest 2 (120 <= 130).
	payload60 := `{"pad":"` + strings.Repeat("a", 50) + `"}`
	if len(payload60) != 60 {
		t.Fatalf("payload60 len = %d", len(payload60))
	}
	for i := 1; i <= 4; i++ {
		insert("bytes", "b-"+string(rune('0'+i)), payload60, 0, 0, liveRecent+int64(i))
	}

	// Separate in-budget plugin must remain completely untouched.
	insert("other", "keep-me", `{}`, 0, 0, liveRecent)

	n, err := st.pruneJournalWithLimits(ctx, 3, JournalLiveMaxAge, 130, 2)
	if err != nil {
		t.Fatalf("pruneJournalWithLimits: %v", err)
	}
	// Expected removals: 2 settled + 1 expired ("age") + 4 excess ("count") + 2 over-budget ("bytes") = 9.
	if n != 9 {
		t.Fatalf("pruned = %d, want 9", n)
	}

	liveAge, err := st.journalList(ctx, "age", 10, true)
	if err != nil || len(liveAge) != 1 || liveAge[0].Op != "live-recent" {
		t.Fatalf("age live entries = %+v, %v", liveAge, err)
	}
	allAge, err := st.journalList(ctx, "age", 10, false)
	if err != nil || len(allAge) != 2 {
		t.Fatalf("age all entries = %+v, %v (want live-recent + undone-new)", allAge, err)
	}

	liveCount, err := st.journalList(ctx, "count", 10, true)
	if err != nil || len(liveCount) != 3 || liveCount[0].Op != "op-7" || liveCount[1].Op != "op-6" || liveCount[2].Op != "op-5" {
		t.Fatalf("count live entries = %+v, %v", liveCount, err)
	}

	liveBytes, err := st.journalList(ctx, "bytes", 10, true)
	if err != nil || len(liveBytes) != 2 || liveBytes[0].Op != "b-4" || liveBytes[1].Op != "b-3" {
		t.Fatalf("bytes live entries = %+v, %v", liveBytes, err)
	}

	liveOther, err := st.journalList(ctx, "other", 10, true)
	if err != nil || len(liveOther) != 1 || liveOther[0].Op != "keep-me" {
		t.Fatalf("other live entries = %+v, %v", liveOther, err)
	}
}
