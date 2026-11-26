package core

import "time"

// Contract between the Recommendations engine (internal/personalize), the
// owner API (internal/admin, design §7) and the apps (design §11).

// Row purposes the engine sets (frontend.Row* share these values).
const (
	PurposeContinue       RowPurpose = "continue"
	PurposeNextUp         RowPurpose = "nextUp"
	PurposeLive           RowPurpose = "live"
	PurposeRecentContexts RowPurpose = "recentContexts" // listen: "Jump back in"
	PurposeLiked          RowPurpose = "liked"          // listen: Liked Songs
)

// RowPresentation is how an app should draw a served row; the engine fills
// it from the page row (or the source's defaults for Ether's picks). Apps
// that can't draw a value fall back to a standard shelf.
type RowPresentation struct {
	// RowID is the page row ("" for one of Ether's picks).
	RowID string `json:"rowId,omitempty"`
	// Layout is standard, hero, wide, tiles or tracks; "" is the app default.
	Layout string `json:"layout,omitempty"`
	// Card is poster, landscape, square or round; "" follows the library.
	Card string `json:"card,omitempty"`
	// Role is the items' media role (album, artist, playlist, track,
	// audiobook, episode, book, …) when the app knows it; apps fill it.
	Role     string `json:"role,omitempty"`
	Personal bool   `json:"personal,omitempty"`
	// Library is the library of a one-row-per-library row.
	Library LibraryID `json:"library,omitempty"`
	// Experience is the row library's experience (watch, listen, read,
	// gallery, live); "" is watch.
	Experience string `json:"experience,omitempty"`
	Numbered   bool   `json:"numbered,omitempty"`
	Trailer    bool   `json:"trailer,omitempty"`
	// HideWatched is applied by the server; apps that rebuild a row's items
	// themselves apply it too.
	HideWatched bool `json:"hideWatched,omitempty"`
	// Surface marks a plugin surface row: a stub the app draws from the
	// plugin's document (Vela only). Ether never sees its titles.
	Surface *RowSurface `json:"surface,omitempty"`
}

// RowSurface identifies a plugin surface row (plugin screens §3b).
type RowSurface struct {
	// Plugin is the plugin id.
	Plugin string `json:"plugin"`
	// PluginName is the plugin's display name for console labels.
	PluginName string `json:"pluginName,omitempty"`
	// Surface is the surface's wire id ("sports.tonight"), the same value
	// as bootstrap.surfaces[].id and the /vela/v0/surfaces/{wireId} URLs.
	Surface string `json:"surface"`
}

// PageLibrary describes the library behind a library page (and each
// library a Home row can draw from) for the designer.
type PageLibrary struct {
	ID         LibraryID `json:"id"`
	Name       string    `json:"name"`
	Experience string    `json:"experience"`
	// MediaType is movie or show for catalogue libraries; "" otherwise.
	MediaType MediaType `json:"mediaType,omitempty"`
	// Plugin is the providing plugin's display name ("" for catalogue).
	Plugin string `json:"plugin,omitempty"`
	// Caps are the Cap* values the library has.
	Caps []string `json:"caps"`
	// Card is the library's card shape.
	Card string `json:"card,omitempty"`
	// RowsTabs are the library's rows tabs (key and label), in tab order.
	RowsTabs []PageTab `json:"rowsTabs,omitempty"`
	// Shelves are a plugin library's declared shelves (key and label), the
	// choices of the Plugin Shelf source on its pages.
	Shelves []ParamOption `json:"shelves,omitempty"`
}

// PageTab is one rows tab of a library.
type PageTab struct {
	Key   string `json:"key"`
	Label string `json:"label"`
}

// PageSummary is one entry of the designer's page picker (GET /pages).
type PageSummary struct {
	Key        PageKey   `json:"key"`
	Name       string    `json:"name"` // "Home", "Movies › Recommendations"
	Library    LibraryID `json:"library,omitempty"`
	Tab        string    `json:"tab,omitempty"`
	Experience string    `json:"experience"`
	Mode       string    `json:"mode"`
	RowCount   int       `json:"rowCount"`
	Saved      bool      `json:"saved"`
	// Apps lists the apps that show the page (vela, plex, mediaBrowser, silo).
	Apps []string `json:"apps"`
}

