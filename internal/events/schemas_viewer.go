package events

import "time"

const (
	EventViewerAccountJoined      = "viewer.account.joined"
	EventViewerAccountDeactivated = "viewer.account.deactivated"
	EventViewerAccountMerged      = "viewer.account.merged"
	EventViewerAccessChanged      = "viewer.access.changed"
	EventViewerSafetyUpdated      = "viewer.safety.updated"
	EventViewerInviteCreated      = "viewer.invite.created"
	EventViewerInviteAccepted     = "viewer.invite.accepted"
	EventViewerWatchScrobbled     = "viewer.watch.scrobbled"
	EventViewerRatingSubmitted    = "viewer.rating.submitted"
)

type ViewerAccountJoinedPayload struct {
	ViewerID int64  `json:"viewerId" event:"label=Viewer ID;desc=Numeric identifier of the new viewer;example=7"`
	Username string `json:"username" event:"label=Username;desc=Viewer display name or username;pii;example=alex"`
	Role     string `json:"role" event:"label=Role;desc=Membership role assigned to the viewer;enum=owner|home|friend|local|guest;example=local"`
	Source   string `json:"source" event:"label=Source;desc=How the viewer account was created;example=invite"`
	InviteID string `json:"inviteId,omitempty" event:"label=Invite ID;desc=Identifier of the invite used if applicable;example=inv_9f2a4c8e1b0d"`
}

type ViewerAccountDeactivatedPayload struct {
	ViewerID int64  `json:"viewerId" event:"label=Viewer ID;desc=Numeric identifier of the deactivated viewer;example=7"`
	Username string `json:"username" event:"label=Username;desc=Viewer display name or username;pii;example=alex"`
	Reason   string `json:"reason" event:"label=Reason;desc=Why the viewer account was paused or deactivated;example=paused_by_owner"`
	Actor    string `json:"actor,omitempty" event:"label=Actor;desc=Owner or subsystem that deactivated the viewer;example=owner"`
}

type ViewerAccountMergedPayload struct {
	FromViewerID int64  `json:"fromViewerId" event:"label=Source Viewer ID;desc=Viewer account merged away;example=9"`
	IntoViewerID int64  `json:"intoViewerId" event:"label=Target Viewer ID;desc=Surviving viewer account;example=7"`
	Actor        string `json:"actor,omitempty" event:"label=Actor;desc=Owner or subsystem that initiated the merge;example=owner"`
}

type ViewerAccessChangedPayload struct {
	ViewerID         int64    `json:"viewerId" event:"label=Viewer ID;desc=Numeric identifier of the updated viewer;example=7"`
	Username         string   `json:"username" event:"label=Username;desc=Viewer display name or username;pii;example=alex"`
	StreamLimit      int      `json:"streamLimit" event:"label=Stream Limit;desc=Maximum concurrent streams allowed (0 means unlimited);example=2"`
	MaxContentRating string   `json:"maxContentRating,omitempty" event:"label=Max Content Rating;desc=Maximum allowed content maturity rating;example=PG-13"`
	AllowedLibraries []string `json:"allowedLibraries,omitempty" event:"label=Allowed Libraries;desc=Library IDs restricted for this viewer (empty means all);example=1,2"`
	Actor            string   `json:"actor,omitempty" event:"label=Actor;desc=Owner or subsystem that updated access;example=owner"`
}

type ViewerSafetyUpdatedPayload struct {
	ViewerID           int64  `json:"viewerId" event:"label=Viewer ID;desc=Numeric identifier of the viewer;example=7"`
	Username           string `json:"username" event:"label=Username;desc=Viewer display name or username;pii;example=alex"`
	RemoteDisabled     bool   `json:"remoteDisabled" event:"label=Remote Disabled;desc=Whether remote streaming outside the LAN is blocked;example=false"`
	MaxBitrateKbps     int    `json:"maxBitrateKbps" event:"label=Max Bitrate;desc=Maximum remote bitrate cap in kbps (0 means unlimited);unit=kbps;example=8000"`
	BedtimeEnabled     bool   `json:"bedtimeEnabled" event:"label=Bedtime Enabled;desc=Whether quiet-hours viewing restrictions are active;example=true"`
	BlockedGenresCount int    `json:"blockedGenresCount" event:"label=Blocked Genres;desc=Count of genres blocked for this viewer;example=2"`
	Actor              string `json:"actor,omitempty" event:"label=Actor;desc=Owner or subsystem that updated safety settings;example=owner"`
}

