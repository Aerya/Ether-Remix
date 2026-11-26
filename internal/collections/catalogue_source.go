package collections

import (
	"context"
	"fmt"

	"github.com/braedonsaunders/ether/internal/core"
)

// CatalogueSync is one catalogue-source sync: the matched titles, how many
// items the list read and the most it was allowed to read. Listed reaching
// Limit means the list may hold more than this sync took.
type CatalogueSync struct {
	Titles []core.Title
	Listed int
	Limit  int
}

// Capped reports whether the sync stopped at its limit.
func (s CatalogueSync) Capped() bool { return s.Limit > 0 && s.Listed >= s.Limit }

// GatherCatalogueSource reuses the installed list-source adapter, including its
// paging and external-ID matching. How many items it reads is the list's
// granted size (what the plugin declares, or what the owner set in the
// plugin's limits); nothing here caps it further. Only a background
// catalogue-source sync invokes it. kind is the list-source kind id, label
// names the list for the owner, and mediaType is "movie" or "show".
func (s *Service) GatherCatalogueSource(ctx context.Context, kind, label, mediaType string) (CatalogueSync, error) {
	k, ok := s.kindRegistry().Lookup(kind)
	if !ok {
		return CatalogueSync{}, fmt.Errorf("%s isn't loaded on this server yet; turn its plugin off and on again in Plugins, or restart Ether", label)
	}
	gatherKind := "movie"
	if mediaType == string(core.TypeShow) {
		gatherKind = "tv"
	}
	src := core.CollectionSource{Kind: kind}
	g := &Gather{Strict: true, Ctx: ctx, Svc: s, Spec: &core.CollectionSpec{}, Src: src, Kinds: []string{gatherKind}}
	k.Gather(g)
	if err := ctx.Err(); err != nil {
		return CatalogueSync{}, err
	}
	return CatalogueSync{Titles: g.Titles(), Listed: g.Listed, Limit: g.Limit}, g.Res.err
}
