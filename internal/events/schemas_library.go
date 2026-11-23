package events

const (
	EventLibraryTitleAdded        = "library.title.added"
	EventLibraryBatchAdded        = "library.batch.added"
	EventLibraryTitleAvailable    = "library.title.available"
	EventLibraryTitleUnavailable  = "library.title.unavailable"
	EventLibraryEpisodeAired      = "library.episode.aired"
	EventLibrarySyncStarted       = "library.sync.started"
	EventLibrarySyncCompleted     = "library.sync.completed"
	EventLibrarySyncFailed        = "library.sync.failed"
	EventLibraryCollectionRebuilt = "library.collection.rebuilt"
	EventLibraryWatchlistSynced   = "library.watchlist.synced"
	EventLibraryMetadataFailed    = "library.metadata.failed"
)

type LibraryTitleAddedPayload struct {
	LibraryID   string   `json:"libraryId" event:"label=Library ID;desc=Identifier of the library that received the title;example=catalog"`
	LibraryName string   `json:"libraryName" event:"label=Library Name;desc=Display name of the library;example=Movies"`
	TitleKey    string   `json:"titleKey" event:"label=Title Key;desc=Canonical Ether rating key for the title;example=movie:603"`
	TitleName   string   `json:"titleName" event:"label=Title Name;desc=Title or episode name;example=The Matrix"`
	ShowTitle   string   `json:"showTitle,omitempty" event:"label=Show Title;desc=Parent series title for seasons and episodes;example=Severance"`
	Season      int      `json:"season,omitempty" event:"label=Season;desc=Season number for television items;example=2"`
	Episode     int      `json:"episode,omitempty" event:"label=Episode;desc=Episode number for television episodes;example=1"`
	MediaType   string   `json:"mediaType" event:"label=Media Type;desc=Type of library item added;enum=movie|show|season|episode|other;example=movie"`
	Year        int      `json:"year,omitempty" event:"label=Year;desc=Release year;example=1999"`
	Genres      []string `json:"genres,omitempty" event:"label=Genres;desc=Genres associated with the title;example=Action,Science Fiction"`
	PosterURL   string   `json:"posterUrl,omitempty" event:"label=Poster URL;desc=Poster artwork URL or path;example=/library/metadata/603/thumb"`
}

type LibraryBatchAddedPayload struct {
	LibraryID    string   `json:"libraryId" event:"label=Library ID;desc=Identifier of the library that received the batch;example=catalog"`
	LibraryName  string   `json:"libraryName" event:"label=Library Name;desc=Display name of the library;example=TV Shows"`
	MediaType    string   `json:"mediaType" event:"label=Media Type;desc=Media type of items in the grouped batch;enum=movie|show|season|episode|mixed|other;example=episode"`
	ShowKey      string   `json:"showKey,omitempty" event:"label=Show Key;desc=Series key when the batch groups episodes of one show;example=show:95396"`
	ShowTitle    string   `json:"showTitle,omitempty" event:"label=Show Title;desc=Series title when the batch groups episodes of one show;example=Severance"`
	Season       int      `json:"season,omitempty" event:"label=Season;desc=Season number when the batch groups one season;example=2"`
	EpisodeCount int      `json:"episodeCount,omitempty" event:"label=Episode Count;desc=Number of episodes included in the batch;example=8"`
	ItemCount    int      `json:"itemCount" event:"label=Item Count;desc=Total number of newly added items in the batch;example=8"`
	TitleNames   []string `json:"titleNames,omitempty" event:"label=Title Names;desc=Sample of title or episode names in the batch;example=Hello Ms. Cobel,Goodbye Mrs. Selvig"`
	WindowSec    int      `json:"windowSec" event:"label=Window;desc=Debounce aggregation window in seconds;unit=s;example=15"`
	PosterURL    string   `json:"posterUrl,omitempty" event:"label=Poster URL;desc=Representative poster URL or path;example=/library/metadata/95396/thumb"`
}

