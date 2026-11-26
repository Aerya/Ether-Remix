package personalize

import (
	"github.com/braedonsaunders/ether/internal/core"
	"testing"
)

func TestRemixImportedRowsRespectLaterOwnerRemoval(t *testing.T) {
	h := newHarness(t, nil)
	sp := core.CollectionSpec{ID: "42", Name: "Selection", MediaType: "movie", Enabled: true}
	if err := h.e.PublishRemixRows(t.Context(), sp); err != nil {
		t.Fatal(err)
	}
	page, ok := h.e.Page(t.Context(), core.PageHome)
	if !ok || len(page.Offered) != 1 {
		t.Fatalf("import not placed: %+v", page)
	}
	page.Rows = nil
	if _, err := h.e.SavePage(t.Context(), page); err != nil {
		t.Fatal(err)
	}
	if err := h.e.PublishRemixRows(t.Context(), sp); err != nil {
		t.Fatal(err)
	}
	page, _ = h.e.Page(t.Context(), core.PageHome)
	if len(page.Rows) != 0 {
		t.Fatal("import restored a row removed by the owner")
	}
}