// PageDocument is GET /pages/{key}: the saved page (or the seeded default
// when Saved is false), the library behind it, and who has changes.
type PageDocument struct {
	Page    RowPage      `json:"page"`
	Saved   bool         `json:"saved"`
	Library *PageLibrary `json:"library,omitempty"`
	// Libraries are every library a Home row can draw from (Home only).
	Libraries []PageLibrary `json:"libraries,omitempty"`
	// ViewersWithChanges lists viewers with a variant, most changes first.
	ViewersWithChanges []ViewerChanges `json:"viewersWithChanges"`
	// Defaults are the engine's page lengths when Length is 0.
	DefaultLength int `json:"defaultLength"`
}

// ViewerChanges is a viewer with "changes just for them" on a page.
type ViewerChanges struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	Changes int    `json:"changes"`
}

// PageSources is GET /pages/{key}/sources.
type PageSources struct {
	Sources   []PageSource  `json:"sources"`
	Libraries []PageLibrary `json:"libraries"`
	// Collections are the owner's collections for the collection source.
	Collections []CollectionRef `json:"collections,omitempty"`
}

// CollectionRef names a collection for the source picker.
type CollectionRef struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Count int    `json:"count"`
}

// PageSource is one entry of GET /pages/{key}/sources: a source and
// whether it fits this page.
type PageSource struct {
	RowSourceInfo
	// Available is false when no library of the page has what it needs;
	// Unavailable says why in plain words.
	Available   bool   `json:"available"`
	Unavailable string `json:"unavailable,omitempty"`
	// Libraries are the page's libraries (Home: every library) it can
	// draw from.
	Libraries []LibraryID `json:"libraries,omitempty"`
	// Used counts rows of this source already on the page.
	Used int `json:"used"`
	// Refs lists the choices of a ref parameter (collections, shelves),
	// so the console never shows raw ids.
	Refs []ParamOption `json:"refs,omitempty"`
}

// PagePreviewRequest is POST /pages/{key}/preview.
type PagePreviewRequest struct {
	// Page is the unsaved draft; nil previews the saved page.
	Page *RowPage `json:"page,omitempty"`
	// Viewer previews as a viewer; 0 is the typical viewer.
	Viewer int64 `json:"viewer,omitempty"`
	// Device is tv, mobile, web or desktop.
	Device string `json:"device"`
	// At is the moment to preview (date and time, server local).
	At    time.Time `json:"at"`
	Count int       `json:"count,omitempty"`
}

// PagePreview is the response: the served rows in order, Ether's picks it
// could still use (the folded "Ether's picks" section), the rows not shown
// with a reason, the summary counts, and, for a viewer with limits, the
// access strip.
type PagePreview struct {
	Summary  PreviewCounts `json:"summary"`
	Rows     []HomeRow     `json:"rows"`
	Picks    []PreviewPick `json:"picks"`
	NotShown []NotShownRow `json:"notShown"`
	Access   *ViewerAccess `json:"access,omitempty"`
	// GeneratedAt and Context echo the request.
	GeneratedAt time.Time  `json:"generatedAt"`
	Context     HubContext `json:"context"`
}

// PreviewCounts feeds the preview's summary line: the rows that count
// toward the length, the length, and how they were placed.
type PreviewCounts struct {
	Rows   int `json:"rows"`
	Length int `json:"length"`
	Pinned int `json:"pinned"`
	Shown  int `json:"shown"`
	// Auto counts rows Ether chose (Auto rows and its picks).
	Auto int `json:"auto"`
	// Picks is how many of Ether's picks the page could still use.
	Picks int `json:"picks"`
	// PicksOn is whether the page lets Ether add its picks.
	PicksOn bool `json:"picksOn"`
	// Chosen counts the page's own rows on it (Rows less Ether's picks).
	Chosen int `json:"chosen"`
	// AppOnly counts rows only some apps draw (plugin rows Vela draws);
	// they are not in Rows and don't count toward the length.
	AppOnly int `json:"appOnly"`
}

