package personalize

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"math"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

const libraryRowsMigrationKey = "library-rows-v1"

func init() {
	store.Register("personalize", 2, `CREATE TABLE personalize_home_layout (id INTEGER PRIMARY KEY CHECK(id=1), document TEXT NOT NULL);`)
	store.Register("personalize", 4, `CREATE TABLE personalize_meta (k TEXT PRIMARY KEY, v TEXT NOT NULL DEFAULT '');`)
}

// parseLibrarySection parses a "library:<id>" layout page section name.
func parseLibrarySection(s string) (int, bool) {
	raw, ok := strings.CutPrefix(s, "library:")
	if !ok || raw == "" {
		return 0, false
	}
	id, err := strconv.Atoi(raw)
	if err != nil || id <= 0 || strconv.Itoa(id) != raw {
		return 0, false
	}
	return id, true
}

func isLibrarySection(s string) bool {
	_, ok := parseLibrarySection(s)
	return ok
}

func librarySectionName(id int) string {
	if id <= 0 {
		return ""
	}
	return "library:" + strconv.Itoa(id)
}

// layoutTargetForMedia returns the layout page name ("home" or "library:<id>").
// Library pages never inherit from Home or from any shared media-type page.
func layoutTargetForMedia(section core.MediaType, sectionID int) string {
	if section == "" {
		return surfHome
	}
	if sectionID > 0 {
		return librarySectionName(sectionID)
	}
	if section == core.TypeShow {
		return "library:2"
	}
	return "library:1"
}

// layoutTargetForContext returns the layout page for a request context: "home"
// for Home and library-scoped Home requests (Plex contentDirectoryID), or
// "library:<id>" for a library's Recommended page.
func layoutTargetForContext(section core.MediaType, hc core.HubContext) string {
	if hc.HomeScoped {
		return surfHome
	}
	return layoutTargetForMedia(section, hc.SectionID)
}

// layoutTarget resolves a caller section ("home", "library:<id>", or legacy
// internal media-type markers accompanied by HubContext.SectionID) into the
// independent layout page ("home" or "library:<id>").
func (e *Engine) layoutTarget(ctx context.Context, section string, sectionID int) string {
	if section == "" || section == surfHome {
		return surfHome
	}
	if isLibrarySection(section) {
		return section
	}
	if sectionID > 0 {
		return librarySectionName(sectionID)
	}
	if section == surfShows {
		if id := e.firstSectionID(ctx, core.TypeShow); id > 0 {
			return librarySectionName(id)
		}
		return "library:2"
	}
	if section == surfMovies {
		if id := e.firstSectionID(ctx, core.TypeMovie); id > 0 {
			return librarySectionName(id)
		}
		return "library:1"
	}
	return section
}

// pagesForTarget resolves the active Global and Viewer pages for target.
// Each target ("home" or "library:<id>") is completely independent.
func pagesForTarget(d core.HomeLayout, user int64, target string) (*core.LayoutPage, *core.LayoutPage) {
	if target == "" {
		target = surfHome
	}
	g := pageFor(d.Global, target)
	v := viewerPage(d, user, target)
	return g, v
}

// migrateSectionLayouts upgrades pre-0.3.16 section-level "movies" / "shows"
// layout pages (Global and Viewer overrides) onto each movie/show library that
// did not already have its own "library:<id>" Global page, then strips all
// "movies" and "shows" pages so Home and per-library layouts are disjoint.
func migrateSectionLayouts(d core.HomeLayout, sections map[int]core.MediaType) (core.HomeLayout, bool) {
	out := cloneLayout(d)
	changed := false

	ids := make([]int, 0, len(sections))
	for id, mt := range sections {
		if id > 0 && (mt == core.TypeMovie || mt == core.TypeShow) {
			ids = append(ids, id)
		}
	}
	sort.Ints(ids)

	hadOwnGlobal := map[string]bool{}
	for _, p := range out.Global {
		if isLibrarySection(p.Section) {
			hadOwnGlobal[p.Section] = true
		}
	}

	for _, pair := range []struct {
		base string
		mt   core.MediaType
	}{
		{surfMovies, core.TypeMovie},
		{surfShows, core.TypeShow},
	} {
		gBase := pageFor(out.Global, pair.base)
		for _, id := range ids {
			if sections[id] != pair.mt {
				continue
			}
			libSec := librarySectionName(id)
			if hadOwnGlobal[libSec] {
				continue
			}
			if gBase != nil {
				cp := cloneLayout(core.HomeLayout{Global: []core.LayoutPage{*gBase}}).Global[0]
				cp.Section = libSec
				out.Global = append(out.Global, cp)
				changed = true
			}
			for vi := range out.Viewers {
				if pageFor(out.Viewers[vi].Pages, libSec) != nil {
					continue
				}
				if vBase := pageFor(out.Viewers[vi].Pages, pair.base); vBase != nil {
					cp := cloneLayout(core.HomeLayout{Global: []core.LayoutPage{*vBase}}).Global[0]
					cp.Section = libSec
					out.Viewers[vi].Pages = append(out.Viewers[vi].Pages, cp)
					changed = true
				}
			}
		}
	}

	keptGlobal := make([]core.LayoutPage, 0, len(out.Global))
	for _, p := range out.Global {
		if p.Section == surfMovies || p.Section == surfShows {
			changed = true
			continue
		}
		keptGlobal = append(keptGlobal, p)
	}
	out.Global = keptGlobal

	keptViewers := make([]core.ViewerLayout, 0, len(out.Viewers))
	for _, vl := range out.Viewers {
		keptPages := make([]core.LayoutPage, 0, len(vl.Pages))
		for _, p := range vl.Pages {
			if p.Section == surfMovies || p.Section == surfShows {
				changed = true
				continue
			}
			keptPages = append(keptPages, p)
		}
		if len(keptPages) > 0 {
			vl.Pages = keptPages
			keptViewers = append(keptViewers, vl)
		} else if len(vl.Pages) > 0 {
			changed = true
		}
	}
	out.Viewers = keptViewers

	return out, changed
}

