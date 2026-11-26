package personalize

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

// Owner API for Recommendations pages (internal/admin serves these, design
// §7). Every method is safe for concurrent use.

// PageDocument returns the page (saved, or the seeded default), the
// library behind it and the viewers with changes.
func (e *Engine) PageDocument(ctx context.Context, key core.PageKey) (core.PageDocument, error) {
	if !key.Valid() {
		return core.PageDocument{}, &ValidationError{Problems: []Problem{{"page", "must be home or library:<id>/<tab>"}}}
	}
	p, saved := e.Page(ctx, key)
	doc := core.PageDocument{Page: p, Saved: saved, ViewersWithChanges: []core.ViewerChanges{}, DefaultLength: e.opts.MaxHomeRows}
	libs := e.pageLibraries(ctx)
	if id, _, ok := key.Library(); ok {
		doc.DefaultLength = e.opts.MaxSectionRows
		for i := range libs {
			if libs[i].ID == id {
				l := libs[i]
				doc.Library = &l
			}
		}
	} else {
		doc.Libraries = libs
	}
	for _, v := range p.Variants {
		doc.ViewersWithChanges = append(doc.ViewersWithChanges, core.ViewerChanges{ID: v.Viewer, Changes: len(v.Rows)})
	}
	sort.SliceStable(doc.ViewersWithChanges, func(i, j int) bool {
		return doc.ViewersWithChanges[i].Changes > doc.ViewersWithChanges[j].Changes
	})
	return doc, nil
}

func (e *Engine) pageLibraries(ctx context.Context) []core.PageLibrary {
	if e.opts.PageLibraries == nil {
		return []core.PageLibrary{}
	}
	return e.opts.PageLibraries(ctx)
}

// PageSources lists the row sources for a page: what each needs, which of
// the page's libraries can feed it, and how many rows of it the page has.
func (e *Engine) PageSources(ctx context.Context, key core.PageKey) (core.PageSources, error) {
	if !key.Valid() {
		return core.PageSources{}, &ValidationError{Problems: []Problem{{"page", "must be home or library:<id>/<tab>"}}}
	}
	libs := e.pageLibraries(ctx)
	pageLibs := libs
	if id, _, ok := key.Library(); ok {
		pageLibs = nil
		for _, l := range libs {
			if l.ID == id {
				pageLibs = append(pageLibs, l)
			}
		}
	}
	p, _ := e.Page(ctx, key)
	used := map[string]int{}
	for _, r := range p.Rows {
		used[r.Source.Kind]++
	}
	out := core.PageSources{Sources: []core.PageSource{}, Libraries: libs}
	var cardRefs []core.ParamOption
	for _, sp := range e.collectionSpecs(ctx) {
		if sp.OwnerUserID == 0 && sp.Enabled && sp.RatingKey != "" {
			cardRefs = append(cardRefs, core.ParamOption{Value: sp.RatingKey, Label: sp.Name})
		}
		if sp.OwnerUserID == 0 {
			out.Collections = append(out.Collections, core.CollectionRef{ID: sp.ID, Name: sp.Name, Count: sp.ItemCount})
		}
	}
	for _, s := range e.SourceInfos() {
		if s.HomeOnly && key != core.PageHome {
			continue
		}
		ps := core.PageSource{RowSourceInfo: s, Used: used[s.Kind]}
		for _, l := range pageLibs {
			if s.Library != 0 && s.Library != l.ID {
				continue
			}
			if hasCaps(l.Caps, s.Needs) {
				ps.Libraries = append(ps.Libraries, l.ID)
			}
		}
		ps.Available = len(ps.Libraries) > 0 || len(s.Needs) == 0
		if key == core.PageHome && slices.Contains(s.Needs, core.CapLive) {
			// Live TV is its own section, not a library: Home can always
			// show what's on (the row is empty without channels).
			ps.Available = true
		}
		if s.Kind == SourceSurface && e.opts.Surfaces != nil {
			for _, sf := range e.opts.Surfaces(ctx) {
				name := sf.Title
				if sf.PluginName != "" {
					name = sf.PluginName + " · " + sf.Title
				}
				ps.Refs = append(ps.Refs, core.ParamOption{Value: sf.Plugin + ":" + sf.ID, Label: name})
			}
			ps.Available = len(ps.Refs) > 0
		}
		if !ps.Available {
			ps.Unavailable = "No library here has what this row needs"
		}
		if ps.Available && metadataRowList(s.Kind) != "" {
			eligible := ps.Libraries[:0]
			for _, lib := range pageLibs {
				if slices.Contains(ps.Libraries, lib.ID) && e.metadataRowAvailable(s.Kind, lib.MediaType) {
					eligible = append(eligible, lib.ID)
				}
			}
			ps.Libraries = eligible
			if len(eligible) == 0 {
				ps.Available = false
				ps.Unavailable = "metadata_source_unavailable"
			}
		}
		e.pluginSourceRefs(ctx, &ps, pageLibs)
		if s.Kind == SourceCollectionCards {
			ps.Refs = cardRefs
		}
		if s.Kind == SourceCollection {
			// The picker lists each collection by its name.
			for _, c := range out.Collections {
				name := c.Name
				if name == "" {
					name = "Untitled collection"
				}
				ps.Refs = append(ps.Refs, core.ParamOption{Value: c.ID, Label: name})
			}
		}
		out.Sources = append(out.Sources, ps)
	}
	return out, nil
}

