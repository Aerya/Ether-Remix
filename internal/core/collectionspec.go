package core

import (
	"encoding/json"
	"time"
)

// CollectionSpec is an owner-designed collection: candidates gathered from
// Sources, filtered by Rules, sorted and limited. A collection is content
// only; any Recommendations page can show it as a row.
type CollectionSpec struct {
	Users           *UserTargets       `json:"users,omitempty"`           // Remix: explicit collection access; nil means all.
	NavigationGroup string             `json:"navigationGroup,omitempty"` // studios | services | genres
	NavigationOrder int                `json:"navigationOrder,omitempty"`
	ID              string             `json:"id,omitempty"` // server-assigned
	Name            string             `json:"name"`
	Description     string             `json:"description,omitempty"`
	MediaType       string             `json:"mediaType"` // "movie" | "show" | "mixed"
	Sources         []CollectionSource `json:"sources"`
	Match           string             `json:"match"` // "all" | "any"
	Rules           []CollectionRule   `json:"rules"`
	Exclude         []CollectionRule   `json:"exclude"`
	// Sort: popularity | rating | released | title | added | random | personal | source.
	RankedTop10 bool              `json:"rankedTop10,omitempty"`
	Sort        string            `json:"sort"`
	SortDesc    bool              `json:"sortDesc"`
	Limit       int               `json:"limit"`   // 0 = no limit
	Refresh     string            `json:"refresh"` // live | hourly | daily | weekly
	Rotate      *CollectionRotate `json:"rotate,omitempty"`
	Artwork     CollectionArtwork `json:"artwork"`
	// BrowseInApps shows the collection as a browsable collection in its
	// library's Collections view in every app. Rows are placed on
	// Recommendations pages, never here.
	BrowseInApps   bool   `json:"browseInApps"`
	PreviewOnFocus bool   `json:"previewOnFocus,omitempty"`
	Enabled        bool   `json:"enabled"`
	LocalOnly      bool   `json:"localOnly,omitempty"`     // evaluate rules against the local catalogue only; no remote discovery
	ReleaseMode    string `json:"releaseMode,omitempty"`   // released (default), upcoming, or mixed
	LookaheadDays  int    `json:"lookaheadDays,omitempty"` // 1..366 when upcoming is enabled
	TemplateID     string `json:"templateId,omitempty"`
	// Server-maintained.
	UpdatedAt   *time.Time `json:"updatedAt,omitempty"`
	LastBuiltAt *time.Time `json:"lastBuiltAt,omitempty"`
	ItemCount   int        `json:"itemCount"`

	// Additive fields beyond the contract (read-only for clients).
	BuiltIn   bool     `json:"builtIn,omitempty"`   // shipped with Ether; only targets/artwork/rotate/enabled are editable
	RatingKey string   `json:"ratingKey,omitempty"` // the Plex ratingKey of the published collection
	Warnings  []string `json:"warnings,omitempty"`  // from the last build
	// OwnerUserID is set for private collections created by a Plex user
	// (core.Collections.CreateSmart); 0 = server-wide.
	OwnerUserID int64 `json:"ownerUserId,omitempty"`
	// Folder optionally groups collections in the console and anchors their
	// display order across frontends (for example, collections imported from a
	// Stremio catalogue add-on).
	Folder string `json:"folder,omitempty"`
	// SortOrder optionally orders collections within a folder or add-on import
	// (1-based; 0 preserves creation order).
	SortOrder int `json:"sortOrder,omitempty"`
	// AddonSync links a collection to the installed Stremio add-on catalogue it
	// was imported from so manifest sync can keep names, order and removal
	// status up to date without overwriting owner customizations.
	AddonSync *CollectionAddonSync `json:"addonSync,omitempty"`
}

