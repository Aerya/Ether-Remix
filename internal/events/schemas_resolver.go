package events

// Canonical resolver event type names.
const (
	EventResolverSearchCompleted    = "resolver.search.completed"
	EventResolverSearchEmpty        = "resolver.search.empty"
	EventResolverReleaseSelected    = "resolver.release.selected"
	EventResolverReleaseBlocklisted = "resolver.release.blocklisted"
	EventResolverPinCreated         = "resolver.pin.created"
	EventResolverPinRemoved         = "resolver.pin.removed"
	EventResolverPinUnhealthy       = "resolver.pin.unhealthy"
	EventResolverPinRejected        = "resolver.pin.rejected"
)

// ResolverSearchCompletedPayload is emitted when a release search succeeds.
type ResolverSearchCompletedPayload struct {
	TitleKey        string `json:"titleKey" event:"label=Title key;example=movie:603"`
	TitleName       string `json:"titleName,omitempty" event:"label=Title name;example=The Matrix"`
	MediaType       string `json:"mediaType,omitempty" event:"label=Media type;enum=movie|episode|live|trailer;example=movie"`
	CandidateCount  int    `json:"candidateCount" event:"label=Candidates found;example=18"`
	EligibleCount   int    `json:"eligibleCount" event:"label=Eligible candidates;example=6"`
	WinnerReleaseID string `json:"winnerReleaseId,omitempty" event:"label=Winner release ID;example=rel_01"`
	DurationMS      int64  `json:"durationMs" event:"label=Search duration;duration_ms;unit=ms;example=420"`
	CacheHit        bool   `json:"cacheHit" event:"label=Cache hit;example=false"`
}

// ResolverSearchEmptyPayload is emitted when resolution finds no playable release.
type ResolverSearchEmptyPayload struct {
	TitleKey        string `json:"titleKey" event:"label=Title key;example=movie:603"`
	TitleName       string `json:"titleName,omitempty" event:"label=Title name;example=The Matrix"`
	MediaType       string `json:"mediaType,omitempty" event:"label=Media type;enum=movie|episode|live|trailer;example=movie"`
	ProfileID       string `json:"profileId,omitempty" event:"label=Quality profile;example=4k-hdr"`
	IndexersQueried int    `json:"indexersQueried" event:"label=Indexers queried;example=3"`
	RejectedCount   int    `json:"rejectedCount" event:"label=Rejected candidates;example=9"`
	TopRejection    string `json:"topRejection,omitempty" event:"label=Top rejection reason;example=below minimum resolution"`
}

// ResolverReleaseSelectedPayload is emitted when the resolver selects a winning release.
type ResolverReleaseSelectedPayload struct {
	TitleKey    string `json:"titleKey" event:"label=Title key;example=movie:603"`
	ReleaseID   string `json:"releaseId" event:"label=Release ID;example=rel_01"`
	ReleaseName string `json:"releaseName" event:"label=Release name;example=The.Matrix.1999.2160p.UHD.BluRay.REMUX.HEVC-GROUP"`
	Indexer     string `json:"indexer,omitempty" event:"label=Indexer;example=NZBGeek"`
	Protocol    string `json:"protocol,omitempty" event:"label=Protocol;example=usenet"`
	SizeBytes   int64  `json:"sizeBytes,omitempty" event:"label=Release size;unit=bytes;example=38400000000"`
	Resolution  string `json:"resolution,omitempty" event:"label=Resolution;example=2160p"`
	HDR         string `json:"hdr,omitempty" event:"label=HDR format;example=hdr10"`
	Score       int    `json:"score,omitempty" event:"label=Ranking score;example=940"`
	Pinned      bool   `json:"pinned" event:"label=Pinned release;example=false"`
}

// ResolverReleaseBlocklistedPayload is emitted when a release is added to the blocklist.
type ResolverReleaseBlocklistedPayload struct {
	ReleaseID   string `json:"releaseId" event:"label=Release ID;example=rel_01"`
	ReleaseName string `json:"releaseName,omitempty" event:"label=Release name;example=The.Matrix.1999.BAD-GROUP"`
	TitleKey    string `json:"titleKey,omitempty" event:"label=Title key;example=movie:603"`
	Indexer     string `json:"indexer,omitempty" event:"label=Indexer;example=NZBGeek"`
	Reason      string `json:"reason" event:"label=Blocklist reason;example=corrupt audio track"`
	Actor       string `json:"actor,omitempty" event:"label=Actor;example=owner"`
}

// ResolverPinCreatedPayload is emitted when an owner pins a release to a title.
type ResolverPinCreatedPayload struct {
	TitleKey    string `json:"titleKey" event:"label=Title key;example=movie:603"`
	TitleName   string `json:"titleName,omitempty" event:"label=Title name;example=The Matrix"`
	ReleaseID   string `json:"releaseId" event:"label=Release ID;example=rel_01"`
	ReleaseName string `json:"releaseName,omitempty" event:"label=Release name;example=The.Matrix.1999.2160p.REMUX"`
	Note        string `json:"note,omitempty" event:"label=Pin note;example=Best Atmos mix"`
	Actor       string `json:"actor,omitempty" event:"label=Actor;example=owner"`
}

