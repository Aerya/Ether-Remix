package plex

import (
	"context"
	"database/sql"
	watchstate "github.com/braedonsaunders/ether/internal/state"
	"github.com/braedonsaunders/ether/internal/store"
	"testing"
	"time"
)

func TestContinueWatchingRetainsImportedOldSeriesOnHome(t *testing.T) {
	ctx := context.Background()
	db, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	f := newFixture()
	defer f.srv.Close()
	st := watchstate.New(db, watchstate.Options{Now: func() time.Time { return fixedNow }})
	defer st.Close(ctx)
	f.srv.setState(st)
	at := fixedNow.Add(-163 * 24 * time.Hour)
	err = st.ImportTransaction(ctx, func(*sql.Tx) (watchstate.ImportBatch, error) {
		return watchstate.ImportBatch{Records: []watchstate.ImportRecord{{Views: 1, UserID: ownerUser.ID, Key: bbE1.Key, ViewedAt: at}}}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/hubs/continueWatching/items", "/hubs/home/continueWatching", "/library/onDeck"} {
		mc := decodeJSON(t, f.get(t, path, jsonAccept...))
		rows, _ := mc["Metadata"].([]any)
		if len(rows) != 1 || rows[0].(map[string]any)["ratingKey"] != bbE2.Key.String() {
			t.Fatalf("%s: %v", path, mc)
		}
		if off, ok := rows[0].(map[string]any)["viewOffset"]; ok && off != float64(0) {
			t.Fatal("invented resume offset", off)
		}
	}
	// A movie must not displace the next episode from Home Continue Watching.
	if err := st.Progress(ctx, ownerUser.ID, fightClub.Key, time.Minute, time.Hour, "playing"); err != nil {
		t.Fatal(err)
	}
	mc := decodeJSON(t, f.get(t, "/hubs?identifier=home.continue,home.ondeck", jsonAccept...))
	found := false
	for _, row := range mc["Hub"].([]any) {
		h := row.(map[string]any)
		if h["hubIdentifier"] == "home.continue" {
			found = true
			count := 0
			for _, item := range h["Metadata"].([]any) {
				if item.(map[string]any)["ratingKey"] == bbE2.Key.String() {
					count++
				}
			}
			if count != 1 {
				t.Fatalf("next episode count %d: %v", count, h)
			}
		}
	}
	if !found {
		t.Fatalf("missing Home Continue Watching: %v", mc)
	}
}