type ViewerInviteCreatedPayload struct {
	InviteID  string    `json:"inviteId" event:"label=Invite ID;desc=Non-secret identifier for the invite;example=inv_9f2a4c8e1b0d"`
	Label     string    `json:"label,omitempty" event:"label=Label;desc=Display label or intended viewer name for the invite;example=Alex"`
	MaxUses   int       `json:"maxUses" event:"label=Max Uses;desc=Maximum times the invite may be redeemed;example=1"`
	ExpiresAt time.Time `json:"expiresAt,omitzero" event:"label=Expires At;desc=Expiration timestamp of the invite;example=2026-11-26T12:00:00Z"`
	Actor     string    `json:"actor,omitempty" event:"label=Actor;desc=Owner or subsystem that created the invite;example=owner"`
}

type ViewerInviteAcceptedPayload struct {
	InviteID string `json:"inviteId" event:"label=Invite ID;desc=Non-secret identifier for the redeemed invite;example=inv_9f2a4c8e1b0d"`
	ViewerID int64  `json:"viewerId" event:"label=Viewer ID;desc=Viewer account bound to the invite;example=7"`
	Username string `json:"username" event:"label=Username;desc=Viewer display name or username;pii;example=alex"`
}

type ViewerWatchScrobbledPayload struct {
	ViewerID  int64  `json:"viewerId" event:"label=Viewer ID;desc=Viewer whose watch state changed;example=7"`
	Username  string `json:"username" event:"label=Username;desc=Viewer display name or username;pii;example=alex"`
	TitleKey  string `json:"titleKey" event:"label=Title Key;desc=Canonical rating key of the title;example=movie:603"`
	TitleName string `json:"titleName" event:"label=Title Name;desc=Display name of the title;example=The Matrix"`
	MediaType string `json:"mediaType" event:"label=Media Type;desc=Type of media scrobbled;enum=movie|show|season|episode|other;example=movie"`
	Watched   bool   `json:"watched" event:"label=Watched;desc=True when marked watched and false when marked unwatched;example=true"`
	Source    string `json:"source" event:"label=Source;desc=Source of the watch state update;example=scrobble"`
}

type ViewerRatingSubmittedPayload struct {
	ViewerID  int64   `json:"viewerId" event:"label=Viewer ID;desc=Viewer who submitted the rating;example=7"`
	Username  string  `json:"username" event:"label=Username;desc=Viewer display name or username;pii;example=alex"`
	TitleKey  string  `json:"titleKey" event:"label=Title Key;desc=Canonical rating key of the rated title;example=movie:603"`
	TitleName string  `json:"titleName" event:"label=Title Name;desc=Display name of the rated title;example=The Matrix"`
	Rating    float64 `json:"rating" event:"label=Rating;desc=Numeric rating submitted by the viewer;example=9.5"`
}

func init() {
	registerBuiltin(RegisterViewerSchemas)
}