// ResolverPinRemovedPayload is emitted when a release pin is removed.
type ResolverPinRemovedPayload struct {
	TitleKey  string `json:"titleKey" event:"label=Title key;example=movie:603"`
	TitleName string `json:"titleName,omitempty" event:"label=Title name;example=The Matrix"`
	ReleaseID string `json:"releaseId,omitempty" event:"label=Release ID;example=rel_01"`
	Actor     string `json:"actor,omitempty" event:"label=Actor;example=owner"`
}

// ResolverPinUnhealthyPayload is emitted when a pinned release becomes unhealthy or unplayable.
type ResolverPinUnhealthyPayload struct {
	TitleKey  string `json:"titleKey" event:"label=Title key;example=movie:603"`
	TitleName string `json:"titleName,omitempty" event:"label=Title name;example=The Matrix"`
	ReleaseID string `json:"releaseId" event:"label=Release ID;example=rel_01"`
	Status    string `json:"status" event:"label=Pin health status;example=unhealthy"`
	Reason    string `json:"reason" event:"label=Unhealthy reason;example=missing articles on all providers"`
}

// ResolverPinRejectedPayload is emitted when a pinned release is rejected for a client request.
type ResolverPinRejectedPayload struct {
	TitleKey  string `json:"titleKey" event:"label=Title key;example=movie:603"`
	TitleName string `json:"titleName,omitempty" event:"label=Title name;example=The Matrix"`
	ReleaseID string `json:"releaseId" event:"label=Release ID;example=rel_01"`
	Client    string `json:"client,omitempty" event:"label=Client;example=Vela iOS"`
	Reason    string `json:"reason" event:"label=Rejection reason;example=release exceeds client resolution ceiling"`
}

// RegisterResolverSchemas registers all built-in resolver event schemas.
func RegisterResolverSchemas(r *Registry) {
	registerMediaSchema[ResolverSearchCompletedPayload](r, EventSchema{
		Type:        EventResolverSearchCompleted,
		Version:     1,
		Domain:      "resolver",
		Label:       "Resolver search completed",
		Description: "Emitted when release resolution finishes and finds a playable candidate.",
		Owner:       OwnerBuiltin,
	})
	registerMediaSchema[ResolverSearchEmptyPayload](r, EventSchema{
		Type:        EventResolverSearchEmpty,
		Version:     1,
		Domain:      "resolver",
		Label:       "Resolver search empty",
		Description: "Emitted when release resolution finds no eligible playable release.",
		Owner:       OwnerBuiltin,
	})
	registerMediaSchema[ResolverReleaseSelectedPayload](r, EventSchema{
		Type:        EventResolverReleaseSelected,
		Version:     1,
		Domain:      "resolver",
		Label:       "Release selected",
		Description: "Emitted when the resolver picks a winning release for a title.",
		Owner:       OwnerBuiltin,
	})
	registerMediaSchema[ResolverReleaseBlocklistedPayload](r, EventSchema{
		Type:        EventResolverReleaseBlocklisted,
		Version:     1,
		Domain:      "resolver",
		Label:       "Release blocklisted",
		Description: "Emitted when a release is blocklisted.",
		Owner:       OwnerBuiltin,
	})
	registerMediaSchema[ResolverPinCreatedPayload](r, EventSchema{
		Type:        EventResolverPinCreated,
		Version:     1,
		Domain:      "resolver",
		Label:       "Release pin created",
		Description: "Emitted when a release is pinned to a title.",
		Owner:       OwnerBuiltin,
	})
	registerMediaSchema[ResolverPinRemovedPayload](r, EventSchema{
		Type:        EventResolverPinRemoved,
		Version:     1,
		Domain:      "resolver",
		Label:       "Release pin removed",
		Description: "Emitted when a release pin is removed from a title.",
		Owner:       OwnerBuiltin,
	})
	registerMediaSchema[ResolverPinUnhealthyPayload](r, EventSchema{
		Type:        EventResolverPinUnhealthy,
		Version:     1,
		Domain:      "resolver",
		Label:       "Pinned release unhealthy",
		Description: "Emitted when a pinned release fails verification or is blocked.",
		Owner:       OwnerBuiltin,
		Critical:    true,
	})
	registerMediaSchema[ResolverPinRejectedPayload](r, EventSchema{
		Type:        EventResolverPinRejected,
		Version:     1,
		Domain:      "resolver",
		Label:       "Pinned release rejected",
		Description: "Emitted when a pinned release cannot satisfy a client's playback constraints.",
		Owner:       OwnerBuiltin,
	})
}

func init() {
	registerBuiltin(RegisterResolverSchemas)
}
