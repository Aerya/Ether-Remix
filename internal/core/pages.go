package core

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Recommendations pages (design: rows-redesign). A page is a place that
// shows rows: Home, or one rows tab of a library (a TabRecommended tab). A
// row is a source (what fills it), a scope (which libraries it draws from),
// a look and a placement. Collections are content only; where they show is
// a row on a page.

// PageKey names a page: "home", or "library:<id>/<tab>".
type PageKey string

// PageHome is every app's Home.
const PageHome PageKey = "home"

// LibraryPage returns the key of a library's rows tab.
func LibraryPage(id LibraryID, tab string) PageKey {
	return PageKey("library:" + strconv.FormatInt(int64(id), 10) + "/" + tab)
}

var libraryPageKey = regexp.MustCompile(`^library:([1-9][0-9]*)/([a-z0-9][a-z0-9_-]{0,63})$`)

// Library returns the library id and tab key of a library page.
func (k PageKey) Library() (LibraryID, string, bool) {
	m := libraryPageKey.FindStringSubmatch(string(k))
	if m == nil {
		return 0, "", false
	}
	id, err := strconv.ParseInt(m[1], 10, 64)
	if err != nil {
		return 0, "", false
	}
	return LibraryID(id), m[2], true
}

// Valid reports whether k is "home" or a well-formed library page key.
func (k PageKey) Valid() bool {
	if k == PageHome {
		return true
	}
	_, _, ok := k.Library()
	return ok
}

// Page modes.
const (
	// PageCurated shows exactly the page's rows, in order; Ether adds nothing.
	PageCurated = "curated"
	// PagePersonalized keeps pinned and shown rows and lets Ether fill the
	// rest, up to the page length, from Auto rows and (with Picks) its own.
	PagePersonalized = "personalized"
)

// RowPage is one page's rows and settings, as the owner saves it.
type RowPage struct {
	Key  PageKey `json:"key"`
	Mode string  `json:"mode"`
	// Length is how many rows a Personalized page grows to.
	Length PageLength `json:"length"`
	// Picks lets Ether add rows the owner hasn't listed (Personalized).
	Picks bool `json:"picks"`
	// Tuning shapes how Ether chooses rows and titles (Personalized).
	Tuning *PageTuning `json:"tuning,omitempty"`
	// SkipHome keeps titles the viewer's Home leads with off a library
	// page. Nil means on.
	SkipHome *bool `json:"skipHome,omitempty"`
	// Rows are in the owner's order.
	Rows []Row `json:"rows"`
	// Variants are "changes just for <viewer>".
	Variants []PageVariant `json:"variants,omitempty"`
	// Offered lists plugin sources (surfaces, a new plugin library's shelves)
	// already added once, so an owner's later Hide or removal sticks.
	Offered []string `json:"offered,omitempty"`
	// Revision increments on every save (optimistic concurrency).
	Revision  int64     `json:"revision"`
	UpdatedAt time.Time `json:"updatedAt,omitempty"`
}

// PageLength is a Personalized page's row target.
type PageLength struct {
	// Rows is the target; 0 is Ether's default.
	Rows int `json:"rows,omitempty"`
	// ByDevice overrides Rows per device class (tv, mobile, web, desktop).
	ByDevice map[string]int `json:"byDevice,omitempty"`
}

// For returns the target for a device, or 0 for the default.
func (l PageLength) For(device string) int {
	if n := l.ByDevice[device]; n > 0 {
		return n
	}
	return l.Rows
}

// PageTuning is the owner's three dials, each 0..1; nil fields are 0.5.
type PageTuning struct {
	// Discovery: 0 sticks to proven favourites, 1 explores new territory.
	Discovery *float64 `json:"discovery,omitempty"`
	// Freshness: 0 favours timeless picks, 1 favours new and trending.
	Freshness *float64 `json:"freshness,omitempty"`
	// Variety: 0 keeps rows focused, 1 spreads across genres, eras and moods.
	Variety *float64 `json:"variety,omitempty"`
}

// Row is one line on a page.
type Row struct {
	// ID is server-assigned ("r" + 8 base-32 characters) and stable.
	ID     string    `json:"id"`
	Source RowSource `json:"source"`
	Scope  RowScope  `json:"scope"`
	// Name is the owner's title; "{library}" becomes each library's name.
	// Empty means the source's own title.
	Name      string       `json:"name,omitempty"`
	Look      RowLook      `json:"look"`
	Placement RowPlacement `json:"placement"`
}

// RowSource is what fills a row: a registered source kind and its
// parameters (validated against the kind's declared parameters).
type RowSource struct {
	Kind   string            `json:"kind"`
	Params map[string]string `json:"params,omitempty"`
}

