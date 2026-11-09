package plex

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/plex/model"
)

// Bibliothèques à thème de PSE (internal/pse/themes) — voir
// docs/pse/FORK.fr.md. Chacune est une section Plex de films ou de séries
// (5101 à 5199) restreinte aux titres de ses collections : la navigation,
// les tris et les filtres d'Ether s'appliquent tels quels, l'onglet
// Recommandé montre une rangée par collection. Les titres gardent leur
// section d'origine (Films ou Séries) pour la lecture et les fiches.

// pseRowParam restreint /library/sections/{thème}/all à une rangée.
const pseRowParam = "pseRow"

func (s *Server) pseTheme(id int) (frontend.PSETheme, frontend.PSEThemes, bool) {
	if id <= frontend.PSEThemeSectionBase || id > frontend.PSEThemeSectionMax {
		return frontend.PSETheme{}, nil, false
	}
	t := frontend.CurrentPSEThemes()
	if t == nil {
		return frontend.PSETheme{}, nil, false
	}
	for _, th := range t.Themes() {
		if th.Section == id {
			return th, t, true
		}
	}
	return frontend.PSETheme{}, nil, false
}

func (s *Server) pseThemeSection(th frontend.PSETheme) Section {
	sum := sha1.Sum([]byte(fmt.Sprintf("%s/pse-theme/%d", s.identity().MachineIdentifier, th.Section)))
	h := hex.EncodeToString(sum[:16])
	return Section{
		ID: th.Section, Type: th.Type, Title: th.Title, Language: "fr-FR",
		UUID: h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32],
	}
}

// pseSectionByID complète sectionByID avec les bibliothèques à thème.
func (s *Server) pseSectionByID(id int) (Section, bool) {
	th, _, ok := s.pseTheme(id)
	if !ok {
		return Section{}, false
	}
	return s.pseThemeSection(th), true
}

// pseCanSeeTheme : une bibliothèque à thème est visible de qui voit la
// section d'Ether de même type.
func (s *Server) pseCanSeeTheme(u *core.User, id int) bool {
	th, _, ok := s.pseTheme(id)
	if !ok {
		return false
	}
	base, ok := s.untitledSectionFor(th.Type)
	return ok && s.canSeeSection(u, base.ID)
}

// pseWithThemes ajoute les bibliothèques à thème à la liste des sections
// d'un spectateur (/library/sections, /media/providers).
func (s *Server) pseWithThemes(u *core.User, secs []Section) []Section {
	t := frontend.CurrentPSEThemes()
	if t == nil {
		return secs
	}
	visible := map[core.MediaType]bool{}
	for _, sec := range secs {
		visible[sec.Type] = true
	}
	out := secs
	for _, th := range t.Themes() {
		if visible[th.Type] && len(th.Rows) > 0 {
			out = append(out, s.pseThemeSection(th))
		}
	}
	return out
}

// pseThemeFilter restreint une navigation dans une bibliothèque à thème à
// ses titres (ou à une rangée, avec ?pseRow=).
func (s *Server) pseThemeFilter(ctx context.Context, sec Section, u *core.User, q url.Values, f *core.Filter) {
	th, t, ok := s.pseTheme(sec.ID)
	if !ok {
		return
	}
	var keys []core.RatingKey
	if row := q.Get(pseRowParam); row != "" {
		for _, r := range th.Rows {
			if r.ID == row {
				keys = t.RowKeys(ctx, row, u)
			}
		}
	} else {
		keys = frontend.PSEThemeKeys(ctx, t, th, u)
	}
	if keys == nil {
		keys = []core.RatingKey{} // rien tant que les collections ne sont pas construites
	}
	f.IncludeKeys = keys
	f.PSEThemed = true // même filtre VF que Films et Séries
}

// pseThemeSort : les rangées montrent d'abord les sorties les plus récentes.
const pseThemeSort = "originallyAvailableAt:desc"

// pseThemeHubs remplace l'onglet Recommandé d'une bibliothèque à thème :
// une rangée par collection.
func (s *Server) pseThemeHubs(r *http.Request, sec Section) (homeBuild, bool) {
	th, _, ok := s.pseTheme(sec.ID)
	if !ok {
		return homeBuild{}, false
	}
	ctx := withStoredPolicy(r.Context())
	pr := FromContext(ctx)
	count := countParam(r, 12)
	mc := hubContainer()
	mc.LibrarySectionID = model.Ptr(sec.ID)
	mc.LibrarySectionTitle = sec.Title
	mc.LibrarySectionUUID = sec.UUID
	hc := hubCtx{section: sec.ID}
	page := *pr
	page.HasPage, page.Start, page.Size = true, 0, count
	id := strconv.Itoa(sec.ID)
	for _, row := range th.Rows {
		v := url.Values{pseRowParam: {row.ID}, "sort": {pseThemeSort}}
		br := s.parseBrowse(ctx, sec, sec.Type, pr.User, v)
		items, total, _, err := s.browse(ctx, &page, br)
		if err != nil || len(items) == 0 {
			continue
		}
		hs := hubSpec{
			Hub: core.Hub{ID: "pse.theme." + id + "." + row.ID, Title: row.Title, Type: sec.Type, Context: "hub." + string(sec.Type) + ".collection",
				Style: "shelf", Items: items, More: total > len(items)},
			key:        "/library/sections/" + id + "/all?" + url.Values{pseRowParam: {row.ID}, "sort": {pseThemeSort}}.Encode(),
			noPromoted: true,
		}
		mc.Hub = append(mc.Hub, s.renderHub(ctx, pr, hs, hc))
	}
	mc.Size = len(mc.Hub)
	return homeBuild{mc: mc, served: map[string]core.Hub{}, section: sec.ID}, true
}

// pseThemeCollections réduit l'onglet Collections d'une bibliothèque à
// thème à ses rangées, dans leur ordre.
func (s *Server) pseThemeCollections(sec Section, all []core.Collection) []core.Collection {
	th, _, ok := s.pseTheme(sec.ID)
	if !ok {
		return all
	}
	byKey := map[core.RatingKey]core.Collection{}
	for _, c := range all {
		base, _ := c.Key.CollectionSection()
		byKey[base] = c
	}
	out := []core.Collection{}
	for _, row := range th.Rows {
		if c, ok := byKey[row.Key]; ok && row.Key != 0 {
			out = append(out, c)
		}
	}
	return out
}