// PreviewPick is one of Ether's picks not on the page right now.
type PreviewPick struct {
	Kind     string `json:"kind"`
	Title    string `json:"title"`
	Personal bool   `json:"personal"`
	Why      string `json:"why"`
}

// NotShownRow is a page row that isn't on the page for this viewer and
// moment, with the strongest reason and, when previewing elsewhere would
// show it, where.
type NotShownRow struct {
	RowID  string `json:"row,omitempty"`
	Title  string `json:"title"`
	Reason string `json:"reason"`
	// PreviewAs is a viewer who would see it; PreviewAt a moment it shows.
	PreviewAs int64      `json:"previewAs,omitempty"`
	PreviewAt *time.Time `json:"previewAt,omitempty"`
}

// ViewerAccess is the Viewer access strip: what limits the previewed viewer.
type ViewerAccess struct {
	Viewer int64  `json:"viewer"`
	Name   string `json:"name"`
	// AgeLimit is the content-rating ceiling ("" none).
	AgeLimit string `json:"ageLimit,omitempty"`
	// Unrated is "hidden" or "shown": how titles without a rating fare.
	Unrated         string   `json:"unrated"`
	LibrariesSeen   int      `json:"librariesSeen"`
	LibrariesTotal  int      `json:"librariesTotal"`
	HiddenLibraries []string `json:"hiddenLibraries,omitempty"`
}

// RowPreview is the response: the titles per row (one per library for a
// one-row-per-library row) and the total across them.
type RowPreview struct {
	Rows  []HomeRow `json:"rows"`
	Total int       `json:"total"`
}

// PageImpact is POST /pages/{key}/impact: who a draft changes.
type PageImpact struct {
	Viewers     int `json:"viewers"`
	WithChanges int `json:"withChanges"`
}

// RowPreviewRequest is POST /pages/{key}/rows/preview: one draft row's
// titles for the drawer's Content tab (a fan-out returns one per library).
type RowPreviewRequest struct {
	Row    Row       `json:"row"`
	Viewer int64     `json:"viewer,omitempty"`
	Device string    `json:"device"`
	At     time.Time `json:"at"`
	Count  int       `json:"count,omitempty"`
}

// PageUsage is one place a collection is used (GET /collections/{id}/usage
// returns {"uses": []PageUsage}).
type PageUsage struct {
	Page      PageKey `json:"page"`
	PageName  string  `json:"pageName"`
	RowID     string  `json:"rowId"`
	RowName   string  `json:"rowName"`
	Placement string  `json:"placement"` // pinned | show | auto | hide
	Position  int     `json:"position,omitempty"`
	// Schedule is a plain-words summary ("Fri–Sat evenings"); "" any time.
	Schedule string `json:"schedule,omitempty"`
}

// FacetPick is one value for a facet parameter picker
// (GET /row-sources/facets/{library}/{facet} returns {"values": []FacetPick}).
type FacetPick struct {
	Value string `json:"value"`
	Label string `json:"label"`
	Count int    `json:"count,omitempty"`
}

// SurfaceInfo is a plugin Home-row surface the engine can place as a row
// (source kind "surface", param surface = "<pluginID>:<surfaceID>").
type SurfaceInfo struct {
	Plugin     string `json:"plugin"`     // plugin id
	ID         string `json:"id"`         // the plugin-local surface id
	WireID     string `json:"wireId"`     // "<pluginShort>.<id>", the document URL id
	Title      string `json:"title"`      // the surface's console name
	PluginName string `json:"pluginName"` // the plugin's display name
	// ContentRating is the surface's declared rating ("" none).
	ContentRating string `json:"contentRating,omitempty"`
}
