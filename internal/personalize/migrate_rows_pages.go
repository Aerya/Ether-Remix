package personalize

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/braedonsaunders/ether/internal/core"
)

// One-time migration rows-pages-v1: the Home layout document (global and
// viewer pages of row policies), the collections' old publication targets
// and the Ether-managed "Discovery sources" become Recommendations pages.
// internal/app runs it once per database; it is deleted with the next
// release that no longer upgrades from 0.3.20.

// RowsPagesMigration is the migration's marker in personalize_meta.
const RowsPagesMigration = "rows-pages-v1"

// LegacyCollectionPlacement is a collection's old targets (see
// collections.LegacyPlacement).
type LegacyCollectionPlacement struct {
	ID         string
	MediaType  string
	Enabled    bool
	HomeRow    bool
	SectionRow bool
	Viewers    []int64
	When       *core.Schedule
	Devices    []string
}

// legacy document shapes (pre-pages).
type legacyLayout struct {
	Global  []legacyPage `json:"global"`
	Viewers []struct {
		UserID int64        `json:"userId"`
		Pages  []legacyPage `json:"pages"`
	} `json:"viewers"`
}

type legacyPage struct {
	Section       string            `json:"section"`
	MaxRows       int               `json:"maxRows"`
	DeviceMaxRows map[string]int    `json:"deviceMaxRows"`
	Rows          []legacyPolicy    `json:"rows"`
	DedupeHome    *bool             `json:"dedupeHome"`
	Tuning        *core.PageTuning  `json:"tuning"`
	Extra         map[string]string `json:"-"`
}

type legacyPolicy struct {
	RankedTop10    *bool   `json:"rankedTop10"`
	PreviewOnFocus *bool   `json:"previewOnFocus"`
	ShowRepeats    *bool   `json:"showRepeats"`
	Style          string  `json:"style"`
	HideWatched    *bool   `json:"hideWatched"`
	Row            string  `json:"row"`
	Status         string  `json:"status"`
	Slot           int     `json:"slot"`
	MinSlot        int     `json:"minSlot"`
	MaxSlot        int     `json:"maxSlot"`
	Priority       float64 `json:"priority"`
	MaxPerPage     int     `json:"maxPerPage"`
	MaxDaysPerWeek int     `json:"maxDaysPerWeek"`
	When           *struct {
		Days      []int    `json:"days"`
		Dayparts  []string `json:"dayparts"`
		Months    []int    `json:"months"`
		StartDate string   `json:"startDate"`
		EndDate   string   `json:"endDate"`
		FromHour  *int     `json:"fromHour"`
		ToHour    *int     `json:"toHour"`
		Devices   []string `json:"devices"`
	} `json:"when"`
}

// retiredDiscovery maps the Ether-managed discovery collections (public
// ids) onto built-in sources and the library they were typed for.
var retiredDiscovery = map[string]struct {
	src core.RowSource
	lib core.LibraryID
}{
	"trending-week-movie": {core.RowSource{Kind: KindTrending, Params: map[string]string{"window": "week"}}, core.LibraryMovies},
	"trending-week-show":  {core.RowSource{Kind: KindTrending, Params: map[string]string{"window": "week"}}, core.LibraryShows},
	"trending-day-movie":  {core.RowSource{Kind: KindTrending, Params: map[string]string{"window": "day"}}, core.LibraryMovies},
	"trending-day-show":   {core.RowSource{Kind: KindTrending, Params: map[string]string{"window": "day"}}, core.LibraryShows},
	"top-rated-movie":     {core.RowSource{Kind: KindTopRated}, core.LibraryMovies},
	"top-rated-show":      {core.RowSource{Kind: KindTopRated}, core.LibraryShows},
	"popular-movie":       {core.RowSource{Kind: KindPopular}, core.LibraryMovies},
	"popular-show":        {core.RowSource{Kind: KindPopular}, core.LibraryShows},
	"now-playing-movie":   {core.RowSource{Kind: KindNewReleases}, core.LibraryMovies},
	"new-releases-movie":  {core.RowSource{Kind: KindNewReleases}, core.LibraryMovies},
	"upcoming-movie":      {core.RowSource{Kind: KindUpcoming}, core.LibraryMovies},
	"airing-today-show":   {core.RowSource{Kind: KindAiringToday}, core.LibraryShows},
	"on-the-air-show":     {core.RowSource{Kind: KindAiringToday, Params: map[string]string{"window": "week"}}, core.LibraryShows},
}

// RetiredDiscoveryIDs lists the Ether-managed discovery collections the
// migration maps onto built-in sources (the caller deletes them after).
func RetiredDiscoveryIDs() []string {
	out := make([]string, 0, len(retiredDiscovery))
	for id := range retiredDiscovery {
		out = append(out, id)
	}
	slices.Sort(out)
	return out
}