// Param returns a parameter value, or def when unset.
func (s RowSource) Param(key, def string) string {
	if v := s.Params[key]; v != "" {
		return v
	}
	return def
}

// MaxRowsPerLibraryRow caps a "one row per library" fan-out (owner decision).
const MaxRowsPerLibraryRow = 6

// RowScope is the libraries a row draws from.
type RowScope struct {
	// Libraries empty means the page's library on a library page, or every
	// library the viewer can see that the source supports on Home.
	Libraries []LibraryID `json:"libraries,omitempty"`
	// PerLibrary makes one row per library (at most MaxRowsPerLibraryRow),
	// kept together, instead of one combined row.
	PerLibrary bool `json:"perLibrary,omitempty"`
}

// Row look values.
const (
	RowStyleStandard = "standard"
	RowStyleHero     = "hero"
	RowStyleWide     = "wide"
	RowStyleTiles    = "tiles"
	RowStyleTracks   = "tracks"

	CardRound = "round"
)

// RowLook is how a row draws. Empty values follow the source and library.
type RowLook struct {
	// Style is "" (the source's default), standard, hero, wide, tiles or tracks.
	Style string `json:"style,omitempty"`
	// Card is "" (follows the library), poster, landscape, square or round.
	Card        string `json:"card,omitempty"`
	HideWatched bool   `json:"hideWatched,omitempty"`
	// Numbered and Trailer: nil follows the source.
	Numbered *bool `json:"numbered,omitempty"`
	Trailer  *bool `json:"trailer,omitempty"`
	// KeepWhole keeps titles other rows on the page already show.
	KeepWhole bool `json:"keepWhole,omitempty"`
}

// ValidRowStyle reports whether s is a row style an owner may choose.
func ValidRowStyle(s string) bool {
	switch s {
	case "", RowStyleStandard, RowStyleHero, RowStyleWide, RowStyleTiles, RowStyleTracks:
		return true
	}
	return false
}

// Placement modes.
const (
	PlacementPinned = "pinned"
	PlacementShow   = "show"
	PlacementAuto   = "auto"
	PlacementHide   = "hide"
)

// RowPlacement is where, when and for whom a row shows.
type RowPlacement struct {
	// Mode is pinned, show, auto or hide. On a Curated page only show and
	// hide apply (the list order is the order).
	Mode string `json:"mode"`
	// Position is the 1-based slot of a pinned row.
	Position int       `json:"position,omitempty"`
	When     *Schedule `json:"when,omitempty"`
	// Devices limits the row to device classes; empty is every device.
	Devices  []string `json:"devices,omitempty"`
	Audience Audience `json:"audience"`
	// More options (auto rows on Personalized pages).
	NotAbove    int `json:"notAbove,omitempty"`
	NotBelow    int `json:"notBelow,omitempty"`
	DaysPerWeek int `json:"daysPerWeek,omitempty"`
	// Priority is -1 (lower), 0 or +1 (higher).
	Priority int `json:"priority,omitempty"`
}

// Audience modes.
const (
	AudienceEveryone = "everyone"
	AudienceOnly     = "only"
	AudienceExcept   = "except"
)

// Audience is who sees a row.
type Audience struct {
	Mode    string  `json:"mode,omitempty"`
	Viewers []int64 `json:"viewers,omitempty"`
}

// Includes reports whether the viewer sees the row (0 is the typical viewer,
// who is in "everyone" and "except" audiences only).
func (a Audience) Includes(viewer int64) bool {
	switch a.Mode {
	case AudienceOnly:
		return slices.Contains(a.Viewers, viewer)
	case AudienceExcept:
		return !slices.Contains(a.Viewers, viewer)
	}
	return true
}

// PageVariant is "changes just for <viewer>": Look and Placement overrides
// of rows on the page, and optionally the viewer's own mode and length.
type PageVariant struct {
	Viewer int64         `json:"viewer"`
	Mode   string        `json:"mode,omitempty"`
	Length *PageLength   `json:"length,omitempty"`
	Rows   []RowOverride `json:"rows"`
}

// RowOverride replaces a row's Look and/or Placement for one viewer.
type RowOverride struct {
	Row       string        `json:"row"`
	Look      *RowLook      `json:"look,omitempty"`
	Placement *RowPlacement `json:"placement,omitempty"`
}

// VariantFor returns the viewer's variant, or nil.
func (p *RowPage) VariantFor(viewer int64) *PageVariant {
	if p == nil || viewer == 0 {
		return nil
	}
	for i := range p.Variants {
		if p.Variants[i].Viewer == viewer {
			return &p.Variants[i]
		}
	}
	return nil
}