func (e *Engine) migrateLibraryLayouts(ctx context.Context) error {
	unlock, err := store.SessionLock(ctx, e.store.db, "personalize:library-rows")
	if err != nil {
		return err
	}
	if unlock != nil {
		defer unlock()
	}

	var marker string
	err = e.store.db.QueryRowContext(ctx, `SELECT v FROM personalize_meta WHERE k = ?`, libraryRowsMigrationKey).Scan(&marker)
	if err == nil && marker == "1" {
		return nil
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}

	var raw string
	err = e.store.db.QueryRowContext(ctx, `SELECT document FROM personalize_home_layout WHERE id = 1`).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		_, err = e.store.db.ExecContext(ctx, `INSERT INTO personalize_meta(k, v) VALUES(?, '1') ON CONFLICT(k) DO UPDATE SET v = excluded.v`, libraryRowsMigrationKey)
		return err
	}
	if err != nil {
		return err
	}

	var d core.HomeLayout
	if err := json.Unmarshal([]byte(raw), &d); err != nil {
		return err
	}

	secs := e.allSections(ctx)
	if rows, qErr := e.store.db.QueryContext(ctx, `SELECT id, media_type FROM libraries WHERE source != 'plugin' AND media_type IN ('movie', 'show')`); qErr == nil {
		for rows.Next() {
			var id int
			var mt string
			if sErr := rows.Scan(&id, &mt); sErr == nil && id > 0 {
				secs[id] = core.MediaType(mt)
			}
		}
		_ = rows.Close()
	}

	next, changed := migrateSectionLayouts(d, secs)
	next.CollectionSchedules = nil

	tx, err := e.store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if changed {
		b, err := json.Marshal(next)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE personalize_home_layout SET document = ? WHERE id = 1`, string(b)); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO personalize_meta(k, v) VALUES(?, '1') ON CONFLICT(k) DO UPDATE SET v = excluded.v`, libraryRowsMigrationKey); err != nil {
		return err
	}
	return tx.Commit()
}

func cloneLayout(d core.HomeLayout) core.HomeLayout {
	b, _ := json.Marshal(d)
	var out core.HomeLayout
	_ = json.Unmarshal(b, &out)
	if out.Global == nil {
		out.Global = []core.LayoutPage{}
	}
	if out.Viewers == nil {
		out.Viewers = []core.ViewerLayout{}
	}
	for i := range out.Global {
		if out.Global[i].Rows == nil {
			out.Global[i].Rows = []core.RowPolicy{}
		}
	}
	for i := range out.Viewers {
		if out.Viewers[i].Pages == nil {
			out.Viewers[i].Pages = []core.LayoutPage{}
		}
		for j := range out.Viewers[i].Pages {
			if out.Viewers[i].Pages[j].Rows == nil {
				out.Viewers[i].Pages[j].Rows = []core.RowPolicy{}
			}
		}
	}
	return out
}