// MigrateRowsPages converts the pre-pages Home layout document and the
// collections' old placements into Recommendations pages and stores them.
// rowsTab names a library's first rows tab ("" for "recommended").
func (e *Engine) MigrateRowsPages(ctx context.Context, placements []LegacyCollectionPlacement) (int, error) {
	raw, err := e.LegacyLayoutBackup(ctx)
	if err != nil {
		return 0, err
	}
	pages, err := e.convertLegacyLayout(ctx, raw, placements)
	if err != nil {
		return 0, err
	}
	if err := e.ImportPages(ctx, pages); err != nil {
		return 0, err
	}
	return len(pages), nil
}

func (e *Engine) convertLegacyLayout(ctx context.Context, raw string, placements []LegacyCollectionPlacement) ([]core.RowPage, error) {
	var doc legacyLayout
	if strings.TrimSpace(raw) != "" {
		if err := json.Unmarshal([]byte(raw), &doc); err != nil {
			e.log.Warn("personalize: old Home layout unreadable; Recommendations pages start from defaults", "err", err)
			doc = legacyLayout{}
		}
	}
	pages := map[core.PageKey]*core.RowPage{}
	order := []core.PageKey{}
	pageFor := func(section string) *core.RowPage {
		key := e.pageKeyFor(ctx, section)
		if section != "home" && section != "" {
			if _, ok := parseLibrarySection(section); !ok {
				return nil
			}
		}
		if p := pages[key]; p != nil {
			return p
		}
		p := DefaultPage(key)
		pages[key] = &p
		order = append(order, key)
		return &p
	}
	type bound struct {
		page *core.RowPage
		id   string
	}
	everyone := map[string]bound{} // page key + source key → the Everyone row
	for _, lp := range doc.Global {
		p := pageFor(lp.Section)
		if p == nil {
			continue
		}
		p.Length = core.PageLength{Rows: lp.MaxRows, ByDevice: lp.DeviceMaxRows}
		p.SkipHome = lp.DedupeHome
		p.Tuning = lp.Tuning
		anchors := 0
		for _, pol := range lp.Rows {
			row, ok := e.legacyRow(p.Key, pol)
			if !ok {
				continue
			}
			k := string(p.Key) + "|" + rowSourceKey(row)
			if _, dup := everyone[k]; dup {
				continue
			}
			if row.Placement.Mode == core.PlacementPinned {
				anchors++
			}
			p.Rows = append(p.Rows, row)
			everyone[k] = bound{p, row.ID}
		}
		// The old workaround for "exactly these rows": every row anchored
		// and the length cut to the anchors. That is a Curated page now.
		if lp.MaxRows > 0 && anchors > 0 && anchors >= lp.MaxRows && anchors == countVisible(p.Rows) {
			p.Mode = core.PageCurated
			p.Picks = false
			slices.SortStableFunc(p.Rows, func(a, b core.Row) int { return a.Placement.Position - b.Placement.Position })
			for i := range p.Rows {
				if p.Rows[i].Placement.Mode != core.PlacementHide {
					p.Rows[i].Placement.Mode, p.Rows[i].Placement.Position = core.PlacementShow, 0
				}
				p.Rows[i].Placement.NotAbove, p.Rows[i].Placement.NotBelow, p.Rows[i].Placement.Priority = 0, 0, 0
			}
		}
	}
	for _, v := range doc.Viewers {
		for _, lp := range v.Pages {
			p := pageFor(lp.Section)
			if p == nil {
				continue
			}
			variant := core.PageVariant{Viewer: v.UserID, Rows: []core.RowOverride{}}
			if lp.MaxRows > 0 || len(lp.DeviceMaxRows) > 0 {
				variant.Length = &core.PageLength{Rows: lp.MaxRows, ByDevice: lp.DeviceMaxRows}
			}
			for _, pol := range lp.Rows {
				row, ok := e.legacyRow(p.Key, pol)
				if !ok {
					continue
				}
				if b, ok := everyone[string(p.Key)+"|"+rowSourceKey(row)]; ok {
					if p.Mode == core.PageCurated && (row.Placement.Mode == core.PlacementPinned || row.Placement.Mode == core.PlacementAuto) {
						row.Placement.Mode, row.Placement.Position = core.PlacementShow, 0
					}
					look, place := row.Look, row.Placement
					variant.Rows = append(variant.Rows, core.RowOverride{Row: b.id, Look: &look, Placement: &place})
					continue
				}
				if row.Placement.Mode == core.PlacementHide {
					continue
				}
				// A row only this viewer had: a row just for them.
				row.Placement.Audience = core.Audience{Mode: core.AudienceOnly, Viewers: []int64{v.UserID}}
				if p.Mode == core.PageCurated {
					row.Placement.Mode, row.Placement.Position = core.PlacementShow, 0
				}
				p.Rows = append(p.Rows, row)
				everyone[string(p.Key)+"|"+rowSourceKey(row)] = bound{p, row.ID}
			}
			if len(variant.Rows) > 0 || variant.Length != nil {
				p.Variants = append(p.Variants, variant)
			}
		}
	}
	// Collections published as Home or library rows by their own targets.
	libs := e.pageLibraries(ctx)
	for _, cp := range placements {
		if !cp.Enabled || retiredDiscovery[cp.ID].src.Kind != "" {
			continue
		}
		place := func(p *core.RowPage) {
			src := core.RowSource{Kind: SourceCollection, Params: map[string]string{"collection": cp.ID}}
			k := string(p.Key) + "|" + sourceKey(SourceCollection, cp.ID)
			if b, ok := everyone[k]; ok {
				// A layout policy already placed it: the collection's own
				// schedule and audience join that row.
				r := p.RowByID(b.id)
				if r != nil && r.Placement.When == nil {
					r.Placement.When, r.Placement.Devices = cp.When, cp.Devices
				}
				if r != nil && cp.Viewers != nil && r.Placement.Audience.Mode == core.AudienceEveryone {
					r.Placement.Audience = core.Audience{Mode: core.AudienceOnly, Viewers: cp.Viewers}
				}
				return
			}
			mode := core.PlacementAuto
			if p.Mode == core.PageCurated {
				mode = core.PlacementShow
			}
			row := core.Row{ID: NewRowID(), Source: src, Placement: core.RowPlacement{Mode: mode, When: cp.When, Devices: cp.Devices,
				Audience: core.Audience{Mode: core.AudienceEveryone}}}
			if cp.Viewers != nil {
				if len(cp.Viewers) == 0 {
					return // nobody was shown it
				}
				row.Placement.Audience = core.Audience{Mode: core.AudienceOnly, Viewers: cp.Viewers}
			}
			p.Rows = append(p.Rows, row)
			everyone[k] = bound{p, row.ID}
		}
		if cp.HomeRow {
			place(pageFor("home"))
		}
		if cp.SectionRow {
			for _, l := range libs {
				if l.Plugin != "" || l.MediaType == "" {
					continue
				}
				if cp.MediaType == "movie" && l.MediaType != core.TypeMovie || cp.MediaType == "show" && l.MediaType != core.TypeShow {
					continue
				}
				if p := pageFor(librarySectionName(int(l.ID))); p != nil {
					place(p)
				}
			}
		}
	}
	var out []core.RowPage
	for _, key := range order {
		p := pages[key]
		if len(p.Rows) == 0 && len(p.Variants) == 0 && p.Length.Rows == 0 && len(p.Length.ByDevice) == 0 && p.Tuning == nil && p.SkipHome == nil {
			continue // nothing the default doesn't already say
		}
		out = append(out, *p)
	}
	return out, nil
}