// RowByID returns the page row with id, or nil.
func (p *RowPage) RowByID(id string) *Row {
	if p == nil {
		return nil
	}
	for i := range p.Rows {
		if p.Rows[i].ID == id {
			return &p.Rows[i]
		}
	}
	return nil
}

var rowIDPattern = regexp.MustCompile(`^r[a-z0-9]{8}$`)

// ValidRowID reports whether id is a server-assigned row id.
func ValidRowID(id string) bool { return rowIDPattern.MatchString(id) }

// ValidateStructure checks a page's shape: modes, positions, looks,
// schedules, devices and audiences. Source kinds and parameters are checked
// by the source registry (internal/personalize).
func (p *RowPage) ValidateStructure() error {
	if !p.Key.Valid() {
		return fmt.Errorf("key: %q is not home or library:<id>/<tab>", p.Key)
	}
	if p.Mode != PageCurated && p.Mode != PagePersonalized {
		return fmt.Errorf("mode: must be curated or personalized")
	}
	if p.Length.Rows < 0 || p.Length.Rows > 50 {
		return fmt.Errorf("length: must be 0 to 50 rows")
	}
	for d, n := range p.Length.ByDevice {
		if !validDeviceClass(d) || n < 0 || n > 50 {
			return fmt.Errorf("length: %q must be tv, mobile, web or desktop with 0 to 50 rows", d)
		}
	}
	if err := p.Tuning.validate(); err != nil {
		return err
	}
	seen := map[string]bool{}
	for i, r := range p.Rows {
		if !ValidRowID(r.ID) || seen[r.ID] {
			return fmt.Errorf("rows[%d]: id %q is missing, malformed or repeated", i, r.ID)
		}
		seen[r.ID] = true
		if strings.TrimSpace(r.Source.Kind) == "" {
			return fmt.Errorf("rows[%d]: source is required", i)
		}
		if len(r.Name) > 80 {
			return fmt.Errorf("rows[%d]: name is longer than 80 characters", i)
		}
		if len(r.Scope.Libraries) > 50 {
			return fmt.Errorf("rows[%d]: too many libraries", i)
		}
		if err := r.Look.validate(); err != nil {
			return fmt.Errorf("rows[%d]: %w", i, err)
		}
		if err := r.Placement.validate(p.Mode); err != nil {
			return fmt.Errorf("rows[%d]: %w", i, err)
		}
	}
	viewers := map[int64]bool{}
	for i, v := range p.Variants {
		if v.Viewer <= 0 || viewers[v.Viewer] {
			return fmt.Errorf("variants[%d]: viewer is missing or repeated", i)
		}
		viewers[v.Viewer] = true
		if v.Mode != "" && v.Mode != PageCurated && v.Mode != PagePersonalized {
			return fmt.Errorf("variants[%d]: mode must be curated or personalized", i)
		}
		mode := p.Mode
		if v.Mode != "" {
			mode = v.Mode
		}
		for j, o := range v.Rows {
			if !seen[o.Row] {
				return fmt.Errorf("variants[%d].rows[%d]: row %q is not on the page", i, j, o.Row)
			}
			if o.Look != nil {
				if err := o.Look.validate(); err != nil {
					return fmt.Errorf("variants[%d].rows[%d]: %w", i, j, err)
				}
			}
			if o.Placement != nil {
				if err := o.Placement.validate(mode); err != nil {
					return fmt.Errorf("variants[%d].rows[%d]: %w", i, j, err)
				}
			}
		}
	}
	return nil
}

func (t *PageTuning) validate() error {
	if t == nil {
		return nil
	}
	for _, v := range []*float64{t.Discovery, t.Freshness, t.Variety} {
		if v != nil && (*v < 0 || *v > 1) {
			return fmt.Errorf("tuning: dials must be 0 to 1")
		}
	}
	return nil
}

func (l RowLook) validate() error {
	if !ValidRowStyle(l.Style) {
		return fmt.Errorf("look: style must be standard, hero, wide, tiles or tracks")
	}
	switch l.Card {
	case "", CardPoster, CardLandscape, CardSquare, CardRound:
	default:
		return fmt.Errorf("look: card must be poster, landscape, square or round")
	}
	return nil
}

