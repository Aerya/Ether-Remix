package plugins

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

const (
	// MaxPluginScreens bounds screens declared by one plugin.
	MaxPluginScreens = 8
)

var screenSlugRe = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)

var validScreenFrontends = map[string]bool{
	"vela": true,
}

// ScreenMeta declares one top-level screen a plugin draws from documents
// (docs/plugin-screens.md).
type ScreenMeta struct {
	ID        string   `json:"id"`
	Title     string   `json:"title"`
	Icon      string   `json:"icon,omitempty"`
	Frontends []string `json:"frontends,omitempty"`
	// Sections is the 0.3.19 fixed-section form, refused since documents
	// replaced it; kept only to say so.
	Sections json.RawMessage `json:"sections,omitempty"`

	// Pages are the screen's documents by page id ("index" is the landing
	// page); each names a package file or variants for older renderers.
	Pages map[string]DocumentPaths `json:"pages,omitempty"`
	// Placement is "navigation" (a top-level section, the default) or
	// "hidden" (reachable only by navigate).
	Placement string `json:"placement,omitempty"`
	// ContentRating hides the screen from viewers whose maximum content
	// rating is lower.
	ContentRating string `json:"contentRating,omitempty"`
	// Default suggests the owner's per-app switch; omitted means shown.
	Default *bool `json:"default,omitempty"`
	// Actions are the plugin-handled actions the screen's documents call.
	Actions map[string]ScreenAction `json:"actions,omitempty"`
}

// DocumentPaths names one document file, or variants of it for older
// renderers, in the plugin package. A single string is accepted.
type DocumentPaths []string

// UnmarshalJSON accepts a path or a list of paths.
func (d *DocumentPaths) UnmarshalJSON(b []byte) error {
	var one string
	if err := json.Unmarshal(b, &one); err == nil {
		*d = DocumentPaths{one}
		return nil
	}
	var many []string
	if err := json.Unmarshal(b, &many); err != nil {
		return fmt.Errorf("must be a file path or a list of file paths")
	}
	*d = many
	return nil
}

// ScreenAction declares one plugin-handled action a document may call.
type ScreenAction struct {
	Args    map[string]ScreenActionArg `json:"args,omitempty"`
	Confirm string                     `json:"confirm,omitempty"`
	Rate    string                     `json:"rate,omitempty"` // normal|low
}

// ScreenActionArg is one typed action argument.
type ScreenActionArg struct {
	Type     string   `json:"type"` // string|int|bool|date|ref|strings
	Max      int      `json:"max,omitempty"`
	Min      int      `json:"min,omitempty"`
	Enum     []string `json:"enum,omitempty"`
	Required bool     `json:"required,omitempty"`
}

// SurfaceMeta declares a document drawn in a host slot (a player overlay,
// a toast, a Home row or a title panel).
type SurfaceMeta struct {
	ID            string          `json:"id"`
	Presentation  string          `json:"presentation"` // playerOverlay|toast|homeRow|titlePanel
	Title         string          `json:"title"`
	Anchor        string          `json:"anchor,omitempty"`     // playerOverlay: topLeft|topRight|bottom
	Visibility    string          `json:"visibility,omitempty"` // playerOverlay: always|chrome|toggle
	Setting       *SurfaceSetting `json:"setting,omitempty"`
	When          *SurfaceWhen    `json:"when,omitempty"`
	TTL           int             `json:"ttl,omitempty"` // toast: seconds, 8..30
	ContentRating string          `json:"contentRating,omitempty"`
	Default       *bool           `json:"default,omitempty"`
	Document      DocumentPaths   `json:"document"`
	Frontends     []string        `json:"frontends,omitempty"`
}

// SurfaceSetting is a player overlay's switch in the player menu.
type SurfaceSetting struct {
	ID      string `json:"id"`
	Label   string `json:"label"`
	Default bool   `json:"default"`
}

// SurfaceWhen limits where a surface applies; it may only name the plugin's
// own feeds and kinds.
type SurfaceWhen struct {
	Event     *SurfaceWhenEvent `json:"event,omitempty"`
	Kind      string            `json:"kind,omitempty"` // channel|programme|title
	TitleKind string            `json:"titleKind,omitempty"`
}

// SurfaceWhenEvent matches the live event on air.
type SurfaceWhenEvent struct {
	Feed string `json:"feed"`
}

const (
	// MaxScreenPages bounds the pages of one screen.
	MaxScreenPages = 8
	// MaxDocumentVariants bounds the renderer variants of one document.
	MaxDocumentVariants = 4
	// MaxScreenActions bounds the plugin actions of one screen.
	MaxScreenActions = 32
	// MaxScreenActionArgs bounds one action's arguments.
	MaxScreenActionArgs = 8
	// MaxPluginSurfaces bounds the surfaces of one plugin.
	MaxPluginSurfaces = 8
)

var screenDocPathRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]{0,127}\.json$`)

var actionArgTypes = map[string]bool{"string": true, "int": true, "bool": true, "date": true, "ref": true, "strings": true}

func validateDocumentPaths(field string, paths DocumentPaths, add func(string, string)) {
	if len(paths) == 0 || len(paths) > MaxDocumentVariants {
		add(field, fmt.Sprintf("must name 1 to %d document files", MaxDocumentVariants))
		return
	}
	seen := map[string]bool{}
	for i, p := range paths {
		if !screenDocPathRe.MatchString(p) || strings.Contains(p, "..") || seen[p] {
			add(fmt.Sprintf("%s[%d]", field, i), "must be a unique .json file path inside the package")
		}
		seen[p] = true
	}
}

func validateScreenActions(base string, actions map[string]ScreenAction, add func(string, string), label func(string, string, int)) {
	if len(actions) > MaxScreenActions {
		add(base, fmt.Sprintf("must declare at most %d actions", MaxScreenActions))
	}
	for id, act := range actions {
		ab := base + "." + id
		if !screenSlugRe.MatchString(id) || strings.HasPrefix(id, "ether") {
			add(ab, "action ids are slugs matching ^[a-z][a-z0-9-]{0,31}$ and may not start with ether")
		}
		if act.Confirm != "" {
			label(ab+".confirm", act.Confirm, 120)
		}
		if act.Rate != "" && act.Rate != "normal" && act.Rate != "low" {
			add(ab+".rate", "must be normal or low")
		}
		if len(act.Args) > MaxScreenActionArgs {
			add(ab+".args", fmt.Sprintf("must declare at most %d arguments", MaxScreenActionArgs))
		}
		for name, arg := range act.Args {
			if !keyRe.MatchString(name) {
				add(ab+".args."+name, "must be a simple key")
			}
			if !actionArgTypes[arg.Type] {
				add(ab+".args."+name+".type", "must be string, int, bool, date, ref or strings")
			}
			if arg.Max < 0 || arg.Max > 512 {
				add(ab+".args."+name+".max", "must be between 0 and 512")
			}
			if len(arg.Enum) > 50 {
				add(ab+".args."+name+".enum", "must list at most 50 values")
			}
		}
	}
}

// ScreensUseDocuments reports whether m contributes any document-backed
// screen or surface.
func ScreensUseDocuments(m Manifest) bool {
	if m.Ether == nil {
		return false
	}
	for _, sc := range m.Ether.Contributes.Screens {
		if len(sc.Pages) > 0 {
			return true
		}
	}
	return len(m.Ether.Contributes.Surfaces) > 0
}

func screensPermissionLine(m Manifest) string {
	// Each screen already has its own "Adds a screen" line; this one is
	// for what plugins draw inside Ether's own pages.
	if m.Ether != nil && len(m.Ether.Contributes.Surfaces) > 0 {
		return "Adds overlays, alerts or rows to your apps"
	}
	return "Draws its screens in your apps"
}

func validateSurfaces(m Manifest, add func(string, string), label func(string, string, int)) {
	surfaces := m.Ether.Contributes.Surfaces
	if len(surfaces) == 0 {
		return
	}
	if !m.HasCapability(CapScreens) {
		add("ether.contributes.surfaces", "needs the screens capability")
	}
	if len(surfaces) > MaxPluginSurfaces {
		add("ether.contributes.surfaces", fmt.Sprintf("must declare at most %d surfaces", MaxPluginSurfaces))
	}
	feeds := map[string]bool{}
	for _, f := range m.Ether.Contributes.LiveFeeds {
		feeds[f.ID] = true
	}
	kinds := map[string]bool{}
	for _, k := range m.Ether.Contributes.MediaKinds {
		kinds[k.Key] = true
	}
	seen := map[string]bool{}
	settings := map[string]bool{}
	count := map[string]int{}
	for i, sf := range surfaces {
		base := fmt.Sprintf("ether.contributes.surfaces[%d]", i)
		if !screenSlugRe.MatchString(sf.ID) || seen[sf.ID] {
			add(base+".id", "must be a unique slug matching ^[a-z][a-z0-9-]{0,31}$")
		}
		seen[sf.ID] = true
		label(base+".title", sf.Title, 40)
		count[sf.Presentation]++
		overlay := sf.Presentation == "playerOverlay"
		switch sf.Presentation {
		case "playerOverlay", "toast", "homeRow", "titlePanel":
		default:
			add(base+".presentation", "must be playerOverlay, toast, homeRow or titlePanel")
		}
		if overlay {
			switch sf.Anchor {
			case "topLeft", "topRight", "bottom":
			default:
				add(base+".anchor", "must be topLeft, topRight or bottom")
			}
			switch sf.Visibility {
			case "", "always", "chrome", "toggle":
			default:
				add(base+".visibility", "must be always, chrome or toggle")
			}
			if sf.Setting == nil {
				add(base+".setting", "every player overlay has a player-menu switch: declare {id, label, default}")
			}
		} else {
			if sf.Anchor != "" {
				add(base+".anchor", "is only for player overlays")
			}
			if sf.Visibility != "" {
				add(base+".visibility", "is only for player overlays")
			}
			if sf.Setting != nil {
				add(base+".setting", "is only for player overlays")
			}
		}
		if sf.Setting != nil {
			if !screenSlugRe.MatchString(sf.Setting.ID) || settings[sf.Setting.ID] {
				add(base+".setting.id", "must be a unique slug matching ^[a-z][a-z0-9-]{0,31}$")
			}
			settings[sf.Setting.ID] = true
			label(base+".setting.label", sf.Setting.Label, 40)
		}
		if sf.TTL != 0 && (sf.Presentation != "toast" || sf.TTL < 8 || sf.TTL > 30) {
			add(base+".ttl", "is a toast's seconds on screen, 8 to 30")
		}
		if w := sf.When; w != nil {
			if w.Event != nil && !feeds[w.Event.Feed] {
				add(base+".when.event.feed", "must be one of this plugin's live feeds")
			}
			switch w.Kind {
			case "", "channel", "programme", "title":
			default:
				add(base+".when.kind", "must be channel, programme or title")
			}
			if w.TitleKind != "" && !kinds[w.TitleKind] {
				add(base+".when.titleKind", "must be one of this plugin's media kinds")
			}
		}
		if sf.Presentation == "titlePanel" && (sf.When == nil || sf.When.TitleKind == "") {
			add(base+".when.titleKind", "a title panel names the media kind whose titles it appears on")
		}
		for j, fe := range sf.Frontends {
			if !validScreenFrontends[fe] {
				add(fmt.Sprintf("%s.frontends[%d]", base, j), "must be a supported frontend such as vela")
			}
		}
		validateDocumentPaths(base+".document", sf.Document, add)
	}
	if count["homeRow"] > 1 {
		add("ether.contributes.surfaces", "a plugin contributes at most one homeRow surface")
	}
	if count["toast"] > 1 {
		add("ether.contributes.surfaces", "a plugin contributes at most one toast surface")
	}
}

func validateScreens(m Manifest, add func(string, string), label func(string, string, int)) {
	screens := m.Ether.Contributes.Screens
	if len(screens) == 0 {
		return
	}
	if len(screens) > MaxPluginScreens {
		add("ether.contributes.screens", fmt.Sprintf("must declare at most %d screens", MaxPluginScreens))
	}
	seenScreens := map[string]bool{}
	for i, sc := range screens {
		base := fmt.Sprintf("ether.contributes.screens[%d]", i)
		if !screenSlugRe.MatchString(sc.ID) || seenScreens[sc.ID] {
			add(base+".id", "must be a unique slug matching ^[a-z][a-z0-9-]{0,31}$")
		}
		seenScreens[sc.ID] = true
		label(base+".title", sc.Title, 40)
		if sc.Icon != "" && !screenSlugRe.MatchString(sc.Icon) {
			add(base+".icon", "must be a slug matching ^[a-z][a-z0-9-]{0,31}$")
		}
		seenFrontends := map[string]bool{}
		for j, fe := range sc.Frontends {
			if !validScreenFrontends[fe] || seenFrontends[fe] {
				add(fmt.Sprintf("%s.frontends[%d]", base, j), "must be a unique supported frontend such as vela")
			}
			seenFrontends[fe] = true
		}
		if len(sc.Sections) > 0 {
			add(base+".sections", "fixed sections are gone: this plugin was built for an older Ether. Update it; screens are documents (pages) now")
		}
		if len(sc.Pages) == 0 {
			add(base+".pages", "must declare the screen's pages")
			continue
		}
		if !m.HasCapability(CapScreens) {
			add(base+".pages", "needs the screens capability")
		}
		if _, ok := sc.Pages["index"]; !ok {
			add(base+".pages", "must include the landing page, index")
		}
		if len(sc.Pages) > MaxScreenPages {
			add(base+".pages", fmt.Sprintf("must declare at most %d pages", MaxScreenPages))
		}
		for page, paths := range sc.Pages {
			if !screenSlugRe.MatchString(page) {
				add(base+".pages."+page, "page ids are slugs matching ^[a-z][a-z0-9-]{0,31}$")
			}
			validateDocumentPaths(base+".pages."+page, paths, add)
		}
		switch sc.Placement {
		case "", "navigation", "hidden":
		default:
			add(base+".placement", "must be navigation or hidden")
		}
		if len(sc.ContentRating) > 16 {
			add(base+".contentRating", "must be a content rating such as TV-PG")
		}
		validateScreenActions(base+".actions", sc.Actions, add, label)
	}
}