func countVisible(rows []core.Row) int {
	n := 0
	for _, r := range rows {
		if r.Placement.Mode != core.PlacementHide {
			n++
		}
	}
	return n
}

func rowSourceKey(r core.Row) string {
	_, param := sourceParam(r.Source)
	key := sourceKey(r.Source.Kind, param)
	if len(r.Scope.Libraries) > 0 {
		key += fmt.Sprint(r.Scope.Libraries)
	}
	for _, k := range []string{"window", "genre"} {
		if v := r.Source.Params[k]; v != "" {
			key += "|" + k + "=" + v
		}
	}
	return key
}

// legacyRow converts one old row policy into a page row; ok is false for a
// policy with nothing to keep (a plain Eligible, a wildcard over several
// kinds, or a row that no longer exists).
func (e *Engine) legacyRow(page core.PageKey, pol legacyPolicy) (core.Row, bool) {
	src, lib, ok := e.legacySource(pol.Row)
	if !ok {
		return core.Row{}, false
	}
	info, known := e.sourceInfo(src.Kind)
	if !known && !strings.HasPrefix(src.Kind, "plugin:") {
		return core.Row{}, false
	}
	row := core.Row{ID: NewRowID(), Source: src}
	if lib != 0 {
		if page == core.PageHome {
			row.Scope.Libraries = []core.LibraryID{lib}
		} else if id, _, _ := page.Library(); id != lib && (lib == core.LibraryMovies || lib == core.LibraryShows) {
			// A movie list on a TV library page (or the reverse) never showed.
			return core.Row{}, false
		}
	}
	if pol.MaxPerPage > 0 && slices.ContainsFunc(info.Params, func(p core.RowParam) bool { return p.Key == "count" }) {
		if row.Source.Params == nil {
			row.Source.Params = map[string]string{}
		}
		row.Source.Params["count"] = fmt.Sprint(min(pol.MaxPerPage, 3))
	}
	look := core.RowLook{Numbered: pol.RankedTop10, Trailer: pol.PreviewOnFocus}
	switch pol.Style {
	case "shelf":
		look.Style = core.RowStyleStandard
	case core.RowStyleHero, core.RowStyleWide:
		look.Style = pol.Style
	}
	look.HideWatched = pol.HideWatched != nil && *pol.HideWatched
	look.KeepWhole = pol.ShowRepeats != nil && *pol.ShowRepeats
	row.Look = look
	place := core.RowPlacement{Audience: core.Audience{Mode: core.AudienceEveryone}, DaysPerWeek: min(pol.MaxDaysPerWeek, 6),
		NotAbove: pol.MinSlot, NotBelow: pol.MaxSlot}
	switch {
	case pol.Status == "off":
		place.Mode = core.PlacementHide
	case pol.Status == "always" && pol.Slot > 0:
		place.Mode, place.Position = core.PlacementPinned, min(pol.Slot, 50)
	case pol.Status == "always":
		place.Mode = core.PlacementShow
	default:
		place.Mode = core.PlacementAuto
	}
	switch {
	case pol.Priority > 1:
		place.Priority = 1
	case pol.Priority > 0 && pol.Priority < 1:
		place.Priority = -1
	}
	if w := pol.When; w != nil {
		s := &core.Schedule{Days: w.Days, Dayparts: w.Dayparts, Months: w.Months, FromHour: w.FromHour, ToHour: w.ToHour}
		if strings.TrimSpace(w.StartDate) != "" || strings.TrimSpace(w.EndDate) != "" {
			s.Dates = []core.DateRange{{From: strings.TrimSpace(w.StartDate), To: strings.TrimSpace(w.EndDate)}}
		}
		if !s.IsZero() {
			place.When = s
		}
		place.Devices = w.Devices
	}
	if place.NotAbove > 0 && place.NotBelow > 0 && place.NotBelow < place.NotAbove {
		place.NotBelow = 0
	}
	row.Placement = place
	if place.Mode == core.PlacementAuto && look == (core.RowLook{}) && place.When == nil && len(place.Devices) == 0 &&
		place.DaysPerWeek == 0 && place.NotAbove == 0 && place.NotBelow == 0 && place.Priority == 0 && lib == 0 && row.Source.Params["count"] == "" {
		return core.Row{}, false // a plain Eligible was the default
	}
	return row, true
}

