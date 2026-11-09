package releasedb

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/store"
)

func TestPlayableSetMatchesCorrelatedPredicate(t *testing.T) {
	db := store.OpenTest(t)
	ctx := context.Background()
	for _, q := range []string{
		`CREATE TABLE candidate_titles(key INTEGER PRIMARY KEY)`,
		`WITH RECURSIVE n(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM n WHERE x<100000) INSERT INTO candidate_titles SELECT x FROM n`,
		`INSERT INTO pse_playable(title_key,releases,any_lang) SELECT key, key%7, 4 FROM candidate_titles WHERE key%100=0`,
		`INSERT INTO pse_lang_exempt(title_key) SELECT key FROM candidate_titles WHERE key%300=0`,
		`INSERT INTO pse_index_titles(title_key,media_type) SELECT key,'movie' FROM candidate_titles WHERE key%2=0`,
	} {
		if _, err := db.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	for _, pending := range []bool{false, true} {
		p := NewPlayable(2, "", pending)
		fast, args := p.PlayableSQL("t.key")
		old := "(EXISTS (SELECT 1 FROM pse_playable pp WHERE pp.title_key=t.key AND (pp.releases>=? OR (pp.any_lang>=? AND " + LangExemptSQL("t.key") + ")))"
		if pending {
			old += " OR NOT EXISTS (SELECT 1 FROM pse_index_titles pi WHERE pi.title_key=t.key)"
		}
		old += ")"
		for _, predicate := range []string{fast, old} {
			var n int
			start := time.Now()
			if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM candidate_titles t WHERE "+predicate, args...).Scan(&n); err != nil {
				t.Fatal(err)
			}
			t.Logf("pending=%v new=%v count=%d duration=%v", pending, predicate == fast, n, time.Since(start))
		}
		var difference int
		if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM candidate_titles t WHERE ("+fast+") != ("+old+")", append(args, args...)...).Scan(&difference); err != nil || difference != 0 {
			t.Fatalf("different membership: %d %v", difference, err)
		}
		if !pending {
			rows, err := db.QueryContext(ctx, "EXPLAIN QUERY PLAN SELECT COUNT(*) FROM candidate_titles t WHERE "+fast, args...)
			if err != nil {
				t.Fatal(err)
			}
			plan := ""
			for rows.Next() {
				var a, b, c int
				var detail string
				if err := rows.Scan(&a, &b, &c, &detail); err != nil {
					t.Fatal(err)
				}
				plan += detail + "\n"
			}
			rows.Close()
			if !strings.Contains(plan, "SEARCH t USING INTEGER PRIMARY KEY") {
				t.Fatal(fmt.Sprintf("catalogue scan returned:\n%s", plan))
			}
			t.Log(plan)
		}
	}
}
