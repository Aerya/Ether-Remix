package releasedb

import (
	"context"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

// Avec PSE_PLAYABLE_SINGLE, les titres à 1 release (moins que le minimum)
// sont affichés et signalés ; ceux qui atteignent le minimum ne le sont pas.
func TestRiskyTitles(t *testing.T) {
	ctx := context.Background()
	s := New(Deps{DB: store.OpenTest(t)})
	one, two, none, exemptOne := core.MovieKey(1), core.MovieKey(2), core.MovieKey(3), core.ShowKey(4)
	for _, r := range []struct {
		key           core.RatingKey
		releases, any int
	}{{one, 1, 1}, {two, 2, 2}, {none, 0, 3}, {exemptOne, 0, 1}} {
		if _, err := s.st.db.ExecContext(ctx, `INSERT INTO pse_playable(title_key,releases,any_lang) VALUES(?,?,?)`, int64(r.key), r.releases, r.any); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.st.SetLangExempt(ctx, []core.RatingKey{exemptOne}); err != nil {
		t.Fatal(err)
	}

	p := NewPlayable(2, "", false)
	if got, _ := s.st.riskKeys(ctx, p); len(got) != 0 {
		t.Fatalf("option désactivée : %v", got)
	}
	p.Single, p.Mark = true, "both"
	if p.ShowMin() != 1 {
		t.Fatalf("seuil d'affichage = %d", p.ShowMin())
	}
	got, err := s.st.riskKeys(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[one] != 1 || got[exemptOne] != 1 {
		t.Fatalf("titres risqués = %v", got)
	}

	// Le catalogue affiche désormais les titres à 1 release.
	clause, args := p.PlayableSQL("t.title_key")
	var n int
	if err := s.st.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pse_playable t WHERE `+clause, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Fatalf("titres affichés = %d, 3 attendus", n)
	}

	SetRisk(got, p)
	t.Cleanup(func() { riskCur.Store(nil) })
	if m := MarkSummary(one, "Un film."); m != "⚠ 1 source : lecture incertaine\n\nUn film." {
		t.Fatalf("résumé = %q", m)
	}
	if m := MarkSummary(two, "Sûr."); m != "Sûr." {
		t.Fatalf("titre sûr marqué : %q", m)
	}
	if b := PosterRisk(one); b != "1 SOURCE" {
		t.Fatalf("badge = %q", b)
	}
	if b := PosterRisk(two); b != "" {
		t.Fatalf("badge sur un titre sûr : %q", b)
	}

	p.Mark = "summary"
	SetRisk(got, p)
	if PosterRisk(one) != "" || MarkSummary(one, "") == "" {
		t.Fatal("mode résumé seul non respecté")
	}
	p.Mark = "poster"
	SetRisk(got, p)
	if PosterRisk(one) == "" || MarkSummary(one, "x") != "x" {
		t.Fatal("mode badge seul non respecté")
	}
}