func hasCaps(have, need []string) bool {
	for _, n := range need {
		if !slices.Contains(have, n) {
			return false
		}
	}
	return true
}

// CollectionUsage lists where a collection is a row ("Used on" in
// Collection Studio). pageName names a page for the owner.
func (e *Engine) CollectionUsage(ctx context.Context, collectionID string, pageName func(core.PageKey) string) []core.PageUsage {
	out := []core.PageUsage{}
	for _, p := range e.Pages(ctx) {
		for _, r := range p.Rows {
			if r.Source.Kind != SourceCollection || r.Source.Param("collection", "") != collectionID {
				continue
			}
			u := core.PageUsage{Page: p.Key, RowID: r.ID, RowName: r.Name, Placement: r.Placement.Mode, Position: r.Placement.Position,
				Schedule: ScheduleSummary(r.Placement.When, r.Placement.Devices)}
			if pageName != nil {
				u.PageName = pageName(p.Key)
			}
			out = append(out, u)
		}
	}
	return out
}

// ScheduleSummary describes a row's when and devices in plain words
// ("Fri–Sat evenings · TV"); "" is any time on every device.
func ScheduleSummary(s *core.Schedule, devices []string) string {
	var parts []string
	if !s.IsZero() {
		if len(s.Holidays) > 0 {
			names := []string{}
			for _, h := range s.Holidays {
				for _, info := range core.Holidays {
					if info.ID == h {
						names = append(names, info.Label)
					}
				}
			}
			parts = append(parts, strings.Join(names, ", "))
		}
		if len(s.Days) > 0 {
			short := []string{"Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"}
			days := []string{}
			for _, d := range s.Days {
				if d >= 0 && d < 7 {
					days = append(days, short[d])
				}
			}
			parts = append(parts, strings.Join(days, ", "))
		}
		if len(s.Dayparts) > 0 {
			parts = append(parts, strings.Join(s.Dayparts, ", "))
		}
		if s.FromHour != nil || s.ToHour != nil {
			from, to := 0, 24
			if s.FromHour != nil {
				from = *s.FromHour
			}
			if s.ToHour != nil {
				to = *s.ToHour
			}
			parts = append(parts, fmt.Sprintf("%02d:00–%02d:00", from, to))
		}
		if len(s.Months) > 0 {
			months := []string{}
			for _, m := range s.Months {
				if m >= 1 && m <= 12 {
					months = append(months, time.Month(m).String()[:3])
				}
			}
			parts = append(parts, strings.Join(months, ", "))
		}
		for _, d := range s.Dates {
			parts = append(parts, d.From+"–"+d.To)
		}
	}
	if len(devices) > 0 {
		parts = append(parts, strings.Join(devices, ", "))
	}
	return strings.Join(parts, " · ")
}
