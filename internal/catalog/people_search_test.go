package catalog

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

func TestPeopleSearchMigrationBackfillAndDiacritics(t *testing.T) {
	ctx := context.Background()
	db := store.OpenTest(t)
	now := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	c, err := New(db, nil, config.Catalog{}, nil, WithNow(func() time.Time { return now }))
	if err != nil {
		t.Fatal(err)
	}

	// Simulate pre-60 rows (remove_diacritics 0) on SQLite, then re-run migration 60's
	// backfill in place so existing catalog_person_names rows are proven to upgrade cleanly.
	if store.DialectOf(db) == store.SQLite {
		for _, stmt := range []string{
			`DROP TABLE IF EXISTS catalog_person_names_fts`,
			`CREATE VIRTUAL TABLE catalog_person_names_fts USING fts5(name, content='catalog_person_names', content_rowid='person_tmdb', tokenize='unicode61 remove_diacritics 0', prefix='1 2 3')`,
		} {
			if _, err := db.ExecContext(ctx, stmt); err != nil {
				t.Fatal(err)
			}
		}
	}

	titles := []core.Title{
		{
			Key: core.MovieKey(101), Type: core.TypeMovie, Title: "Dune", Year: 2021,
			Released: now.AddDate(-3, 0, 0), Popularity: 120, VoteCount: 15000,
			Cast:      []core.Person{{TMDB: 1640, Name: "Stellan Skarsgård", Role: "Baron Harkonnen", ThumbPath: "/stellan.jpg"}},
			Directors: []core.Person{{TMDB: 137427, Name: "Denis Villeneuve", ThumbPath: "/denis.jpg"}},
		},
		{
			Key: core.ShowKey(202), Type: core.TypeShow, Title: "Andor", Year: 2022,
			Released: now.AddDate(-2, 0, 0), Popularity: 95, VoteCount: 4000,
			Cast:    []core.Person{{TMDB: 1640, Name: "Stellan Skarsgård", Role: "Luthen Rael"}, {TMDB: 8784, Name: "Adria Arjona", Role: "Bix Caleen"}},
			Writers: []core.Person{{TMDB: 20803, Name: "Tony Gilroy"}},
		},
		{
			Key: core.MovieKey(103), Type: core.TypeMovie, Title: "Oppenheimer", Year: 2023,
			Released: now.AddDate(-1, 0, 0), Popularity: 150, VoteCount: 18000,
			Cast:      []core.Person{{TMDB: 2037, Name: "Cillian Murphy", Role: "J. Robert Oppenheimer"}},
			Directors: []core.Person{{TMDB: 525, Name: "Christopher Nolan"}},
		},
	}
	for _, item := range titles {
		item.IDs.TMDB = item.Key.TMDB()
		if err := c.UpsertTitle(ctx, item, true); err != nil {
			t.Fatal(err)
		}
	}
	// Insert a crew-only credit (composer, kind='crew') WITH a catalog_people row for one person
	// and WITHOUT any catalog_people row for Stellan Skarsgård (proving sparse LEFT JOIN safety).
	if _, err := db.ExecContext(ctx,
		`INSERT INTO catalog_people(person_tmdb, imdb) VALUES (935717, 'nm3234869') ON CONFLICT(person_tmdb) DO UPDATE SET imdb=excluded.imdb`,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO catalog_credits(key, person_tmdb, name, role, kind, ord, thumb) VALUES (?, 935717, 'Ludwig Göransson', 'Original Music Composer', 'crew', 0, '/ludwig.jpg')`,
		int64(core.MovieKey(103)),
	); err != nil {
		t.Fatal(err)
	}

	if store.DialectOf(db) == store.SQLite {
		for _, stmt := range []string{
			`DROP TABLE IF EXISTS catalog_person_names_fts`,
			`CREATE VIRTUAL TABLE catalog_person_names_fts USING fts5(name, content='catalog_person_names', content_rowid='person_tmdb', tokenize='unicode61 remove_diacritics 2', prefix='1 2 3', detail=none, columnsize=0)`,
			`INSERT INTO catalog_person_names_fts(catalog_person_names_fts) VALUES('rebuild')`,
			`INSERT INTO catalog_person_names_fts(catalog_person_names_fts) VALUES('integrity-check')`,
		} {
			if _, err := db.ExecContext(ctx, stmt); err != nil {
				t.Fatalf("migration 60 rebuild failed: %v", err)
			}
		}
	}

	// 1. Accented cast member searched without accents returns both movie and show (even with no catalog_people row).
	got, err := c.PeopleTitles(ctx, nil, "stellan skarsgard", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Title != "Dune" || got[1].Title != "Andor" {
		t.Fatalf("stellan skarsgard titles = %+v, want [Dune, Andor]", got)
	}
	if got[0].SearchScore < 0.946 || got[0].SearchScore >= 0.96 {
		t.Fatalf("exact person score = %f, want in [0.946, 0.96)", got[0].SearchScore)
	}

	// 2. Director, writer, and crew (composer with diacritics) all return their credited titles.
	for _, tc := range []struct {
		query string
		want  string
	}{
		{"Denis Villeneuve", "Dune"},
		{"Tony Gilroy", "Andor"},
		{"ludwig goransson", "Oppenheimer"},
		{"Ludwig Göransson", "Oppenheimer"},
	} {
		res, err := c.PeopleTitles(ctx, nil, tc.query, 10)
		if err != nil {
			t.Fatalf("%s: %v", tc.query, err)
		}
		if len(res) != 1 || res[0].Title != tc.want {
			t.Fatalf("%s: got %+v, want [%s]", tc.query, res, tc.want)
		}
	}
}

func TestPeopleSearchLeadRoleOrderAndExactTiering(t *testing.T) {
	ctx := context.Background()
	db := store.OpenTest(t)
	now := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	c, err := New(db, nil, config.Catalog{}, nil, WithNow(func() time.Time { return now }))
	if err != nil {
		t.Fatal(err)
	}

	// Seed Brad Pitt across:
	// 1. Starring role (ord=0, Fight Club)
	// 2. Directed feature (director, Directed Gem)
	// 3. Talk-show "Self" appearance (ord=0, role="Self - Guest", huge popularity)
	// 4. Deep uncredited cameo (ord=25, role="Vanisher (uncredited)", huge popularity)
	seed := []struct {
		id         int64
		title      string
		pop        float64
		votes      int
		cast       []core.Person
		directors  []core.Person
		customOrd  int
		customRole string
	}{
		{1, "Fight Club", 95, 25000, []core.Person{{TMDB: 287, Name: "Brad Pitt", Role: "Tyler Durden"}}, nil, -1, ""},
		{2, "Directed Gem", 80, 9000, nil, []core.Person{{TMDB: 287, Name: "Brad Pitt"}}, -1, ""},
		{3, "Late Night Talk Show", 250, 30000, []core.Person{{TMDB: 287, Name: "Brad Pitt", Role: "Self - Guest"}}, nil, -1, ""},
		{4, "Blockbuster Cameo", 300, 40000, nil, nil, 25, "Vanisher (uncredited)"},
		{5, "Iron Man", 110, 22000, []core.Person{{TMDB: 3223, Name: "Robert Downey Jr.", Role: "Tony Stark"}}, nil, -1, ""},
		{6, "Pulp Fiction", 130, 26000, []core.Person{{TMDB: 2231, Name: "Samuel L. Jackson", Role: "Jules Winnfield"}}, []core.Person{{TMDB: 138, Name: "Quentin Tarantino"}}, -1, ""},
	}
	for _, s := range seed {
		m := core.Title{
			Key: core.MovieKey(s.id), IDs: core.IDs{TMDB: s.id}, Type: core.TypeMovie,
			Title: s.title, Year: 2020, Released: now.AddDate(-2, 0, 0),
			Popularity: s.pop, VoteCount: s.votes, Cast: s.cast, Directors: s.directors,
		}
		if err := c.UpsertTitle(ctx, m, true); err != nil {
			t.Fatal(err)
		}
		if s.customOrd >= 0 {
			if _, err := db.ExecContext(ctx,
				`INSERT INTO catalog_credits(key, person_tmdb, name, role, kind, ord, thumb) VALUES (?, 287, 'Brad Pitt', ?, 'cast', ?, '')`,
				int64(m.Key), s.customRole, s.customOrd,
			); err != nil {
				t.Fatal(err)
			}
		}
	}

	got, err := c.PeopleTitles(ctx, nil, "Brad Pitt", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 {
		t.Fatalf("len(got) = %d, want 4: %+v", len(got), got)
	}
	// Starring role and directed feature must rank ahead of "Self" and deep uncredited cameo,
	// even though the talk show and cameo have higher raw popularity.
	names := []string{got[0].Title, got[1].Title, got[2].Title, got[3].Title}
	if (names[0] != "Fight Club" && names[0] != "Directed Gem") || (names[1] != "Fight Club" && names[1] != "Directed Gem") {
		t.Fatalf("lead roles did not rank ahead of cameos/Self: %v", names)
	}

	// Near-exact matches: dropped suffix ("robert downey"), dropped middle initial ("samuel jackson"),
	// 1-edit typo ("quentin tarentino"), and last-word prefix ("brad pit") all score in [0.935, 0.946).
	for _, tc := range []struct {
		query string
		want  string
	}{
		{"robert downey", "Iron Man"},
		{"samuel jackson", "Pulp Fiction"},
		{"quentin tarentino", "Pulp Fiction"},
		{"brad pit", "Fight Club"},
	} {
		res, err := c.PeopleTitles(ctx, nil, tc.query, 10)
		if err != nil {
			t.Fatalf("%s: %v", tc.query, err)
		}
		if len(res) == 0 || res[0].Title != tc.want {
			t.Fatalf("%s: got %+v, want leading %s", tc.query, res, tc.want)
		}
		if res[0].SearchScore < 0.935 || res[0].SearchScore >= 0.946 {
			t.Fatalf("%s: near-exact score = %f, want in [0.935, 0.946)", tc.query, res[0].SearchScore)
		}
	}

	// Partial surname/prefix matches ("pitt", "tarant") score < 0.50 so title matches never lose to them,
	// and WithExactPeopleOnly skips them completely.
	partial, err := c.PeopleTitles(ctx, nil, "pitt", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(partial) == 0 || partial[0].SearchScore >= 0.50 {
		t.Fatalf("partial surname match score = %+v, want < 0.50", partial)
	}
	skipped, err := c.PeopleTitles(core.WithExactPeopleOnly(ctx), nil, "pitt", 10)
	if err != nil || len(skipped) != 0 {
		t.Fatalf("WithExactPeopleOnly returned %+v (err=%v), want empty", skipped, err)
	}
}

func TestPeopleSearchVisibilityAndParentalSafety(t *testing.T) {
	ctx := context.Background()
	db := store.OpenTest(t)
	now := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	c, err := New(db, nil, config.Catalog{}, nil,
		WithNow(func() time.Time { return now }),
		WithLibraryPolicies(LibraryPolicies{
			MovieRules: []core.LibraryExclusion{{
				Name: "hide horror", MediaType: "movie", Match: "all", Scope: core.VisibilityEverywhere,
				Rules: []core.CollectionRule{{Field: "genre", Op: "is", Value: json.RawMessage(`"Horror"`)}},
			}},
		}),
	)
	if err != nil {
		t.Fatal(err)
	}

	actor := []core.Person{{TMDB: 500, Name: "Alex Mercer", Role: "Lead"}}
	items := []core.Title{
		{Key: core.MovieKey(1), IDs: core.IDs{TMDB: 1}, Type: core.TypeMovie, Title: "Family Adventure", Genres: []string{"Adventure"}, ContentRating: "PG", Released: now.AddDate(-1, 0, 0), Cast: actor},
		{Key: core.MovieKey(3), IDs: core.IDs{TMDB: 3}, Type: core.TypeMovie, Title: "Genre Hidden", Genres: []string{"Horror"}, ContentRating: "R", Released: now.AddDate(-1, 0, 0), Cast: actor},
		{Key: core.MovieKey(4), IDs: core.IDs{TMDB: 4}, Type: core.TypeMovie, Title: "Adult Entry", Genres: []string{"Drama"}, Adult: true, ContentRating: "PG", Released: now.AddDate(-1, 0, 0), Cast: actor},
		{Key: core.MovieKey(5), IDs: core.IDs{TMDB: 5}, Type: core.TypeMovie, Title: "Future Unreleased", Genres: []string{"Drama"}, ContentRating: "PG", Released: now.AddDate(1, 0, 0), Cast: actor},
	}
	for _, it := range items {
		if err := c.UpsertTitle(ctx, it, true); err != nil {
			t.Fatal(err)
		}
	}

	// Library visibility rule ("everywhere"), adult guard, and release-date guard filter out 3, 4, and 5.
	got, err := c.PeopleTitles(ctx, &core.User{ID: 2}, "Alex Mercer", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Title != "Family Adventure" {
		t.Fatalf("got %+v, want [Family Adventure]", got)
	}
}

func TestPeopleSearchQueryPlanUsesIndexesWithoutTempBTree(t *testing.T) {
	ctx := context.Background()
	db := store.OpenTest(t)
	if store.DialectOf(db) != store.SQLite {
		return
	}
	now := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	c, err := New(db, nil, config.Catalog{}, nil, WithNow(func() time.Time { return now }))
	if err != nil {
		t.Fatal(err)
	}
	m := core.Title{
		Key: core.MovieKey(1), IDs: core.IDs{TMDB: 1}, Type: core.TypeMovie,
		Title: "Dune", Released: now.AddDate(-1, 0, 0),
		Cast: []core.Person{{TMDB: 1640, Name: "Stellan Skarsgård", Role: "Baron"}},
	}
	if err := c.UpsertTitle(ctx, m, true); err != nil {
		t.Fatal(err)
	}

	cols := qualifiedTitleCols("t")
	queries := []struct {
		name string
		sql  string
		args []any
	}{
		{
			name: "exact_person_btree",
			sql:  `SELECT person_tmdb, name, credits FROM catalog_person_names WHERE LOWER(name)=? AND credits>0 LIMIT ?`,
			args: []any{"stellan skarsgard", 16},
		},
		{
			name: "exact_person_fts",
			sql:  `SELECT p.person_tmdb, p.name, p.credits FROM catalog_person_names_fts f JOIN catalog_person_names p ON p.person_tmdb=f.rowid WHERE catalog_person_names_fts MATCH ? AND p.credits>0 LIMIT ?`,
			args: []any{`"stellan" "skarsgard"`, 64},
		},
		{
			name: "prefix_person_fts",
			sql:  `SELECT p.person_tmdb, p.name, p.credits FROM catalog_person_names_fts f JOIN catalog_person_names p ON p.person_tmdb=f.rowid WHERE catalog_person_names_fts MATCH ? AND p.credits>0 LIMIT ?`,
			args: []any{`"stellan"* "skarsgard"*`, 64},
		},
		{
			name: "person_credits_union",
			sql: `SELECT * FROM (SELECT ` + cols + `, cr.person_tmdb, cr.kind, cr.ord, cr.role FROM catalog_credits cr JOIN catalog_titles t ON t.key=cr.key WHERE cr.person_tmdb=? AND t.adult=0 AND t.type IN ('movie','show') LIMIT ?)
UNION ALL
SELECT * FROM (SELECT ` + cols + `, cr.person_tmdb, cr.kind, cr.ord, cr.role FROM catalog_credits cr JOIN catalog_titles t ON t.key=cr.key WHERE cr.person_tmdb=? AND t.adult=0 AND t.type IN ('movie','show') LIMIT ?)`,
			args: []any{1640, 360, 525, 120},
		},
	}

	for _, q := range queries {
		rows, err := db.QueryContext(ctx, "EXPLAIN QUERY PLAN "+q.sql, q.args...)
		if err != nil {
			t.Fatalf("%s explain: %v", q.name, err)
		}
		var details []string
		for rows.Next() {
			var id, parent, notused int
			var detail string
			if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			details = append(details, detail)
		}
		rows.Close()
		plan := strings.Join(details, " | ")
		t.Logf("%s: %s", q.name, plan)
		if strings.Contains(plan, "USE TEMP B-TREE") {
			t.Fatalf("%s used temp B-tree: %s", q.name, plan)
		}
		for _, forbidden := range []string{"SCAN catalog_titles", "SCAN t", "SCAN catalog_credits", "SCAN cr", "SCAN catalog_person_names", "SCAN p"} {
			if strings.Contains(plan, forbidden) {
				t.Fatalf("%s performed full scan (%s): %s", q.name, forbidden, plan)
			}
		}
	}
}