// CollectionAddonSync records the add-on catalogue a collection tracks.
type CollectionAddonSync struct {
	AddonID     string `json:"addonId"`
	AddonName   string `json:"addonName,omitempty"`
	CatalogType string `json:"catalogType"`
	CatalogID   string `json:"catalogId"`
	CatalogKey  string `json:"catalogKey"`
	ManagedName string `json:"managedName,omitempty"`
	Removed     bool   `json:"removed,omitempty"`
}

// CollectionSource is one place candidates come from. Kind selects which
// other fields apply:
//
//	catalog                                   the whole browsable catalogue
//	tmdbList        List, MediaType           trending_day|trending_week|popular|top_rated|now_playing|upcoming|airing_today|on_the_air
//	tmdbCollection  ID, Label                 a TMDB franchise
//	tmdbDiscover    Params                    raw /discover parameters (names allowed for ids)
//	tmdbKeyword | tmdbCompany | tmdbNetwork | tmdbPerson   ID, Label (Role for people)
//	watchProvider   ID, Region, Label         what's streaming on a service
//	traktList | letterboxdList | mdblist      URL
//	anilistList                               URL (an AniList anime list; optionally one named list)
//	stevenlu                                  List: movies (default) or a score-filtered variant
//	plugin:<id>/<key>  Params, MaxItems       a plugin's or Stremio add-on's list (catalog)
//	titles          Keys                      hand-picked rating keys
//
// When ID is 0 the Label is resolved to a TMDB id by name.
type CollectionSource struct {
	Kind      string            `json:"kind"`
	List      string            `json:"list,omitempty"`
	MediaType string            `json:"mediaType,omitempty"`
	ID        int64             `json:"id,omitempty"`
	Label     string            `json:"label,omitempty"`
	Params    map[string]string `json:"params,omitempty"`
	Region    string            `json:"region,omitempty"`
	URL       string            `json:"url,omitempty"`
	Keys      []string          `json:"keys,omitempty"`
	// Role narrows tmdbPerson credits: "cast", "director", "writer",
	// "crew" or "" (every credit). Additive to the contract.
	Role string `json:"role,omitempty"`
	// MaxItems caps how many items a plugin or add-on list source pulls per
	// refresh (1..2000). Additive to the contract.
	MaxItems int `json:"maxItems,omitempty"`
}

// CollectionRule is one condition. Value is typed by Field; Label and Labels
// are display text for ids.
type CollectionRule struct {
	Field  string          `json:"field"`
	Op     string          `json:"op"`
	Value  json.RawMessage `json:"value"`
	Label  string          `json:"label,omitempty"`
	Labels []string        `json:"labels,omitempty"`
}

// CollectionRotate shows a rotating subset of the members.
type CollectionRotate struct {
	Size  int    `json:"size"`
	Every string `json:"every"` // "daily" | "weekly"
}

// CollectionArtwork selects the poster Plex shows for the collection.
type CollectionArtwork struct {
	PublicOnly bool   `json:"publicOnly,omitempty"` // imported packs must never fetch private network images
	Logo       string `json:"logo,omitempty"`       // TMDB logo path for navigation cards
	Mode       string `json:"mode"`                 // auto | collage | backdrop | tmdb | custom
	URL        string `json:"url,omitempty"`
	Accent     string `json:"accent,omitempty"` // "#rrggbb"
}

// CollectionTemplate is a curated starting point in the Collection Studio.
type CollectionTemplate struct {
	ID          string         `json:"id"`
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Category    string         `json:"category"`
	Spec        CollectionSpec `json:"spec"`
}

// CollectionPreview is the result of evaluating an unsaved spec.
type CollectionPreview struct {
	Total    int      `json:"total"`
	Items    []Title  `json:"items"`
	Warnings []string `json:"warnings"`
	TookMs   int64    `json:"tookMs"`
}

// LookupResult is one TMDB entity (person, keyword, company, network,
// collection, watch provider) found by name for the collection builder.
type LookupResult struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Subtitle  string `json:"subtitle,omitempty"`
	ImagePath string `json:"imagePath,omitempty"` // TMDB image path
}
