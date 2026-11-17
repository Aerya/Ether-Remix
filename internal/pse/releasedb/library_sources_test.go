package releasedb

import (
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
	"reflect"
	"testing"
	"time"
)

func TestLibraryKeysOnlyFreshEnabledSource(t *testing.T) {
	db := store.OpenTest(t)
	t.Cleanup(func() { activeSources.Store(nil) })
	add := func(id, source string, key core.RatingKey, at time.Time) {
		t.Helper()
		_, e := db.Exec(`INSERT INTO pse_releases(id,title_key,media_type,source_id,source_name,name,first_seen,last_seen) VALUES(?,?,'movie',?,'Source','Release',?,?)`, id, key, source, at.Unix(), at.Unix())
		if e != nil {
			t.Fatal(e)
		}
	}
	now := time.Now()
	add("fresh", "a", core.MovieKey(1), now)
	add("stale", "a", core.MovieKey(2), now.Add(-MaxAge-time.Hour))
	add("other", "b", core.MovieKey(3), now)
	add("episode", "a", core.EpisodeKey(4, 1, 1), now)
	add("episode2", "a", core.EpisodeKey(4, 1, 2), now)
	got, e := LibraryKeys(t.Context(), db, "a")
	if e != nil || !reflect.DeepEqual(got, []core.RatingKey{core.MovieKey(1), core.ShowKey(4)}) {
		t.Fatalf("%v %v", got, e)
	}
	active := map[string]bool{"b": true}
	activeSources.Store(&active)
	got, e = LibraryKeys(t.Context(), db, "a")
	if e != nil || len(got) != 0 {
		t.Fatalf("disabled: %v %v", got, e)
	}
}
