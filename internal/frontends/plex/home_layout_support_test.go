package plex

import (
	"context"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"

	"github.com/braedonsaunders/ether/internal/frontends/plex/model"
	"github.com/braedonsaunders/ether/internal/personalize"
)

// finalizeHomeRows is the wire-level entry used by layout tests. Production
// home and section handlers pass facts captured while painting.
func (s *Server) finalizeHomeRows(ctx context.Context, pr *Request, hc core.HubContext, section string, rows []model.Hub) []model.Hub {
	hubs := make([]core.Hub, len(rows))
	neutral := make([]core.NeutralHomeRow, len(rows))
	for i, row := range rows {
		hubs[i], neutral[i] = homeLayoutFacts(row)
	}
	return s.arrangeHomeRows(ctx, pr, hc, section, rows, hubs, neutral)
}

// saveCuratedPage saves a Curated Recommendations page holding rows, each
// shown in list order.
func saveCuratedPage(t testing.TB, e *personalize.Engine, key core.PageKey, length int, rows ...core.Row) {
	t.Helper()
	cur, _ := e.Page(context.Background(), key)
	for i := range rows {
		rows[i].Placement.Mode = core.PlacementShow
	}
	page := core.RowPage{Key: key, Mode: core.PageCurated, Length: core.PageLength{Rows: length}, Rows: rows, Revision: cur.Revision}
	if _, err := e.SavePage(context.Background(), page); err != nil {
		t.Fatal(err)
	}
}

// presentLayout is a page engine stand-in: it keeps the listed rows in its
// order and hands each the presentation the page asks for.
type presentLayout struct {
	*fakeRecommender
	order   []string
	present map[string]core.RowPresentation
}

func (p presentLayout) FinalizeNeutralHome(_ context.Context, _ *core.User, _ core.HomeSituation, _ string, rows []core.NeutralHomeRow) ([]core.NeutralHomeRow, error) {
	byID := map[string]core.NeutralHomeRow{}
	for _, r := range rows {
		byID[r.ID] = r
	}
	var out []core.NeutralHomeRow
	for _, id := range p.order {
		if r, ok := byID[id]; ok {
			r.Present = p.present[id]
			out = append(out, r)
		} else if pr := p.present[id]; pr.Surface != nil {
			out = append(out, core.NeutralHomeRow{ID: id, Present: pr})
		}
	}
	return out, nil
}