// homeLayout loads the saved document; with none saved the layout is empty
// and every row is ranked by the engine.
func (s *sqlStore) homeLayout(ctx context.Context) (core.HomeLayout, error) {
	var raw string
	err := s.db.QueryRowContext(ctx, `SELECT document FROM personalize_home_layout WHERE id=1`).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return cloneLayout(core.HomeLayout{}), nil
	}
	if err != nil {
		return core.HomeLayout{}, err
	}
	var d core.HomeLayout
	err = json.Unmarshal([]byte(raw), &d)
	return cloneLayout(d), err
}
func (s *sqlStore) setHomeLayout(ctx context.Context, d core.HomeLayout) error {
	b, err := json.Marshal(d)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO personalize_home_layout(id,document) VALUES(1,?) ON CONFLICT(id) DO UPDATE SET document=excluded.document`, string(b))
	return err
}

func normalizeHomeLayout(d core.HomeLayout) (core.HomeLayout, error) {
	// Values JSON cannot carry would be lost by the clone below.
	nan := &ValidationError{}
	checkNaN := func(ps []core.LayoutPage, field string) {
		for i, p := range ps {
			if tu := p.Tuning; tu != nil {
				for name, v := range map[string]*float64{"discovery": tu.Discovery, "freshness": tu.Freshness, "variety": tu.Variety} {
					if v != nil && (math.IsNaN(*v) || math.IsInf(*v, 0)) {
						nan.add(fmt.Sprintf("%s[%d].tuning.%s", field, i, name), "must be 0-1")
					}
				}
			}
			for j, r := range p.Rows {
				if math.IsNaN(r.Priority) || math.IsInf(r.Priority, 0) {
					nan.add(fmt.Sprintf("%s[%d].rows[%d].priority", field, i, j), "must be 0-10")
				}
			}
		}
	}
	checkNaN(d.Global, "layout.global")
	for i, v := range d.Viewers {
		checkNaN(v.Pages, fmt.Sprintf("layout.viewers[%d].pages", i))
	}
	if err := nan.orNil(); err != nil {
		return d, err
	}
	d = cloneLayout(d)
	ve := &ValidationError{}
	pages := func(ps []core.LayoutPage, field string) {
		seen := map[string]bool{}
		for i := range ps {
			p := &ps[i]
			f := fmt.Sprintf("%s[%d]", field, i)
			if p.Section == "" {
				p.Section = "home"
			}
			if p.Section != "home" && !isLibrarySection(p.Section) {
				ve.add(f+".section", "must be home or library:<id>")
			}
			if seen[p.Section] {
				ve.add(f+".section", "duplicate section")
			}
			seen[p.Section] = true
			if p.MaxRows < 0 || p.MaxRows > 100 {
				ve.add(f+".maxRows", "must be 0-100")
			}
			for dev, n := range p.DeviceMaxRows {
				if !validDevices[dev] || n < 1 || n > 100 {
					ve.add(f+".deviceMaxRows", "devices must be tv, mobile, web or desktop with limits 1-100")
				}
			}
			if p.DedupeHome != nil && p.Section == "home" {
				ve.add(f+".dedupeHome", "only applies to library pages")
			}
			if tu := p.Tuning; tu != nil {
				for _, dial := range []struct {
					name string
					v    *float64
				}{{"discovery", tu.Discovery}, {"freshness", tu.Freshness}, {"variety", tu.Variety}} {
					if dial.v != nil && (math.IsNaN(*dial.v) || *dial.v < 0 || *dial.v > 1) {
						ve.add(f+".tuning."+dial.name, "must be 0-1")
					}
				}
				if tu.Discovery == nil && tu.Freshness == nil && tu.Variety == nil {
					p.Tuning = nil // every dial inherits
				}
			}
			ids := map[string]bool{}
			slots := map[int]bool{}
			for j := range p.Rows {
				r := &p.Rows[j]
				rf := fmt.Sprintf("%s.rows[%d]", f, j)
				if strings.TrimSpace(r.Row) == "" {
					ve.add(rf+".row", "is required")
				}
				if _, err := path.Match(r.Row, ""); err != nil {
					ve.add(rf+".row", "invalid pattern")
				}
				if ids[r.Row] {
					ve.add(rf+".row", "duplicate row")
				}
				ids[r.Row] = true
				if r.Status != "always" && r.Status != "eligible" && r.Status != "off" {
					ve.add(rf+".status", "must be always, eligible or off")
				}
				for name, n := range map[string]int{"slot": r.Slot, "minSlot": r.MinSlot, "maxSlot": r.MaxSlot, "maxPerPage": r.MaxPerPage} {
					if n < 0 || n > 100 {
						ve.add(rf+"."+name, "must be 0-100")
					}
				}
				if r.MinSlot > 0 && r.MaxSlot > 0 && r.MinSlot > r.MaxSlot {
					ve.add(rf+".maxSlot", "must not precede minSlot")
				}
				if math.IsNaN(r.Priority) || math.IsInf(r.Priority, 0) || r.Priority < 0 || r.Priority > 10 {
					ve.add(rf+".priority", "must be 0-10")
				}
				if r.MaxDaysPerWeek < 0 || r.MaxDaysPerWeek > 7 {
					ve.add(rf+".maxDaysPerWeek", "must be 0-7")
				}
				if r.Slot > 0 {
					if slots[r.Slot] {
						ve.add(rf+".slot", "another row owns this slot")
					}
					slots[r.Slot] = true
					if r.Status != "off" {
						r.Status = "always"
					}
					r.MinSlot = 0
					r.MaxSlot = 0
				}
				if w := r.When; w != nil {
					validateWhen(ve, rf+".when", w)
					for _, m := range w.Months {
						if m < 1 || m > 12 {
							ve.add(rf+".when.months", "must be 1-12")
						}
					}
					for _, dp := range w.Dayparts {
						if dp != "morning" && dp != "afternoon" && dp != "evening" && dp != "late" {
							ve.add(rf+".when.dayparts", "invalid daypart")
						}
					}
					for _, date := range []string{w.StartDate, w.EndDate} {
						if date != "" {
							if _, err := time.Parse("2006-01-02", date); err != nil {
								ve.add(rf+".when", "dates must be YYYY-MM-DD")
							}
						}
					}
					if w.StartDate != "" && w.EndDate != "" && w.StartDate > w.EndDate {
						ve.add(rf+".when.endDate", "must follow startDate")
					}
				}
			}
		}
	}
	pages(d.Global, "layout.global")
	// A collection row's schedule belongs to its collection
	// (collectionSchedules); only a viewer override may replace it.
	for i, p := range d.Global {
		for j, r := range p.Rows {
			if r.When != nil && collectionRowID(r.Row) != "" {
				ve.add(fmt.Sprintf("layout.global[%d].rows[%d].when", i, j), "a collection row uses its collection's schedule")
			}
		}
	}
	seen := map[int64]bool{}
	for i, v := range d.Viewers {
		if v.UserID <= 0 || seen[v.UserID] {
			ve.add(fmt.Sprintf("layout.viewers[%d].userId", i), "must be a unique positive viewer id")
		}
		seen[v.UserID] = true
		pages(v.Pages, fmt.Sprintf("layout.viewers[%d].pages", i))
	}
	return d, ve.orNil()
}
func (e *Engine) HomeLayout(ctx context.Context) (core.HomeLayout, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return cloneLayout(e.homeLayout), nil
}
func (e *Engine) SetHomeLayout(ctx context.Context, d core.HomeLayout) (core.HomeLayout, error) {
	d, err := normalizeHomeLayout(d)
	if err != nil {
		return d, err
	}
	d.UpdatedAt = e.now()
	// Collection schedules are saved with their collections, never here.
	d.CollectionSchedules = nil
	if err = e.store.setHomeLayout(ctx, d); err != nil {
		return d, err
	}
	e.mu.Lock()
	e.homeLayout = cloneLayout(d)
	e.layoutVer++
	e.mu.Unlock()
	e.notify("layout")
	return cloneLayout(d), nil
}
func pageFor(ps []core.LayoutPage, section string) *core.LayoutPage {
	for i := range ps {
		if ps[i].Section == section {
			return &ps[i]
		}
	}
	return nil
}
func matchingPolicy(p *core.LayoutPage, row core.HomeRow) *core.RowPolicy {
	if p == nil {
		return nil
	}
	rank := 0
	var best *core.RowPolicy
	stem := hubStem(row.HubID)
	for i := range p.Rows {
		r := &p.Rows[i]
		// Bulk edits give hundreds of rows their own exact rule and this
		// runs rows × rules per Home request, so a miss must stay cheap:
		// three string compares and a byte scan, no allocation.
		n := matchRank(r.Row, row.HubID, stem, row.Kind)
		if n >= rank && n > 0 {
			best = r
			rank = n
		}
	}
	return best
}
func weekEligible(user int64, key string, hc core.HubContext, n int) bool {
	if n <= 0 || n >= 7 {
		return true
	}
	h := fnv.New32a()
	fmt.Fprintf(h, "%d:%s", user, key)
	day := (int(hc.Now.Weekday()) + 6) % 7
	return (day+int(h.Sum32()%7))%7 < n
}

func policyHiddenReason(p *core.RowPolicy, layer string, user int64, hc core.HubContext) (code, why string) {
	if p == nil {
		return "", ""
	}
	switch {
	case p.Status == "off":
		if layer == "override" {
			return "off_override", "Off in override layer"
		}
		return "off_global", "Off in global layer"
	case !whenMatches(p.When, hc):
		return "outside_schedule", "Outside schedule"
	case !weekEligible(user, p.Row, hc, p.MaxDaysPerWeek):
		return "weekly_cap", "Weekly frequency cap"
	}
	return "", ""
}

// arrangeLayout is the final, shared composition pass, including front-end rows.
// Explicit "always" rows are never dropped by page-length caps or slot conflicts;
// eligible rows remain bounded by page length and position constraints.
func arrangeLayout(rows []core.HomeRow, d core.HomeLayout, user int64, hc core.HubContext, section string, def int) []core.HomeRow {
	if section == "" {
		section = "home"
	}
	target := section
	if !isLibrarySection(section) && section != surfHome {
		if hc.SectionID > 0 {
			target = librarySectionName(hc.SectionID)
		} else if section == surfShows {
			target = "library:2"
		} else if section == surfMovies {
			target = "library:1"
		}
	}
	return arrangeLayoutFor(rows, d, user, hc, target, def)
}

func arrangeLayoutFor(rows []core.HomeRow, d core.HomeLayout, user int64, hc core.HubContext, target string, def int) []core.HomeRow {
	g, v := pagesForTarget(d, user, target)
	maxRows := def
	for _, p := range []*core.LayoutPage{g, v} {
		if p != nil {
			if p.MaxRows > 0 {
				maxRows = p.MaxRows
			}
			if p.DeviceMaxRows[hc.Device] > 0 {
				maxRows = p.DeviceMaxRows[hc.Device]
			}
		}
	}
	if maxRows <= 0 {
		maxRows = 14
	}
	type candidate struct {
		r              core.HomeRow
		p              *core.RowPolicy
		rank           float64
		explicitAlways bool
	}
	var activeRows []core.HomeRow
	for _, row := range rows {
		p := matchingPolicy(g, row)
		layer := "global"
		if q := matchingPolicy(v, row); q != nil {
			p = q
			layer = "override"
		}
		if code, _ := policyHiddenReason(p, layer, user, hc); code != "" {
			continue
		}
		activeRows = append(activeRows, row)
	}
	// Rows no owner policy covers get the engine's defaults: Continue Watching
	// first, then top picks, new releases and trending discovery.
	lead, defaults := leadDefaults(activeRows, g, v, maxRows)
	var pool []candidate
	hidden := []core.HomeRow{}
	counts := map[string]int{}
	for _, row := range rows {
		row.Hidden = false
		row.HiddenCode = ""
		row.HiddenReason = ""
		row.Slot = 0
		row.AnchorSlot = 0
		row.Pinned = false
		row.Layer = "personalized"
		row.Placement = "Chosen by personalization"
		p := matchingPolicy(g, row)
		layer := "global"
		if q := matchingPolicy(v, row); q != nil {
			p = q
			layer = "override"
		}
		row.Policy = p
		if p != nil && p.PreviewOnFocus != nil {
			row.Hub.PreviewOnFocus = *p.PreviewOnFocus
		}
		// Built-in Top 10 families are ranked by default. Resolve this before
		// the three-state owner policy so an explicit Off still wins.
		if row.Kind == KindTop10 || row.Kind == KindTop10Today {
			row.Hub.RankedTop10 = true
		}
		if p != nil && p.RankedTop10 != nil {
			row.Hub.RankedTop10 = *p.RankedTop10
		}
		row.PolicyLayer = ""
		explicitAlways := false
		if p != nil {
			row.PolicyLayer = layer
			if p.Priority > 0 || p.MinSlot > 0 || p.MaxSlot > 0 || p.When != nil || p.MaxPerPage > 0 || p.MaxDaysPerWeek > 0 {
				row.Placement = "Personalized within " + layer + " constraints"
			}
			if layer == "override" {
				row.Layer = layer
				row.Placement = "Viewer override"
			}
			if code, why := policyHiddenReason(p, layer, user, hc); why != "" {
				row.Layer = layer
				row.Hidden = true
				row.HiddenCode = code
				row.HiddenReason = why
				hidden = append(hidden, row)
				continue
			}
			if p.Status == "always" {
				explicitAlways = true
				row.Layer = layer
				row.Placement = "Always included by " + layer
			}
			if p.Slot > 0 {
				row.AnchorSlot = p.Slot
				row.Pinned = true
				row.Layer = layer
				row.Placement = fmt.Sprintf("%s anchor at slot %d", layer, p.Slot)
			}
		}
		cp := p
		if cp == nil {
			if cp = defaults[row.HubID]; cp != nil {
				if row.HubID == lead {
					row.Placement = "First populated discovery row"
				} else {
					row.Placement = "Placed after Continue Watching and the discovery lead"
				}
			}
		}
		pool = append(pool, candidate{row, cp, 0, explicitAlways})
	}
	// Explanation-only hidden rows are absent from Plex's candidate list.
	// Compute ranking from the eligible pool so their presence in a preview
	// cannot change the relative effect of owner priority weights.
	for i := range pool {
		c := &pool[i]
		c.rank = float64(len(pool) - i)
		if c.p != nil {
			if c.p.Priority > 0 {
				c.rank *= c.p.Priority
			}
			if c.p.Status == "always" {
				c.rank += 10000
			}
			if c.p.Slot > 0 && c.r.Layer == "override" {
				c.rank += 100000
			}
		}
	}
	sort.SliceStable(pool, func(i, j int) bool {
		return pool[i].rank > pool[j].rank
	})
	out := []core.HomeRow{}
	used := make([]bool, len(pool))
	needsExplicitAlways := func(c candidate, tally map[string]int) bool {
		if !c.explicitAlways || c.p == nil {
			return false
		}
		if c.p.MaxPerPage > 0 && tally[c.p.Row] >= c.p.MaxPerPage {
			return false
		}
		if c.p.Slot > 0 && tally[c.p.Row] >= 1 {
			return false
		}
		return true
	}
	remainingAlways := func() int {
		sim := make(map[string]int, len(counts))
		for k, n := range counts {
			sim[k] = n
		}
		rem := 0
		for i, c := range pool {
			if used[i] || !needsExplicitAlways(c, sim) {
				continue
			}
			sim[c.p.Row]++
			rem++
		}
		return rem
	}
	for slot := 1; slot <= maxRows || remainingAlways() > 0; slot++ {
		remAlways := remainingAlways()
		allowOptional := slot <= maxRows && len(out)+remAlways < maxRows
		pick := -1
		displacedPick := false
		deadline := 101
		for i, c := range pool {
			if used[i] {
				continue
			}
			must := needsExplicitAlways(c, counts)
			if !must && !allowOptional {
				continue
			}
			p := c.p
			if p != nil {
				if p.MaxPerPage > 0 && counts[p.Row] >= p.MaxPerPage {
					continue
				}
				if p.Slot > 0 {
					if !must && p.Slot != slot {
						continue
					}
					if must && p.Slot > slot {
						continue
					}
				}
				if p.MinSlot > slot {
					continue
				}
				if p.MaxSlot > 0 && p.MaxSlot < slot && !must {
					continue
				}
				if p.Slot == slot {
					pick = i
					break
				}
				if must && p.Slot > 0 && p.Slot < slot {
					if !displacedPick {
						pick = i
						displacedPick = true
					}
					continue
				}
				if !displacedPick && p.MaxSlot > 0 && p.MaxSlot < deadline {
					pick = i
					deadline = p.MaxSlot
					continue
				}
			}
			if pick < 0 {
				pick = i
			}
		}
		if pick < 0 {
			if remAlways == 0 {
				break
			}
			bestTarget := 1000
			for i, c := range pool {
				if used[i] || !needsExplicitAlways(c, counts) {
					continue
				}
				target := max(c.p.Slot, c.p.MinSlot)
				if target == 0 {
					target = slot
				}
				if pick < 0 || target < bestTarget {
					pick = i
					bestTarget = target
				}
			}
			if pick < 0 {
				break
			}
		}
		used[pick] = true
		c := pool[pick]
		c.r.Slot = slot
		if c.p != nil {
			counts[c.p.Row]++
		}
		out = append(out, c.r)
	}
	for i, c := range pool {
		if !used[i] {
			c.r.Hidden = true
			c.r.HiddenCode = "page_constraints"
			c.r.HiddenReason = "Page length, frequency cap or position constraints"
			hidden = append(hidden, c.r)
		}
	}
	return append(out, hidden...)
}

type draftLayoutKey struct{}

// PreviewLayout assembles an unsaved document without changing persisted state,
// memoized production layouts, impressions, or viewer overrides.
func (e *Engine) PreviewLayout(ctx context.Context, user *core.User, hc core.HubContext, section string, limit int, d core.HomeLayout) (*core.HomePreview, error) {
	ctx = core.WithCatalogViewer(ctx, user)
	d, err := normalizeHomeLayout(d)
	if err != nil {
		return nil, err
	}
	return e.PreviewHome(context.WithValue(ctx, draftLayoutKey{}, d), user, hc, section, limit)
}

// PreviewCandidateRows arranges pre-built candidate rows (such as a plugin
// library's Recommended shelves) under a draft or saved HomeLayout without
// mutating persisted state.
func (e *Engine) PreviewCandidateRows(ctx context.Context, user *core.User, hc core.HubContext, section string, defaultMaxRows int, d core.HomeLayout, rows []core.HomeRow) (*core.HomePreview, error) {
	ctx = core.WithCatalogViewer(ctx, user)
	d, err := normalizeHomeLayout(d)
	if err != nil {
		return nil, err
	}
	if section != "home" && !isLibrarySection(section) {
		return nil, &ValidationError{Problems: []Problem{{"section", "must be home or library:<id>"}}}
	}
	if hc.Now.IsZero() {
		hc.Now = e.now()
	}
	hc.Now = hc.Now.In(e.loc)
	hc.Daypart, hc.Weekend = core.DaypartAt(hc.Now), core.IsWeekend(hc.Now)
	if !validDevices[hc.Device] {
		return nil, &ValidationError{Problems: []Problem{{"device", "must be tv, mobile, web or desktop"}}}
	}
	uid := int64(0)
	if user != nil {
		uid = user.ID
	}
	if defaultMaxRows <= 0 {
		defaultMaxRows = e.opts.MaxSectionRows
	}
	arranged := arrangeLayoutFor(rows, d, uid, hc, section, defaultMaxRows)
	return &core.HomePreview{
		GeneratedAt: e.now(),
		Context:     hc,
		Rows:        arranged,
	}, nil
}

func (e *Engine) layoutForContext(ctx context.Context) core.HomeLayout {
	if d, ok := ctx.Value(draftLayoutKey{}).(core.HomeLayout); ok {
		return d
	}
	d, _ := e.HomeLayout(ctx)
	return d
}

// SetExtraHomeRows supplies the additional production rows for owner previews.
// Wire once during startup, before serving requests.
func (e *Engine) SetExtraHomeRows(fn func(context.Context, *core.User, core.HubContext, string, int) []core.Hub) {
	e.extraHomeRows = fn
}
func hubKind(id string) string {
	switch {
	case strings.Contains(id, "continue"), strings.Contains(id, "inprogress"):
		return KindContinue
	case strings.Contains(id, "recentlyadded"):
		return "recently-added"
	case strings.Contains(id, "playlist"):
		return "playlists"
	}
	// An engine or collection row from before a restart ("movie.cluster.…", "collection.…").
	if k := parseHubKind(id); k != "other" && (strings.HasPrefix(id, "home.") || strings.HasPrefix(id, "movie.") || strings.HasPrefix(id, "tv.") || strings.HasPrefix(id, "collection.")) {
		return k
	}
	return id
}
func rowsFromHubs(hubs []core.Hub) []core.HomeRow {
	rows := make([]core.HomeRow, 0, len(hubs))
	for _, h := range hubs {
		rows = append(rows, core.HomeRow{HubID: h.ID, Title: h.Title, Kind: hubKind(h.ID), Source: core.RowSourcePlex, Items: h.Items, Hub: h})
	}
	return rows
}

// FinalizeHome arranges the complete Plex row list after all producers ran.
// Unknown/custom hubs with no catalogue items are deliberately preserved.
func (e *Engine) FinalizeHome(ctx context.Context, user *core.User, hc core.HubContext, section string, hubs []core.Hub) ([]core.Hub, error) {
	ctx = core.WithCatalogViewer(ctx, user)
	rows := rowsFromHubs(hubs)
	rows = e.finalizeHomeRows(ctx, user, hc, section, rows)
	out := make([]core.Hub, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.Hub)
	}
	return out, nil
}

// finalizeHomeRows is shared by both wire-shaped and neutral adapters.
func (e *Engine) finalizeHomeRows(ctx context.Context, user *core.User, hc core.HubContext, section string, rows []core.HomeRow) []core.HomeRow {
	return e.finalizeHomeRowsWithDefault(ctx, user, hc, section, rows, 0)
}

func (e *Engine) finalizeHomeRowsWithDefault(ctx context.Context, user *core.User, hc core.HubContext, section string, rows []core.HomeRow, defaultMaxRows int) []core.HomeRow {
	ctx = core.WithCatalogViewer(ctx, user)
	if user == nil {
		user = &core.User{}
	}
	if hc.Now.IsZero() {
		hc.Now = e.now()
	}
	hc.Now = hc.Now.In(e.loc)
	target := e.layoutTarget(ctx, section, hc.SectionID)
	def := e.opts.MaxHomeRows
	if target != "" && target != "home" {
		def = e.opts.MaxSectionRows
	}
	if defaultMaxRows > 0 {
		def = defaultMaxRows
	}
	for i := range rows {
		e.hubMu.RLock()
		kind := e.hubKinds[rows[i].HubID]
		e.hubMu.RUnlock()
		if kind != "" {
			rows[i].Kind = kind
		}
	}
	// Recently Added is the catalogue's newest additions, not picks: keep
	// only titles this viewer could plausibly want (toppicks.go), unless an
	// active Always policy requires keeping any available additions.
	var taste *Taste
	if m := e.modelFor(ctx, user, false); m != nil {
		taste = m.taste
	}
	layoutDoc := e.layoutForContext(ctx)
	kept := rows[:0]
	for _, r := range rows {
		if r.Kind == KindRecentlyAdded {
			p, _ := effectiveHomePolicy(layoutDoc, user.ID, target, r)
			always := p != nil && p.Status == "always" && whenMatches(p.When, hc) && weekEligible(user.ID, p.Row, hc, p.MaxDaysPerWeek)
			curated := curateFeed(r, taste)
			if curated.Hidden {
				if always {
					if len(curated.Items) > 0 {
						curated.Hidden = false
						curated.HiddenCode = ""
						curated.HiddenReason = ""
						curated.Hub.Items = curated.Items
						kept = append(kept, curated)
					} else if rowHasItems(r) {
						kept = append(kept, r)
					}
				}
				continue
			}
			r = curated
			r.Hub.Items = curated.Items
		}
		kept = append(kept, r)
	}
	rows = kept
	out := []core.HomeRow{}
	for _, row := range arrangeLayoutFor(rows, layoutDoc, user.ID, hc, target, def) {
		if !row.Hidden {
			out = append(out, row)
		}
	}
	return out
}

// HomeRowBudget returns the effective maximum Home rows for user and context,
// honoring global, viewer, and device Home layout maxRows.
func (e *Engine) HomeRowBudget(ctx context.Context, user *core.User, hc core.HubContext) int {
	ctx = core.WithCatalogViewer(ctx, user)
	layout := e.layoutForContext(ctx)
	g, v := pagesForTarget(layout, userID(user), surfHome)
	maxRows := e.opts.MaxHomeRows
	for _, p := range []*core.LayoutPage{g, v} {
		if p != nil {
			if p.MaxRows > 0 {
				maxRows = p.MaxRows
			}
			if p.DeviceMaxRows[hc.Device] > 0 {
				maxRows = p.DeviceMaxRows[hc.Device]
			}
		}
	}
	if maxRows <= 0 {
		maxRows = 20
	}
	return maxRows
}

// HomeRowEnabled checks the owner's inclusion policy without applying slot or
// page-size constraints to a separately fetched Plex Continue Watching row.
func (e *Engine) HomeRowEnabled(ctx context.Context, user *core.User, hc core.HubContext, id string) bool {
	ctx = core.WithCatalogViewer(ctx, user)
	if user == nil {
		user = &core.User{}
	}
	if hc.Now.IsZero() {
		hc.Now = e.now()
	}
	hc.Now = hc.Now.In(e.loc)
	layout := e.layoutForContext(ctx)
	row := core.HomeRow{HubID: id, Kind: hubKind(id)}
	policy := matchingPolicy(pageFor(layout.Global, "home"), row)
	for _, viewer := range layout.Viewers {
		if viewer.UserID == user.ID {
			if override := matchingPolicy(pageFor(viewer.Pages, "home"), row); override != nil {
				policy = override
			}
		}
	}
	return policy == nil || (policy.Status != "off" && whenMatches(policy.When, hc) && weekEligible(user.ID, policy.Row, hc, policy.MaxDaysPerWeek))
}

func (e *Engine) FinalizeNeutralHome(ctx context.Context, user *core.User, situation core.HomeSituation, section string, candidates []core.NeutralHomeRow) ([]core.NeutralHomeRow, error) {
	return e.finalizeNeutralHome(ctx, user, situation, section, candidates, false)
}

// FinalizeNeutralHomeFill applies the owner rules to a minimal hero fill. Shelf
// sparsity and taste curation must not discard the one or two missing slots.
func (e *Engine) FinalizeNeutralHomeFill(ctx context.Context, user *core.User, situation core.HomeSituation, section string, candidates []core.NeutralHomeRow) ([]core.NeutralHomeRow, error) {
	return e.finalizeNeutralHome(ctx, user, situation, section, candidates, true)
}

func (e *Engine) finalizeNeutralHome(ctx context.Context, user *core.User, situation core.HomeSituation, section string, candidates []core.NeutralHomeRow, fill bool) ([]core.NeutralHomeRow, error) {
	ctx = core.WithCatalogViewer(ctx, user)
	hc := core.HubContext{Device: situation.Device, Daypart: situation.Daypart, Weekend: situation.Weekend, Now: situation.Now, SectionID: situation.LibraryID, Product: situation.Product, Platform: situation.Platform, SessionID: situation.SessionID, ClientID: situation.ClientID}
	rows := make([]core.HomeRow, 0, len(candidates))
	byID := make(map[string]core.NeutralHomeRow, len(candidates))
	for _, candidate := range candidates {
		kind := candidate.Kind
		if kind == "" {
			kind = hubKind(candidate.ID)
		}
		h := core.Hub{ID: candidate.ID, Title: candidate.Title, Type: candidate.Type, Items: candidate.Items, ItemCount: candidate.ItemCount, PreviewOnFocus: candidate.PreviewOnFocus, RankedTop10: candidate.RankedTop10}
		rows = append(rows, core.HomeRow{HubID: candidate.ID, Title: candidate.Title, Kind: kind, Source: core.RowSourcePrecomposed, Items: candidate.Items, Hub: h})
		byID[candidate.ID] = candidate
	}
	var arranged []core.HomeRow
	if fill {
		if user == nil {
			return nil, core.ErrUnauthorized
		}
		hc.Now = hc.Now.In(e.loc)
		// Fill is a band, so shelf-only default slots (Recently Added at
		// slot eight) do not apply. Explicit owner rules still do.
		layout := e.layoutForContext(ctx)
		target := e.layoutTarget(ctx, section, hc.SectionID)
		layout.Global = append([]core.LayoutPage(nil), layout.Global...)
		page := pageFor(layout.Global, target)
		if page == nil {
			layout.Global = append(layout.Global, core.LayoutPage{Section: target})
			page = &layout.Global[len(layout.Global)-1]
		}
		page.Rows = append([]core.RowPolicy(nil), page.Rows...)
		for _, row := range rows {
			if policy, _ := effectiveHomePolicy(layout, user.ID, target, row); policy == nil {
				page.Rows = append(page.Rows, core.RowPolicy{Row: row.HubID, Status: "eligible"})
			}
		}
		for _, row := range arrangeLayoutFor(rows, layout, user.ID, hc, target, e.opts.MaxHomeRows) {
			if !row.Hidden {
				arranged = append(arranged, row)
			}
		}
	} else {
		arranged = e.finalizeHomeRowsWithDefault(ctx, user, hc, section, rows, situation.DefaultMaxRows)
	}
	out := make([]core.NeutralHomeRow, 0, len(arranged))
	for _, row := range arranged {
		if candidate, ok := byID[row.HubID]; ok {
			candidate.PreviewOnFocus = row.Hub.PreviewOnFocus
			if row.Policy != nil {
				candidate.PreviewOnFocusPolicy = row.Policy.PreviewOnFocus
			}
			candidate.RankedTop10 = row.Hub.RankedTop10
			candidate.Always = row.Policy != nil && row.Policy.Status == "always"
			out = append(out, candidate)
		}
	}
	return out, nil
}

var _ core.NeutralHomeLayoutSource = (*Engine)(nil)

func (e *Engine) HomeCandidatesFor(ctx context.Context, u *core.User, hc core.HubContext, limit int) ([]core.Hub, error) {
	return toHubs(e.home(ctx, u, hc, "", limit, false).Rows), nil
}

// HomeCandidatesWithPolicy applies per-request visibility before ranking.
func (e *Engine) HomeCandidatesWithPolicy(ctx context.Context, u *core.User, hc core.HubContext, limit int, allow func(context.Context, *core.User, *core.Title) bool) ([]core.Hub, error) {
	if allow == nil {
		return nil, errors.New("personalize: missing visibility policy")
	}
	p := e.homeWithPolicy(ctx, u, hc, "", limit, false, allow)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return toHubs(p.Rows), nil
}

// SectionCandidatesWithPolicy applies viewer visibility before section ranking.
func (e *Engine) SectionCandidatesWithPolicy(ctx context.Context, u *core.User, section core.MediaType, hc core.HubContext, limit int, allow func(context.Context, *core.User, *core.Title) bool) ([]core.Hub, error) {
	if allow == nil {
		return nil, errors.New("personalize: missing visibility policy")
	}
	p := e.homeWithPolicy(ctx, u, hc, section, limit, false, allow)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return toHubs(p.Rows), nil
}

var _ core.PolicyHomeCandidateSource = (*Engine)(nil)

func (e *Engine) SectionCandidatesFor(ctx context.Context, u *core.User, t core.MediaType, hc core.HubContext, limit int) ([]core.Hub, error) {
	return toHubs(e.home(ctx, u, hc, t, limit, false).Rows), nil
}

// HubItemsFor resolves a specific hub by identifier from the viewer's warm
// model, the generic model, or owner collection rows when the active page no
// longer includes that row (e.g. a cold-start showcase row after the viewer's
// personal model finishes building, or a shelf dropped under a wider count).
func (e *Engine) HubItemsFor(ctx context.Context, u *core.User, hc core.HubContext, id string, limit int) (core.Hub, bool) {
	if limit <= 0 {
		limit = 12
	}
	if hc.Now.IsZero() {
		hc.Now = e.now().In(e.loc)
	}
	user := core.User{}
	if u != nil {
		user = *u
	}
	var section core.MediaType
	switch {
	case strings.HasPrefix(id, "movie."):
		section = core.TypeMovie
	case strings.HasPrefix(id, "tv."):
		section = core.TypeShow
	}
	if section != "" && hc.SectionID == 0 {
		hc.SectionID = e.firstSectionID(ctx, section)
	}
	layoutDoc := e.layoutForContext(ctx)
	owner, _ := e.ownerRows(ctx, &user, hc, section, layoutDoc, false)
	e.mu.Lock()
	m := e.models[user.ID]
	gm := e.models[genericID]
	e.mu.Unlock()
	models := []*userModel{}
	if m != nil {
		models = append(models, m)
	}
	if gm != nil && gm != m {
		models = append(models, gm)
	}
	if len(models) == 0 {
		if fm := e.modelFor(ctx, &user, false); fm != nil {
			models = append(models, fm)
		}
	}
	day := dayOf(hc.Now, e.loc)
	intent, _ := e.intentFor(user.ID, hc.Now)
	target := layoutTargetForContext(section, hc)
	tune := resolveTuning(layoutDoc, user.ID, target)
	allowed := e.allowedFor(&user)
	for _, mod := range models {
		a := &assembly{
			visit:   int64(day),
			layout:  &layoutDoc,
			model:   mod,
			user:    user,
			hc:      hc,
			section: section,
			limit:   limit,
			owner:   owner,
			allowed: allowed,
			day:     day,
			intent:  intent,
			tune:    &tune,
		}
		cands := append(append([]*candRow{}, mod.rows...), owner...)
		for _, c := range cands {
			if a.hubID(c) != id {
				continue
			}
			items := a.contextItems(c)
			if len(items) == 0 {
				continue
			}
			if section != "" && hc.SectionID > 0 && e.opts.LibraryMembers != nil {
				keys := make([]core.RatingKey, len(items))
				for i, it := range items {
					keys[i] = it.t.Key
				}
				members, _, scoped, err := e.opts.LibraryMembers(ctx, core.LibraryID(hc.SectionID), keys)
				if err != nil {
					return core.Hub{}, false
				}
				if scoped {
					filtered := items[:0]
					for _, it := range items {
						if members[it.t.Key] {
							filtered = append(filtered, it)
						}
					}
					items = filtered
					if len(items) == 0 {
						continue
					}
				}
			}
			if e.opts.FilterAvailable != nil {
				pool := make([]core.Title, len(items))
				for i, it := range items {
					pool[i] = it.t
				}
				avail := map[core.RatingKey]bool{}
				for _, t := range e.opts.FilterAvailable(ctx, pool) {
					avail[t.Key] = true
				}
				filtered := items[:0]
				for _, it := range items {
					if avail[it.t.Key] {
						filtered = append(filtered, it)
					}
				}
				items = filtered
				if len(items) == 0 {
					continue
				}
			}
			cap := a.rowCap(c)
			more := len(items) > cap
			if more {
				items = items[:cap]
			}
			hr := a.homeRow(&rankedRow{c: c, items: items, hubID: id}, items, more)
			return hr.Hub, true
		}
	}
	return core.Hub{}, false
}
func (e *Engine) LayoutDefaults() map[string]int {
	return map[string]int{"home": e.opts.MaxHomeRows, "library": e.opts.MaxSectionRows}
}

func effectiveHomePolicy(d core.HomeLayout, user int64, target string, row core.HomeRow) (*core.RowPolicy, string) {
	g, v := pagesForTarget(d, user, target)
	if p := matchingPolicy(v, row); p != nil {
		return p, "override"
	}
	return matchingPolicy(g, row), "global"
}

// selectedCollections lists the collections the layout places on a page for
// this viewer and moment (When and days-per-week applied).
func selectedCollections(d core.HomeLayout, user int64, target string, hc core.HubContext) []string {
	return layoutCollectionsFor(d, user, target, &hc)
}

// placedCollections lists every collection the layout places on a page for
// this viewer, whatever the hour, daypart or day. Owner rows are fetched for
// this set so a new daypart never finds its rows unfetched.
func placedCollections(d core.HomeLayout, user int64, target string) []string {
	return layoutCollectionsFor(d, user, target, nil)
}

func layoutCollectionsFor(d core.HomeLayout, user int64, target string, hc *core.HubContext) []string {
	ids := []string{}
	seen := map[string]bool{}
	g, v := pagesForTarget(d, user, target)
	for _, page := range []*core.LayoutPage{g, v} {
		if page == nil {
			continue
		}
		for _, r := range page.Rows {
			var key string
			var probe core.HomeRow
			if strings.HasPrefix(r.Row, "plugin:") {
				if strings.ContainsAny(r.Row, "*?[") {
					continue
				}
				key = r.Row
				probe = core.HomeRow{HubID: r.Row, Kind: r.Row}
			} else {
				key = strings.TrimPrefix(strings.TrimPrefix(r.Row, "collection:"), "collection.")
				if key == r.Row || strings.ContainsAny(key, "*?[") {
					continue
				}
				probe = core.HomeRow{HubID: "collection." + key, Kind: "collection:" + key}
			}
			p, _ := effectiveHomePolicy(d, user, target, probe)
			if p == nil || p.Status == "off" || seen[key] {
				continue
			}
			if hc == nil || whenMatches(p.When, *hc) && weekEligible(user, p.Row, *hc, p.MaxDaysPerWeek) {
				seen[key] = true
				ids = append(ids, key)
			}
		}
	}
	return ids
}
