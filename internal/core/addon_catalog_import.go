package core

import "time"

// Naming modes for collections created by importing a Stremio add-on manifest.
const (
	AddonImportNameAutoType   = "autoType"   // append (Movies)/(Series) only when two catalogues share a name
	AddonImportNamePlain      = "plain"      // keep the catalogue's manifest name as-is
	AddonImportNameAppendType = "appendType" // always append (Movies)/(Series)
	AddonImportNamePrefix     = "prefix"     // prepend NameAffix to every catalogue name
	AddonImportNameSuffix     = "suffix"     // append NameAffix to every catalogue name
)

// AddonCatalogExtra describes one filter extra declared by a Stremio catalog.
type AddonCatalogExtra struct {
	Name     string   `json:"name"`
	Label    string   `json:"label"`
	Required bool     `json:"required,omitempty"`
	Options  []string `json:"options,omitempty"`
}

// AddonCatalogPreviewItem is one movie or series catalog in a manifest preview.
type AddonCatalogPreviewItem struct {
	Key                    string              `json:"key"`
	Type                   string              `json:"type"`      // "movie" | "series"
	MediaType              string              `json:"mediaType"` // "movie" | "show"
	ID                     string              `json:"id"`
	Name                   string              `json:"name"`
	Order                  int                 `json:"order"`
	SupportsSkip           bool                `json:"supportsSkip,omitempty"`
	Standalone             bool                `json:"standalone"`
	Extras                 []AddonCatalogExtra `json:"extras,omitempty"`
	RequiredExtras         []string            `json:"requiredExtras,omitempty"`
	HasDuplicateName       bool                `json:"hasDuplicateName,omitempty"`
	Imported               bool                `json:"imported,omitempty"`
	ExistingCollectionID   string              `json:"existingCollectionId,omitempty"`
	ExistingCollectionName string              `json:"existingCollectionName,omitempty"`
	ExistingParams         map[string]string   `json:"existingParams,omitempty"`
	ExistingMaxItems       int                 `json:"existingMaxItems,omitempty"`
	ExistingRemoved        bool                `json:"existingRemoved,omitempty"`
}

// AddonCatalogRemovedItem is a managed collection whose catalogue disappeared
// from the add-on's manifest.
type AddonCatalogRemovedItem struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	CatalogType string `json:"catalogType"`
	CatalogID   string `json:"catalogId"`
	CatalogKey  string `json:"catalogKey"`
}

