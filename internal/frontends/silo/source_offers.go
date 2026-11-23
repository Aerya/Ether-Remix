package silo

import (
	"net/http"
	"strings"

	"github.com/braedonsaunders/ether/internal/frontend"
)

// Browse lists authorized version selectors only. Opening a leaf source
// belongs to playback. A catalog item never plans or probes; watch and the
// versions list do when the catalogue has no probed copy.
func (i *instance) sourceVersions(r *http.Request, scope frontend.VersionScope, detail frontend.ItemDetail) ([]frontend.Version, []frontend.SourceOffer, error) {
	if i.host.SourceOffers == nil {
		return nil, nil, frontend.ErrSourceGone
	}
	versions := make([]frontend.Version, 0, len(detail.Versions))
	releases := make([]string, 0, len(detail.Versions))
	for _, version := range detail.Versions {
		if version.ID != "" {
			versions = append(versions, version)
			releases = append(releases, version.ID)
		}
	}
	// A plugin library item has no catalogue editions and no detail source:
	// the broker plans it only through a version handle from its own menu.
	if len(versions) == 0 && detail.Title.Key.IsPluginKey() {
		return i.pluginVersions(r, scope, detail)
	}
	// A detail page does not open a resolver. Probed copies already on disk
	// are a catalogue read, and they are the only file a cold title has.
	if len(versions) == 0 && i.host.Editions != nil {
		listed, err := i.host.Editions.Editions(r.Context(), scope.Viewer, scope.Device, detail.Title.Key)
		if err != nil {
			return nil, nil, err
		}
		for _, version := range listed {
			if version.ID != "" {
				versions = append(versions, version)
				releases = append(releases, version.ID)
			}
		}
	}
	if len(versions) > 128 {
		return nil, nil, frontend.ErrSourceGone
	}
	targets := make([]frontend.SourceOfferTarget, 0, len(versions))
	if len(versions) > 0 {
		batch, ok := i.host.VersionHandles.(frontend.VersionHandleBatch)
		if !ok {
			return nil, nil, frontend.ErrSourceGone
		}
		handles, err := batch.IssueBatch(r.Context(), scope, detail.Title.Key, releases)
		if err != nil {
			return nil, nil, err
		}
		for _, version := range versions {
			if handles[version.ID] == "" {
				return nil, nil, frontend.ErrSourceGone
			}
			targets = append(targets, frontend.SourceOfferTarget{Item: detail.Title.Key, VersionHandle: handles[version.ID]})
		}
	}
	if len(targets) == 0 {
		return i.probedVersion(r, scope, detail)
	}
	offers, err := i.host.SourceOffers.IssueBatch(r.Context(), scope, targets)
	if err != nil {
		return nil, nil, err
	}
	if len(offers) != len(versions) {
		return nil, nil, frontend.ErrSourceGone
	}
	return versions, offers, nil
}

// detailOpensSource is an explicit file request. Catalog item browse stays on
// catalogue editions. Watch and the versions list each open one leaf, because
// playback needs that file id before the client can start.
func detailOpensSource(path string) bool {
	return strings.HasSuffix(path, "/versions") || strings.HasPrefix(path, "/api/v2/watch/")
}