type LibraryTitleAvailablePayload struct {
	TitleKey       string `json:"titleKey" event:"label=Title Key;desc=Canonical rating key of the title;example=movie:603"`
	TitleName      string `json:"titleName" event:"label=Title Name;desc=Display title that became playable;example=The Matrix"`
	MediaType      string `json:"mediaType" event:"label=Media Type;desc=Type of media item;enum=movie|show|season|episode|other;example=movie"`
	Year           int    `json:"year,omitempty" event:"label=Year;desc=Release year;example=1999"`
	Source         string `json:"source" event:"label=Source;desc=Subsystem that established availability;example=watchlist"`
	WatchedByCount int    `json:"watchedByCount,omitempty" event:"label=Watchlist Count;desc=Number of viewers tracking this title;example=2"`
}

type LibraryTitleUnavailablePayload struct {
	TitleKey  string `json:"titleKey" event:"label=Title Key;desc=Canonical rating key of the title;example=movie:603"`
	TitleName string `json:"titleName" event:"label=Title Name;desc=Display title that lost playability;example=The Matrix"`
	MediaType string `json:"mediaType" event:"label=Media Type;desc=Type of media item;enum=movie|show|season|episode|other;example=movie"`
	Reason    string `json:"reason" event:"label=Reason;desc=Reason the title is no longer playable;example=no healthy releases available"`
}

type LibraryEpisodeAiredPayload struct {
	ShowKey       string `json:"showKey" event:"label=Show Key;desc=Canonical rating key of the series;example=show:95396"`
	ShowTitle     string `json:"showTitle" event:"label=Show Title;desc=Name of the series;example=Severance"`
	EpisodeKey    string `json:"episodeKey" event:"label=Episode Key;desc=Canonical rating key of the newly aired episode;example=episode:95396:2:1"`
	EpisodeTitle  string `json:"episodeTitle" event:"label=Episode Title;desc=Title of the newly aired episode;example=Hello Ms. Cobel"`
	Season        int    `json:"season" event:"label=Season;desc=Season number;example=2"`
	Episode       int    `json:"episode" event:"label=Episode;desc=Episode number;example=1"`
	AirDate       string `json:"airDate,omitempty" event:"label=Air Date;desc=ISO calendar air date of the episode;example=2025-01-17"`
	FollowerCount int    `json:"followerCount" event:"label=Follower Count;desc=Number of household viewers following this show;example=3"`
}

type LibrarySyncStartedPayload struct {
	LibraryID   string `json:"libraryId" event:"label=Library ID;desc=Identifier of the library being synced;example=catalog"`
	LibraryName string `json:"libraryName" event:"label=Library Name;desc=Display name of the library;example=Catalog"`
	SourceKind  string `json:"sourceKind" event:"label=Source Kind;desc=Backing sync provider kind;example=tmdb"`
	Trigger     string `json:"trigger" event:"label=Trigger;desc=What initiated the sync pass;enum=scheduled|manual|startup|webhook;example=scheduled"`
}

type LibrarySyncCompletedPayload struct {
	LibraryID   string `json:"libraryId" event:"label=Library ID;desc=Identifier of the library that finished syncing;example=catalog"`
	LibraryName string `json:"libraryName" event:"label=Library Name;desc=Display name of the library;example=Catalog"`
	SourceKind  string `json:"sourceKind" event:"label=Source Kind;desc=Backing sync provider kind;example=tmdb"`
	Added       int    `json:"added" event:"label=Added;desc=Number of items added during sync;example=24"`
	Updated     int    `json:"updated" event:"label=Updated;desc=Number of items updated during sync;example=110"`
	Removed     int    `json:"removed" event:"label=Removed;desc=Number of items removed during sync;example=2"`
	DurationMS  int64  `json:"durationMs" event:"label=Duration;desc=Total sync duration in milliseconds;duration_ms;unit=ms;example=4250"`
}

type LibrarySyncFailedPayload struct {
	LibraryID   string `json:"libraryId" event:"label=Library ID;desc=Identifier of the library whose sync failed;example=catalog"`
	LibraryName string `json:"libraryName" event:"label=Library Name;desc=Display name of the library;example=Catalog"`
	SourceKind  string `json:"sourceKind" event:"label=Source Kind;desc=Backing sync provider kind;example=tmdb"`
	Error       string `json:"error" event:"label=Error;desc=Error reported by the sync pass;example=tmdb rate limit exceeded"`
}

