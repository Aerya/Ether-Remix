package personalize

import (
	"context"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
)

// Test helpers for Recommendations pages.

// prow is a page row of source kind with a placement ("pinned", "show",
// "auto", "hide"); pos is a pinned row's position.
func prow(kind, mode string, pos int) core.Row {
	src := core.RowSource{Kind: kind}
	if id, ok := cutCollection(kind); ok {
		src = core.RowSource{Kind: SourceCollection, Params: map[string]string{"collection": id}}
	}
	r := core.Row{ID: NewRowID(), Source: src, Placement: core.RowPlacement{Mode: mode, Audience: core.Audience{Mode: core.AudienceEveryone}}}
	if mode == core.PlacementPinned {
		r.Placement.Position = pos
	}
	return r
}

func cutCollection(kind string) (string, bool) {
	for _, p := range []string{KindCollectionPrefix, "collection."} {
		if len(kind) > len(p) && kind[:len(p)] == p {
			return kind[len(p):], true
		}
	}
	return "", false
}

// rpage is a Personalized page with Ether's picks on.
func rpage(key core.PageKey, rows ...core.Row) core.RowPage {
	if rows == nil {
		rows = []core.Row{}
	}
	return core.RowPage{Key: key, Mode: core.PagePersonalized, Picks: true, Rows: rows}
}

// libPage is a catalogue library's Recommendations page key.
func libPage(id core.LibraryID) core.PageKey { return core.LibraryPage(id, "recommended") }

// mustSavePage saves a page over whatever is saved.
func mustSavePage(t *testing.T, e *Engine, p core.RowPage) core.RowPage {
	t.Helper()
	cur, saved := e.Page(context.Background(), p.Key)
	if saved {
		p.Revision = cur.Revision
	}
	out, err := e.SavePage(context.Background(), p)
	if err != nil {
		t.Fatalf("SavePage(%s): %v", p.Key, err)
	}
	return out
}

// viewerOverride sets "changes just for viewer" on a page's row.
func viewerOverride(p *core.RowPage, viewer int64, rowID string, mode string, pos int) {
	pl := core.RowPlacement{Mode: mode, Position: pos, Audience: core.Audience{Mode: core.AudienceEveryone}}
	if mode != core.PlacementPinned {
		pl.Position = 0
	}
	for i := range p.Variants {
		if p.Variants[i].Viewer == viewer {
			p.Variants[i].Rows = append(p.Variants[i].Rows, core.RowOverride{Row: rowID, Placement: &pl})
			return
		}
	}
	p.Variants = append(p.Variants, core.PageVariant{Viewer: viewer, Rows: []core.RowOverride{{Row: rowID, Placement: &pl}}})
}

// previewDraft previews a section under an unsaved page.
func previewDraft(e *Engine, ctx context.Context, user *core.User, hc core.HubContext, section string, limit int, p core.RowPage) (*core.HomePreview, error) {
	return e.PreviewHome(withDraftPage(ctx, p), user, hc, section, limit)
}
