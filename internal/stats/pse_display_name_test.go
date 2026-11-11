package stats

import (
	"context"
	"testing"

	"github.com/braedonsaunders/ether/internal/store"
)

func TestRenameViewerRelabelsOnlyTheirPlays(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	s := New(db, Options{})
	for _, row := range []struct {
		id   string
		user int
	}{{"a", 1}, {"b", 2}} {
		if _, err := db.ExecContext(ctx, `INSERT INTO stats_plays(id,user_id,rating_key,username,title,platform,device,decision,started,updated) VALUES(?,?,1,'old','t','p','d','direct',0,0)`, row.id, row.user); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.RenameViewer(ctx, 1, "Julien"); err != nil {
		t.Fatal(err)
	}
	for id, want := range map[string]string{"a": "Julien", "b": "old"} {
		var got string
		if err := db.QueryRowContext(ctx, `SELECT username FROM stats_plays WHERE id=?`, id).Scan(&got); err != nil || got != want {
			t.Fatalf("%s: %q (%v), want %q", id, got, err, want)
		}
	}
}
