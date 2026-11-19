package releasedb

import (
	"context"
	"errors"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

func TestReferencePruningBatchesAndFailsClosed(t *testing.T) {
	ctx := context.Background()
	st := NewStore(store.OpenTest(t))
	lib := Library{Store: st, Seal: func(s string) (string, error) { return s, nil }, Open: func(s string) (string, error) { return s, nil }, Signature: "test"}
	for i := int64(1); i <= 300; i++ {
		if err := lib.Save(ctx, core.MovieKey(i), []core.Release{{ID: "r", Name: "Film", Protocol: core.ProtocolHTTP, StreamURL: "https://source.test/file"}}); err != nil {
			t.Fatal(err)
		}
	}
	lib.Accept = func(context.Context, core.RatingKey, core.Release) (bool, error) {
		return false, errors.New("selection unavailable")
	}
	if _, err := lib.PruneSelection(ctx); err == nil {
		t.Fatal("selection failure ignored")
	}
	var count int
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM pse_release_library`).Scan(&count); err != nil || count != 300 {
		t.Fatal("deleted without selection", count, err)
	}
	lib.Accept = func(_ context.Context, key core.RatingKey, _ core.Release) (bool, error) {
		return key.TMDB()%2 == 0, nil
	}
	n, err := lib.PruneSelection(ctx)
	if err != nil || n != 150 {
		t.Fatal(n, err)
	}
	if n, err = lib.PruneSelection(ctx); err != nil || n != 0 {
		t.Fatal("not idempotent", n, err)
	}
}