// probedVersion opens one source for watch and the versions list. Item browse
// stays on catalogue data; a leaf open belongs to an explicit file request.
func (i *instance) probedVersion(r *http.Request, scope frontend.VersionScope, detail frontend.ItemDetail) ([]frontend.Version, []frontend.SourceOffer, error) {
	planner, ok := i.host.Sessions.(frontend.DetailPlanner)
	if !ok || i.host.SourceOffers == nil || !detailOpensSource(r.URL.Path) {
		return nil, nil, nil
	}
	plan, err := planner.DetailPlan(r.Context(), frontend.PlanRequest{
		Viewer:   scope.Viewer,
		Device:   scope.Device,
		Protocol: scope.Protocol,
		Item:     detail.Title.Key,
		Caps:     scope.Caps,
		Intent:   frontend.PlayIntent{Options: frontend.DecideOptions{DirectPlay: true, AudioStream: -1, SubtitleStream: -1}},
	})
	if err != nil || plan.DetailSource == "" || plan.ETag == "" {
		return nil, nil, nil
	}
	offers, err := i.host.SourceOffers.IssueBatch(r.Context(), scope, []frontend.SourceOfferTarget{{
		Item: detail.Title.Key, DetailSource: plan.DetailSource, ETag: plan.ETag,
	}})
	if err != nil {
		return nil, nil, err
	}
	if len(offers) != 1 {
		return nil, nil, frontend.ErrSourceGone
	}
	return []frontend.Version{{ID: plan.DetailSource, Media: plan.Media}}, offers, nil
}

// pluginVersions lists a plugin library item's own version menu (its Auto
// slot and the sources its resolver returned) as version-handle offers.
// Without it a plugin item fell through to probedVersion, whose detail-source
// offer the broker refuses for plugin keys, so no plugin item could play.
// The menu asks the plugin's resolver, so like probedVersion it runs only for
// watch and the versions list, never for item browse.
func (i *instance) pluginVersions(r *http.Request, scope frontend.VersionScope, detail frontend.ItemDetail) ([]frontend.Version, []frontend.SourceOffer, error) {
	if i.host.Editions == nil || i.host.VersionHandles == nil || i.host.SourceOffers == nil || !detailOpensSource(r.URL.Path) {
		return nil, nil, nil
	}
	batch, ok := i.host.VersionHandles.(frontend.VersionHandleBatch)
	if !ok {
		return nil, nil, nil
	}
	title := detail.Title
	// Watch and /versions are explicit playback/version requests: clear
	// LocalResponse and tag IndexerPurposePlayback so cold plugin items invoke
	// /resolve instead of returning empty cache-only results.
	vctx := frontend.WithoutLocalResponse(frontend.WithIndexerPurpose(r.Context(), frontend.IndexerPurposePlayback))
	req := frontend.ResolveRequest{Title: &title, Client: scope.Caps, User: scope.Viewer}
	var (
		variants []frontend.ReleaseVariant
		err      error
	)
	if slots, ok := i.host.Editions.(frontend.VersionSlots); ok {
		variants, err = slots.VersionSlots(vctx, req, frontend.MaxVariants, true)
	} else {
		variants, err = i.host.Editions.Variants(vctx, req, frontend.MaxVariants)
	}
	if err != nil || len(variants) == 0 {
		return nil, nil, nil
	}
	versions := make([]frontend.Version, 0, len(variants))
	releases := make([]string, 0, len(variants))
	seen := make(map[string]bool, len(variants))
	for _, variant := range variants {
		id := variant.ID
		if id == "" {
			id = variant.Release.ID
		}
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		versions = append(versions, frontend.Version{ID: id, Name: variant.Label, Edition: variant.Edition, Media: variant.Info})
		releases = append(releases, id)
	}
	if len(versions) == 0 {
		return nil, nil, nil
	}
	handles, err := batch.IssueBatch(vctx, scope, title.Key, releases)
	if err != nil {
		return nil, nil, nil
	}
	targets := make([]frontend.SourceOfferTarget, 0, len(versions))
	kept := versions[:0]
	for _, version := range versions {
		if handles[version.ID] == "" {
			continue
		}
		kept = append(kept, version)
		targets = append(targets, frontend.SourceOfferTarget{Item: title.Key, VersionHandle: handles[version.ID]})
	}
	if len(targets) == 0 {
		return nil, nil, nil
	}
	offers, err := i.host.SourceOffers.IssueBatch(vctx, scope, targets)
	if err != nil || len(offers) != len(kept) {
		return nil, nil, nil
	}
	return kept, offers, nil
}
