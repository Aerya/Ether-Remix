package collections

import (
	"context"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/extend"
	"github.com/braedonsaunders/ether/internal/pse/mediaaccess"
)

type localFilesKind struct{}

func (localFilesKind) ID() string { return "localFiles" }
func (localFilesKind) Describe() KindDescribe {
	return KindDescribe{Label: extend.T("Local files"), Hint: extend.T("Files in an indexed local folder"), Defaults: `{}`, MediaTypes: []string{"movie", "show", "mixed"}}
}
func (localFilesKind) Validate(ve *ValidationError, path string, src core.CollectionSource) {
	if src.Label == "" {
		ve.add(path, "local folder required")
	}
}
func (localFilesKind) Gather(g *Gather) {
	policy, err := mediaaccess.Read(g.Ctx, g.Svc.db, "local:"+g.Src.Label)
	if err != nil {
		g.Fail(err)
		return
	}
	if viewer := core.CatalogViewerID(g.Ctx); viewer > 0 && !mediaaccess.Allowed(g.Ctx, g.Svc.db, "local:"+g.Src.Label, viewer) {
		return
	}
	// The background builder reads the configured folder as its owner. Serving
	// this collection still checks its current sharing policy for every viewer.
	ctx := core.WithCatalogViewer(g.Ctx, &core.User{ID: policy.OwnerID})

	rows, err := g.Svc.db.QueryContext(g.Ctx, "SELECT DISTINCT title_key FROM pse_local_files WHERE root_id=? AND kind='video'", g.Src.Label)
	if err != nil {
		g.Fail(err)
		return
	}
	keys := []core.RatingKey{}
	for rows.Next() {
		var k core.RatingKey
		if err = rows.Scan(&k); err != nil {
			rows.Close()
			g.Fail(err)
			return
		}
		if k.Kind() == core.KindEpisode {
			k = core.ShowKey(k.TMDB())
		}
		keys = append(keys, k)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		g.Fail(err)
		return
	}
	stored, ok := g.Svc.cat.(interface {
		StoredTitle(context.Context, core.RatingKey) (*core.Title, error)
	})
	for _, k := range keys {
		g.Res.keys = append(g.Res.keys, k)
		if ok {
			if t, e := stored.StoredTitle(ctx, k); e == nil {
				g.Res.titles = append(g.Res.titles, *t)
			}
		}
	}
}