type LibraryCollectionRebuiltPayload struct {
	CollectionID   string `json:"collectionId" event:"label=Collection ID;desc=Identifier of the rebuilt collection;example=trending-movies"`
	CollectionName string `json:"collectionName" event:"label=Collection Name;desc=Display name of the collection;example=Trending Movies"`
	SourceKind     string `json:"sourceKind" event:"label=Source Kind;desc=Primary source kind populating the collection;example=tmdb_trending"`
	ItemCount      int    `json:"itemCount" event:"label=Item Count;desc=Total titles in the collection after rebuild;example=40"`
	Added          int    `json:"added" event:"label=Added;desc=Number of titles newly added to the collection;example=5"`
	Removed        int    `json:"removed" event:"label=Removed;desc=Number of titles removed from the collection;example=3"`
	DurationMS     int64  `json:"durationMs" event:"label=Duration;desc=Time spent rebuilding the collection in milliseconds;duration_ms;unit=ms;example=820"`
}

type LibraryWatchlistSyncedPayload struct {
	Provider   string `json:"provider" event:"label=Provider;desc=Watchlist provider synced;example=plex"`
	ViewerID   int64  `json:"viewerId" event:"label=Viewer ID;desc=Household viewer whose watchlist was synced;example=1"`
	Added      int    `json:"added" event:"label=Added;desc=Number of newly added watchlist items;example=3"`
	Removed    int    `json:"removed" event:"label=Removed;desc=Number of removed watchlist items;example=1"`
	TotalItems int    `json:"totalItems" event:"label=Total Items;desc=Total watchlist items tracked after sync;example=28"`
	DurationMS int64  `json:"durationMs" event:"label=Duration;desc=Sync duration in milliseconds;duration_ms;unit=ms;example=640"`
}

type LibraryMetadataFailedPayload struct {
	Provider  string `json:"provider" event:"label=Provider;desc=Metadata provider that returned an error;example=tmdb"`
	TitleKey  string `json:"titleKey" event:"label=Title Key;desc=Canonical rating key whose metadata fetch failed;example=movie:603"`
	MediaType string `json:"mediaType" event:"label=Media Type;desc=Type of item being fetched;enum=movie|show|season|episode|other;example=movie"`
	Error     string `json:"error" event:"label=Error;desc=Failure reason returned by the metadata provider;example=upstream status 502"`
}

func init() {
	registerBuiltin(RegisterLibrarySchemas)
}