// legacySource resolves an old row policy's target ("home.trending",
// "trending", "because.*", "collection:12", "collection.trending-week-movie",
// "plugin:x/y") to a source, plus the library a typed discovery
// collection was for.
func (e *Engine) legacySource(target string) (core.RowSource, core.LibraryID, bool) {
	target = strings.TrimSpace(target)
	if strings.HasPrefix(target, "plugin:") {
		return core.RowSource{Kind: target}, 0, !strings.ContainsAny(target, "*?[")
	}
	stem := target
	for _, p := range []string{"home.", "movie.", "tv."} {
		if s, ok := strings.CutPrefix(stem, p); ok {
			stem = s
			break
		}
	}
	for _, p := range []string{KindCollectionPrefix, "collection."} {
		if id, ok := strings.CutPrefix(stem, p); ok {
			if id == "" || strings.ContainsAny(id, "*?[") {
				return core.RowSource{}, 0, false
			}
			if d, ok := retiredDiscovery[id]; ok {
				return d.src, d.lib, true
			}
			return core.RowSource{Kind: SourceCollection, Params: map[string]string{"collection": id}}, 0, true
		}
	}
	if _, ok := kindByName[stem]; ok {
		return core.RowSource{Kind: stem}, 0, true
	}
	switch stem {
	case "continue", "continueWatching":
		return core.RowSource{Kind: KindContinue}, 0, true
	}
	if strings.HasPrefix(stem, "live.") || strings.HasPrefix(stem, "livetv.") {
		return core.RowSource{Kind: KindLive}, 0, true
	}
	// A glob over one kind's rows ("because.*") or a seeded row id.
	glob := strings.TrimSuffix(stem, ".*")
	if k := parseHubKind("home." + glob); k != "other" && !strings.HasPrefix(k, KindCollectionPrefix) {
		return core.RowSource{Kind: k}, 0, true
	}
	if k := parseHubKind("home." + stem); k != "other" && !strings.HasPrefix(k, KindCollectionPrefix) {
		return core.RowSource{Kind: k}, 0, true
	}
	return core.RowSource{}, 0, false
}
