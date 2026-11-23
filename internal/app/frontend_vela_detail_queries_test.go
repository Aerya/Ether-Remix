package app

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	artworkservice "github.com/braedonsaunders/ether/internal/artwork/service"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/frontendtest"
	"github.com/braedonsaunders/ether/internal/frontend/kit/ids"
	"github.com/braedonsaunders/ether/internal/frontends/vela"
	"github.com/braedonsaunders/ether/internal/identity"
	"github.com/braedonsaunders/ether/internal/plextv"
	"github.com/braedonsaunders/ether/internal/resolver"
	"github.com/braedonsaunders/ether/internal/tmdb"
)

type detailQueryIndexer struct{ releases []core.Release }

func (detailQueryIndexer) Name() string { return "offline" }
func (f detailQueryIndexer) Search(context.Context, core.SearchRequest) ([]core.Release, error) {
	return f.releases, nil
}
func (detailQueryIndexer) FetchNZB(context.Context, core.Release) ([]byte, error) {
	return nil, core.ErrNotAvailable
}

func TestVelaDetailBoundedQueries(t *testing.T) {
	a := setupTestApp(t, true)
	ctx := t.Context()
	if err := a.users.SetOwner(ctx, plextv.Account{ID: 99, Username: "owner"}, "owner-token"); err != nil {
		t.Fatal(err)
	}
	viewer, err := a.users.Owner(ctx)
	if err != nil {
		t.Fatal(err)
	}
	title := core.Title{Key: core.MovieKey(44), Type: core.TypeMovie, IDs: core.IDs{TMDB: 44}, Title: "Inception", Year: 2010, Released: time.Date(2010, 1, 1, 0, 0, 0, 0, time.UTC)}
	for n := 1; n <= 27; n++ {
		title.Cast = append(title.Cast, core.Person{TMDB: int64(n), Name: fmt.Sprint("Actor ", n), ThumbPath: fmt.Sprintf("/person%d.jpg", n)})
	}
	title.Crew = []core.Person{{TMDB: 100, Name: "Director", Role: "Director", ThumbPath: "/director.jpg"}}
	if err := a.catalog.UpsertTitle(ctx, title, true); err != nil {
		t.Fatal(err)
	}
	if _, err := a.db.ExecContext(ctx, `UPDATE catalog_titles SET detail=1,detail_at=? WHERE key=?`, time.Now().Unix(), int64(title.Key)); err != nil {
		t.Fatal(err)
	}
	source := *a.artwork
	source.Metadata = nil
	source.Variants = func(*core.Title, string) (*tmdb.Images, bool) { return &tmdb.Images{}, true }
	source.Images = artworkservice.NewRemoteCache(1024, &http.Client{Transport: disabledArtworkTransport{}})
	originalResolver := a.resolver
	defer func() { a.resolver = originalResolver }()
	for _, count := range []int{1, 442} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			releases := make([]core.Release, count)
			for n := range releases {
				releases[n] = core.Release{ID: fmt.Sprint("release-", n), Name: fmt.Sprintf("Inception.2010.1080p.WEB-DL.H264-G%d", n), Size: 8 << 30}
			}
			// A learned verdict, without HC in the name, must filter the
			// completed snapshot even though the GET never waits for ranking.
			releases = append(releases, core.Release{ID: "blocked", Name: "Inception.2010.1080p.WEB-DL.H264-BLOCKED", Size: 8 << 30})
			cfg := a.cfg.Read()
			cfg.Resolver.HardSubtitles = "exclude"
			a.resolver = resolver.New(detailQueryIndexer{releases}, nil, a.db, cfg, resolver.Options{})
			defer a.resolver.Close()
			if err := a.resolver.RememberSubtitleVerdict(ctx, "blocked", true); err != nil {
				t.Fatal(err)
			}
			if done := a.resolver.RefillSearchCache(&title); done != nil {
				select {
				case <-done:
				case <-time.After(5 * time.Second):
					t.Fatal("offline cache fill timed out")
				}
			}
			exp, err := a.newSurfaceExperience("vela", source)
			if err != nil {
				t.Fatal(err)
			}
			grants, err := identity.NewGrants(a.db, a.users, time.Hour, nil)
			if err != nil {
				t.Fatal(err)
			}
			device := core.Device{ID: "tv"}
			credential, err := grants.Issue(ctx, viewer.ID, nil, "vela", device)
			if err != nil {
				t.Fatal(err)
			}
			images, err := identity.NewArtworkImageGrantService(a.db, "vela", grants, func() frontend.Experience { return exp }, source, time.Minute, nil)
			if err != nil {
				t.Fatal(err)
			}
			inst, err := (vela.Frontend{ReplayKey: make([]byte, 32)}).Mount(ctx, frontend.Host{Experience: exp, Identity: frontendtest.Identity{ResolveDeviceFunc: func(ctx context.Context, token string, _ core.Device) (*core.User, time.Time, error) {
				v, e := grants.Resolve(ctx, "vela", token)
				return v, time.Time{}, e
			}}, ImageGrants: images, Sessions: frontendtest.Sessions{}})
			if err != nil {
				t.Fatal(err)
			}
			defer inst.Close(context.Background())
			req := httptest.NewRequest("GET", "/vela/v0/titles/"+ids.RatingKeyGUID(title.Key), nil).WithContext(ctx)
			req.Header.Set("Authorization", "Bearer "+credential)
			req.Header.Set("X-Vela-Device-Id", "tv")
			w := httptest.NewRecorder()
			inst.Handler().ServeHTTP(w, req)
			if w.Code != 200 {
				t.Fatalf("detail: %d %s", w.Code, w.Body.String())
			}
			// Observe background completion outside the request. The completed
			// snapshot must contain a checked source, never the blocked source.
			local := core.WithLocalResponse(ctx)
			reqVariants := core.ResolveRequest{Title: &title, User: viewer, Client: core.ClientCaps{Product: "Vela", ClientID: "tv"}}
			if slots, err := a.resolver.Variants(local, reqVariants, 4); err != nil || len(slots) == 0 {
				t.Fatalf("immediate slots: %v %v", slots, err)
			}
			deadline := time.Now().Add(5 * time.Second)
			for {
				completed, err := a.resolver.Variants(local, reqVariants, 4)
				if err != nil || len(completed) == 0 {
					t.Fatalf("completed snapshot: %v %v", completed, err)
				}
				checked := false
				for _, version := range completed {
					if version.Release.ID == "blocked" {
						t.Fatal("completed snapshot advertised a policy-blocked source")
					}
					checked = checked || version.Release.ID != version.ID
				}
				if checked {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("completed snapshot never supplied a checked source")
				}
				time.Sleep(10 * time.Millisecond)
			}
		})
	}
}