// RegisterLibrarySchemas registers all library domain event schemas on r.
func RegisterLibrarySchemas(r *Registry) {
	mustRegisterBuiltin[LibraryTitleAddedPayload](r, EventSchema{
		Type:        EventLibraryTitleAdded,
		Label:       "Title Added",
		Description: "A new title or episode was added to a library.",
		Sample: LibraryTitleAddedPayload{
			LibraryID:   "catalog",
			LibraryName: "Movies",
			TitleKey:    "movie:603",
			TitleName:   "The Matrix",
			MediaType:   "movie",
			Year:        1999,
			Genres:      []string{"Action", "Science Fiction"},
			PosterURL:   "/library/metadata/603/thumb",
		},
	})

	mustRegisterBuiltin[LibraryBatchAddedPayload](r, EventSchema{
		Type:        EventLibraryBatchAdded,
		Label:       "Recently Added Batch",
		Description: "Debounced group of newly added titles or episodes collapsed into one digest event.",
		Sample: LibraryBatchAddedPayload{
			LibraryID:    "catalog",
			LibraryName:  "TV Shows",
			MediaType:    "episode",
			ShowKey:      "show:95396",
			ShowTitle:    "Severance",
			Season:       2,
			EpisodeCount: 8,
			ItemCount:    8,
			TitleNames:   []string{"Hello Ms. Cobel", "Goodbye Mrs. Selvig"},
			WindowSec:    15,
			PosterURL:    "/library/metadata/95396/thumb",
		},
	})

	mustRegisterBuiltin[LibraryTitleAvailablePayload](r, EventSchema{
		Type:        EventLibraryTitleAvailable,
		Label:       "Title Became Available",
		Description: "A tracked or watchlisted title became playable.",
		LegacyNames: []string{"title.available"},
		Sample: LibraryTitleAvailablePayload{
			TitleKey:       "movie:603",
			TitleName:      "The Matrix",
			MediaType:      "movie",
			Year:           1999,
			Source:         "watchlist",
			WatchedByCount: 2,
		},
	})

	mustRegisterBuiltin[LibraryTitleUnavailablePayload](r, EventSchema{
		Type:        EventLibraryTitleUnavailable,
		Label:       "Title Became Unavailable",
		Description: "A previously playable tracked title no longer has a healthy playable release.",
		Sample: LibraryTitleUnavailablePayload{
			TitleKey:  "movie:603",
			TitleName: "The Matrix",
			MediaType: "movie",
			Reason:    "no healthy releases available",
		},
	})

	mustRegisterBuiltin[LibraryEpisodeAiredPayload](r, EventSchema{
		Type:        EventLibraryEpisodeAired,
		Label:       "New Episode Aired",
		Description: "A new episode aired for a series followed by one or more viewers.",
		LegacyNames: []string{"viewer.episode"},
		Sample: LibraryEpisodeAiredPayload{
			ShowKey:       "show:95396",
			ShowTitle:     "Severance",
			EpisodeKey:    "episode:95396:2:1",
			EpisodeTitle:  "Hello Ms. Cobel",
			Season:        2,
			Episode:       1,
			AirDate:       "2025-01-17",
			FollowerCount: 3,
		},
	})

	mustRegisterBuiltin[LibrarySyncStartedPayload](r, EventSchema{
		Type:        EventLibrarySyncStarted,
		Label:       "Library Sync Started",
		Description: "A catalogue or plugin library sync pass started.",
		Sample: LibrarySyncStartedPayload{
			LibraryID:   "catalog",
			LibraryName: "Catalog",
			SourceKind:  "tmdb",
			Trigger:     "scheduled",
		},
	})

	mustRegisterBuiltin[LibrarySyncCompletedPayload](r, EventSchema{
		Type:        EventLibrarySyncCompleted,
		Label:       "Library Sync Completed",
		Description: "A catalogue or plugin library sync pass finished successfully.",
		Sample: LibrarySyncCompletedPayload{
			LibraryID:   "catalog",
			LibraryName: "Catalog",
			SourceKind:  "tmdb",
			Added:       24,
			Updated:     110,
			Removed:     2,
			DurationMS:  4250,
		},
	})

	mustRegisterBuiltin[LibrarySyncFailedPayload](r, EventSchema{
		Type:        EventLibrarySyncFailed,
		Label:       "Library Sync Failed",
		Description: "A catalogue or plugin library sync pass failed.",
		Sample: LibrarySyncFailedPayload{
			LibraryID:   "catalog",
			LibraryName: "Catalog",
			SourceKind:  "tmdb",
			Error:       "tmdb rate limit exceeded",
		},
	})

	mustRegisterBuiltin[LibraryCollectionRebuiltPayload](r, EventSchema{
		Type:        EventLibraryCollectionRebuilt,
		Label:       "Collection Rebuilt",
		Description: "A dynamic collection finished rebuilding its item list.",
		LegacyNames: []string{"collection.rebuilt"},
		Sample: LibraryCollectionRebuiltPayload{
			CollectionID:   "trending-movies",
			CollectionName: "Trending Movies",
			SourceKind:     "tmdb_trending",
			ItemCount:      40,
			Added:          5,
			Removed:        3,
			DurationMS:     820,
		},
	})

	mustRegisterBuiltin[LibraryWatchlistSyncedPayload](r, EventSchema{
		Type:        EventLibraryWatchlistSynced,
		Label:       "Watchlist Synced",
		Description: "A viewer's external watchlist finished syncing.",
		Sample: LibraryWatchlistSyncedPayload{
			Provider:   "plex",
			ViewerID:   1,
			Added:      3,
			Removed:    1,
			TotalItems: 28,
			DurationMS: 640,
		},
	})

	mustRegisterBuiltin[LibraryMetadataFailedPayload](r, EventSchema{
		Type:        EventLibraryMetadataFailed,
		Label:       "Metadata Fetch Failed",
		Description: "Fetching metadata for a title failed.",
		Sample: LibraryMetadataFailedPayload{
			Provider:  "tmdb",
			TitleKey:  "movie:603",
			MediaType: "movie",
			Error:     "upstream status 502",
		},
	})
}
