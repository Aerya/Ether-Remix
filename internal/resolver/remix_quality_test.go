package resolver

import (
	"context"
	"errors"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/pse/quality"
	"testing"
)

func TestRemixQualityBlocksPinnedAndCachedCandidates(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	q, err := quality.New(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()
	s := New(&fakeIndexer{releases: movieReleases()}, fakeHealth{}, db, testCfg(), Options{})
	defer s.Close()
	req := core.ResolveRequest{Title: matrixTitle(), Client: appleTVCaps()}
	if _, err = s.Resolve(ctx, req); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Pin(ctx, matrixTitle().Key, "", withParsed(movieReleases()[2]), ""); err != nil {
		t.Fatal(err)
	}
	if err = q.Save(ctx, quality.Config{Profiles: []quality.Profile{{ID: "p", Name: "deny", Scope: "global", Rules: []core.ReleaseRule{{ID: "r", Name: "all", Action: "avoid", Match: "any", Conditions: []core.ReleaseCondition{{Field: "name", Operator: "regex", Value: ".*"}}}}}}}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Resolve(ctx, req); !errors.Is(err, core.ErrNotAvailable) {
		t.Fatalf("pinned/cache bypassed strict gate: %v", err)
	}
	if err = q.Save(ctx, quality.Config{}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Resolve(ctx, req); err != nil {
		t.Fatalf("removing rules did not restore playback: %v", err)
	}
}