func (pl RowPlacement) validate(mode string) error {
	switch pl.Mode {
	// A Curated page keeps pins at their position and shows Auto rows in
	// list order, so switching modes never rewrites a row's placement.
	case PlacementShow, PlacementHide, PlacementPinned, PlacementAuto:
	default:
		return fmt.Errorf("placement: mode must be pinned, show, auto or hide")
	}
	if pl.Mode == PlacementPinned && (pl.Position < 1 || pl.Position > 50) {
		return fmt.Errorf("placement: a pinned row needs a position from 1 to 50")
	}
	if pl.Mode != PlacementPinned && pl.Position != 0 {
		return fmt.Errorf("placement: only a pinned row has a position")
	}
	if err := pl.When.Validate(); err != nil {
		return fmt.Errorf("when: %w", err)
	}
	for _, d := range pl.Devices {
		if !validDeviceClass(d) {
			return fmt.Errorf("devices: %q is not tv, mobile, web or desktop", d)
		}
	}
	switch pl.Audience.Mode {
	case "", AudienceEveryone:
		if len(pl.Audience.Viewers) > 0 {
			return fmt.Errorf("audience: everyone takes no viewers")
		}
	case AudienceOnly, AudienceExcept:
		if len(pl.Audience.Viewers) == 0 {
			return fmt.Errorf("audience: choose at least one viewer")
		}
	default:
		return fmt.Errorf("audience: mode must be everyone, only or except")
	}
	if pl.NotAbove < 0 || pl.NotBelow < 0 || pl.NotAbove > 50 || pl.NotBelow > 50 || (pl.NotAbove > 0 && pl.NotBelow > 0 && pl.NotBelow < pl.NotAbove) {
		return fmt.Errorf("placement: never above / below positions don't fit together")
	}
	if pl.DaysPerWeek < 0 || pl.DaysPerWeek > 6 {
		return fmt.Errorf("placement: days a week must be 1 to 6")
	}
	if pl.Priority < -1 || pl.Priority > 1 {
		return fmt.Errorf("placement: priority must be lower, normal or higher")
	}
	return nil
}

func validDeviceClass(d string) bool {
	switch d {
	case DeviceTV, DeviceMobile, DeviceWeb, DeviceDesktop:
		return true
	}
	return false
}

// RowLayer says which layer a served row's placement came from (previews).
const (
	RowLayerEveryone = "everyone"
	RowLayerViewer   = "viewer"
	RowLayerEther    = "ether"
)

// NormalizePins keeps pinned positions unique: two rows that show together
// (same audience, schedule and devices) never hold the same position. A
// collision moves the later row (by position, then list order) down one,
// and the rows after it follow, as dropping a row into a list does; a pin
// pushed past 50 becomes Show. Rows with different audiences or schedules
// may share a position (only one of them shows at a time for most viewers).
// A viewer's overrides are kept unique among themselves the same way.
func (p *RowPage) NormalizePins() {
	type pin struct {
		pl    *RowPlacement
		order int
	}
	settle := func(pins []pin) {
		groups := map[string][]pin{}
		var keys []string
		for _, x := range pins {
			a, d := x.pl.Audience, x.pl.Devices
			if a.Mode == "" {
				a.Mode = AudienceEveryone
			}
			if len(a.Viewers) == 0 {
				a.Viewers = nil
			}
			if len(d) == 0 {
				d = nil
			}
			sig, _ := json.Marshal(struct {
				A Audience
				W *Schedule
				D []string
			}{a, x.pl.When, d})
			k := string(sig)
			if _, ok := groups[k]; !ok {
				keys = append(keys, k)
			}
			groups[k] = append(groups[k], x)
		}
		for _, k := range keys {
			g := groups[k]
			slices.SortStableFunc(g, func(a, b pin) int {
				if a.pl.Position != b.pl.Position {
					return a.pl.Position - b.pl.Position
				}
				return a.order - b.order
			})
			last := 0
			for _, x := range g {
				if x.pl.Position <= last {
					if last >= 50 {
						x.pl.Mode, x.pl.Position = PlacementShow, 0
						continue
					}
					x.pl.Position = last + 1
				}
				last = x.pl.Position
			}
		}
	}
	var base []pin
	for i := range p.Rows {
		if pl := &p.Rows[i].Placement; pl.Mode == PlacementPinned && pl.Position > 0 {
			base = append(base, pin{pl, i})
		}
	}
	settle(base)
	order := map[string]int{}
	for i, r := range p.Rows {
		order[r.ID] = i
	}
	for v := range p.Variants {
		var own []pin
		for j := range p.Variants[v].Rows {
			if pl := p.Variants[v].Rows[j].Placement; pl != nil && pl.Mode == PlacementPinned && pl.Position > 0 {
				own = append(own, pin{pl, order[p.Variants[v].Rows[j].Row]})
			}
		}
		settle(own)
	}
}