// RegisterViewerSchemas registers all viewer domain event schemas on r.
func RegisterViewerSchemas(r *Registry) {
	sampleExpiry := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)

	mustRegisterBuiltin[ViewerAccountJoinedPayload](r, EventSchema{
		Type:        EventViewerAccountJoined,
		Label:       "Viewer Joined",
		Description: "A new household viewer account was created or gained access.",
		LegacyNames: []string{"viewer.added", "user.joined"},
		Sample: ViewerAccountJoinedPayload{
			ViewerID: 7,
			Username: "alex",
			Role:     "local",
			Source:   "invite",
			InviteID: "inv_9f2a4c8e1b0d",
		},
	})

	mustRegisterBuiltin[ViewerAccountDeactivatedPayload](r, EventSchema{
		Type:        EventViewerAccountDeactivated,
		Label:       "Viewer Deactivated",
		Description: "A viewer account was paused, removed, or lost share access.",
		Sample: ViewerAccountDeactivatedPayload{
			ViewerID: 7,
			Username: "alex",
			Reason:   "paused_by_owner",
			Actor:    "owner",
		},
	})

	mustRegisterBuiltin[ViewerAccountMergedPayload](r, EventSchema{
		Type:        EventViewerAccountMerged,
		Label:       "Viewers Merged",
		Description: "Two viewer accounts were merged into one identity.",
		Sample: ViewerAccountMergedPayload{
			FromViewerID: 9,
			IntoViewerID: 7,
			Actor:        "owner",
		},
	})

	mustRegisterBuiltin[ViewerAccessChangedPayload](r, EventSchema{
		Type:        EventViewerAccessChanged,
		Label:       "Viewer Access Changed",
		Description: "A viewer's library access, stream limit, or maturity rating changed.",
		LegacyNames: []string{"viewer.access_changed"},
		Sample: ViewerAccessChangedPayload{
			ViewerID:         7,
			Username:         "alex",
			StreamLimit:      2,
			MaxContentRating: "PG-13",
			AllowedLibraries: []string{"1", "2"},
			Actor:            "owner",
		},
	})

	mustRegisterBuiltin[ViewerSafetyUpdatedPayload](r, EventSchema{
		Type:        EventViewerSafetyUpdated,
		Label:       "Viewer Safety Policy Updated",
		Description: "Parental controls, bitrate caps, or remote access rules were updated for a viewer.",
		Sample: ViewerSafetyUpdatedPayload{
			ViewerID:           7,
			Username:           "alex",
			RemoteDisabled:     false,
			MaxBitrateKbps:     8000,
			BedtimeEnabled:     true,
			BlockedGenresCount: 2,
			Actor:              "owner",
		},
	})

	mustRegisterBuiltin[ViewerInviteCreatedPayload](r, EventSchema{
		Type:        EventViewerInviteCreated,
		Label:       "Viewer Invite Created",
		Description: "A viewer invitation link was created.",
		Sample: ViewerInviteCreatedPayload{
			InviteID:  "inv_9f2a4c8e1b0d",
			Label:     "Alex",
			MaxUses:   1,
			ExpiresAt: sampleExpiry,
			Actor:     "owner",
		},
	})

	mustRegisterBuiltin[ViewerInviteAcceptedPayload](r, EventSchema{
		Type:        EventViewerInviteAccepted,
		Label:       "Viewer Invite Accepted",
		Description: "A viewer redeemed an invitation link.",
		Sample: ViewerInviteAcceptedPayload{
			InviteID: "inv_9f2a4c8e1b0d",
			ViewerID: 7,
			Username: "alex",
		},
	})

	mustRegisterBuiltin[ViewerWatchScrobbledPayload](r, EventSchema{
		Type:        EventViewerWatchScrobbled,
		Label:       "Title Scrobbled",
		Description: "A title was marked watched or unwatched for a viewer.",
		Sample: ViewerWatchScrobbledPayload{
			ViewerID:  7,
			Username:  "alex",
			TitleKey:  "movie:603",
			TitleName: "The Matrix",
			MediaType: "movie",
			Watched:   true,
			Source:    "scrobble",
		},
	})

	mustRegisterBuiltin[ViewerRatingSubmittedPayload](r, EventSchema{
		Type:        EventViewerRatingSubmitted,
		Label:       "Rating Submitted",
		Description: "A viewer rated a title.",
		Sample: ViewerRatingSubmittedPayload{
			ViewerID:  7,
			Username:  "alex",
			TitleKey:  "movie:603",
			TitleName: "The Matrix",
			Rating:    9.5,
		},
	})
}