// AddonImportSkippedItem explains why one catalogue was not imported or synced.
type AddonImportSkippedItem struct {
	Key    string `json:"key"`
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

// AddonImportSummary records the counts from an import or manifest sync run.
type AddonImportSummary struct {
	Created  int                      `json:"created"`
	Updated  int                      `json:"updated"`
	Removed  int                      `json:"removed"`
	Restored int                      `json:"restored"`
	Skipped  []AddonImportSkippedItem `json:"skipped,omitempty"`
}

// AddonCatalogSubscription is the persisted manifest-following import config
// for one installed Stremio add-on.
type AddonCatalogSubscription struct {
	AddonID        string              `json:"addonId"`
	AddonName      string              `json:"addonName,omitempty"`
	AddonLogo      string              `json:"addonLogo,omitempty"`
	Host           string              `json:"host,omitempty"`
	AddonEnabled   bool                `json:"addonEnabled"`
	Folder         string              `json:"folder"`
	NameMode       string              `json:"nameMode"`
	NameAffix      string              `json:"nameAffix,omitempty"`
	FollowManifest bool                `json:"followManifest"`
	Refresh        string              `json:"refresh"`
	MaxItems       int                 `json:"maxItems"`
	Enabled        bool                `json:"enabled"`
	BrowseInApps   bool                `json:"browseInApps"`
	CatalogCount   int                 `json:"catalogCount"`
	ManagedCount   int                 `json:"managedCount"`
	RemovedCount   int                 `json:"removedCount"`
	LastSyncedAt   *time.Time          `json:"lastSyncedAt,omitempty"`
	LastSummary    *AddonImportSummary `json:"lastSummary,omitempty"`
}

// AddonCatalogAvailable describes an installed Stremio catalog add-on that can
// be imported or synced from the Collections console.
type AddonCatalogAvailable struct {
	AddonID      string `json:"addonId"`
	Name         string `json:"name"`
	Logo         string `json:"logo,omitempty"`
	Host         string `json:"host,omitempty"`
	Enabled      bool   `json:"enabled"`
	CatalogCount int    `json:"catalogCount"`
	HasImport    bool   `json:"hasImport,omitempty"`
}

// AddonCatalogImportList is returned by GET /api/collections/addon-imports.
type AddonCatalogImportList struct {
	Imports         []AddonCatalogSubscription `json:"imports"`
	AvailableAddons []AddonCatalogAvailable    `json:"availableAddons"`
}

// AddonCatalogPreviewRequest inspects either an installed add-on by ID or a
// manifest URL (without exposing or echoing the URL back).
type AddonCatalogPreviewRequest struct {
	URL     string `json:"url,omitempty"`
	AddonID string `json:"addonId,omitempty"`
}

// AddonCatalogPreview is returned by POST /api/collections/addon-imports/preview.
type AddonCatalogPreview struct {
	AddonID            string                    `json:"addonId"`
	Installed          bool                      `json:"installed"`
	AddonName          string                    `json:"addonName"`
	AddonVersion       string                    `json:"addonVersion,omitempty"`
	AddonDescription   string                    `json:"addonDescription,omitempty"`
	AddonLogo          string                    `json:"addonLogo,omitempty"`
	Host               string                    `json:"host,omitempty"`
	ConfigureFirst     bool                      `json:"configureFirst,omitempty"`
	Subscription       *AddonCatalogSubscription `json:"subscription,omitempty"`
	Catalogs           []AddonCatalogPreviewItem `json:"catalogs"`
	RemovedCollections []AddonCatalogRemovedItem `json:"removedCollections,omitempty"`
}

// AddonCatalogImportItem selects one catalog from a manifest to import.
type AddonCatalogImportItem struct {
	Key      string            `json:"key"`
	Name     string            `json:"name,omitempty"`
	Params   map[string]string `json:"params,omitempty"`
	MaxItems int               `json:"maxItems,omitempty"`
}

// AddonCatalogImportRequest creates/updates collections from selected add-on
// catalogs and optionally keeps them synced with the add-on's manifest.
type AddonCatalogImportRequest struct {
	URL            string                   `json:"url,omitempty"`
	AddonID        string                   `json:"addonId,omitempty"`
	Folder         string                   `json:"folder,omitempty"`
	NameMode       string                   `json:"nameMode,omitempty"`
	NameAffix      string                   `json:"nameAffix,omitempty"`
	FollowManifest bool                     `json:"followManifest"`
	Refresh        string                   `json:"refresh,omitempty"`
	MaxItems       int                      `json:"maxItems,omitempty"`
	Enabled        bool                     `json:"enabled"`
	BrowseInApps   bool                     `json:"browseInApps"`
	Items          []AddonCatalogImportItem `json:"items"`
}

// AddonCatalogImportResult summarizes an import or manual manifest sync.
type AddonCatalogImportResult struct {
	AddonID         string                   `json:"addonId"`
	AddonName       string                   `json:"addonName"`
	ManifestChanged bool                     `json:"manifestChanged,omitempty"`
	Created         int                      `json:"created"`
	Updated         int                      `json:"updated"`
	Removed         int                      `json:"removed"`
	Restored        int                      `json:"restored"`
	Skipped         []AddonImportSkippedItem `json:"skipped,omitempty"`
	CollectionIDs   []string                 `json:"collectionIds,omitempty"`
	Subscription    AddonCatalogSubscription `json:"subscription"`
}
