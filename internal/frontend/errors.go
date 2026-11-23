package frontend

import (
	"errors"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/auth"
	"github.com/braedonsaunders/ether/internal/core"
)

// ErrNotFound identifies an absent or inaccessible frontend resource.
// It shares identity with the domain error for errors.Is checks.
var ErrNotFound = core.ErrNotFound

// ErrUnauthorized identifies a viewer who cannot see or change the resource.
// It shares identity with the domain error for errors.Is checks.
var ErrUnauthorized = core.ErrUnauthorized

// ErrNotAvailable means the host has no service for this browse feature.
// It shares identity with the domain error for errors.Is checks.
var ErrNotAvailable = core.ErrNotAvailable

// ErrPlexNotShared identifies a valid plex.tv account without access to this server.
var ErrPlexNotShared = auth.ErrPlexNotShared

// ErrPlexUnreachable indicates plex.tv token validation is unavailable.
var ErrPlexUnreachable = auth.ErrPlexUnreachable

// ErrRowViewStale asks the client to refresh its visit-scoped row views.
var ErrRowViewStale = errors.New("frontend: row view snapshot expired or changed")

// ErrLiveGuideStale asks the caller to restart a changed guide snapshot.
var ErrLiveGuideStale = errors.New("frontend: live guide snapshot changed")

// ErrStreamLimit identifies exhausted playback admission.
var ErrStreamLimit = core.ErrStreamLimit

// ProviderConnectionCapError reports that a live media provider's concurrent
// connection limit has been reached.
type ProviderConnectionCapError = core.ProviderConnectionCapError

// ErrDownloadBudget identifies an exceeded temporary download disk budget.
// It shares identity with the domain error for errors.Is checks.
var ErrDownloadBudget = core.ErrDownloadBudget

// ErrDownloadNeedsTranscode identifies a download source that cannot be
// copied to MP4. It shares identity with the domain error for errors.Is.
var ErrDownloadNeedsTranscode = core.ErrDownloadNeedsTranscode

// ErrVideoTranscodingDisabled identifies a playback or conversion request that
// requires re-encoding video while transcode.videoTranscoding is set to "off".
var ErrVideoTranscodingDisabled = core.ErrVideoTranscodingDisabled

// RequiredAudioLanguageError reports that no release matched the quality
// profile's required Audio language.
type RequiredAudioLanguageError = core.RequiredAudioLanguageError

// The two wordings a viewer can be shown when a title would need its video
// re-encoded: the server-wide switch, or the owner's switch for their account.
const (
	MsgVideoTranscodingOff       = "This title needs a video transcode to play on this device, and video transcoding is turned off."
	MsgViewerVideoTranscodingOff = "This title would need converting to play on this device, and converting video is turned off for your account. Try another version or device, or ask the server owner."
)

// Wordings for download conversion and download limits.
const (
	MsgDownloadTranscodingOff       = "This title would need converting to download at this quality, and converting downloads is turned off on this server. Try another quality, or ask the server owner."
	MsgViewerDownloadTranscodingOff = "This title would need converting to download at this quality, and converting downloads is turned off for your account. Try another quality, or ask the server owner."
	MsgViewerDownloadLimit          = "You've reached your download limit for today. It resets tomorrow at 00:00 UTC."
	MsgViewerDownloadDataLimit      = "You've reached your download data limit for today. It resets tomorrow at 00:00 UTC."
)

// DownloadLimitMessage formats a viewer-facing explanation when a download
// hits the server or per-viewer download limit, including when it resets.
func DownloadLimitMessage(d core.DownloadLimitDecision) string {
	period := d.Period
	if period != "month" {
		period = "day"
	}
	var head string
	if d.Unit == "gb" {
		if period == "month" {
			head = "You've reached your download data limit for this month."
		} else {
			head = "You've reached your download data limit for today."
		}
	} else {
		if period == "month" {
			head = "You've reached your download limit for this month."
		} else {
			head = "You've reached your download limit for today."
		}
	}
	if period == "month" {
		if !d.ResetAt.IsZero() {
			return head + " It resets on " + d.ResetAt.UTC().Format("Jan 2 at 15:04 UTC") + "."
		}
		return head + " It resets next month."
	}
	if !d.ResetAt.IsZero() {
		return head + " It resets tomorrow at " + d.ResetAt.UTC().Format("15:04 UTC") + "."
	}
	return head + " It resets tomorrow at 00:00 UTC."
}

// VideoTranscodingOffMessage picks the wording for viewer. policy is the
// host's viewer policy; one that cannot answer gets the server-wide wording.
func VideoTranscodingOffMessage(policy any, viewer *core.User) string {
	if p, ok := policy.(interface {
		VideoTranscodingDisabled(*core.User) bool
	}); ok && viewer != nil && p.VideoTranscodingDisabled(viewer) {
		return MsgViewerVideoTranscodingOff
	}
	return MsgVideoTranscodingOff
}

// ErrInteractionReceipt is an invalid, stale or out-of-scope interaction handle.
var ErrInteractionReceipt = errors.New("experience: invalid interaction receipt")

// IdentityRateLimitError preserves the identity denial and its fixed-window
// retry delay without exposing the peer, username or bucket identifiers.
type IdentityRateLimitError struct{ RetryAfter time.Duration }

func (*IdentityRateLimitError) Error() string { return ErrRateLimited.Error() }
func (*IdentityRateLimitError) Unwrap() error { return ErrRateLimited }

// RejectedPlaybackPath records why a delivery candidate could not be used.
type RejectedPlaybackPath struct {
	Path    string   `json:"path"`
	Reasons []string `json:"reasons"`
}

// NoCompatiblePathError is a terminal capability/policy decision, rather than
// a temporary source failure. It preserves the candidates evaluated by planning.
type NoCompatiblePathError struct {
	RejectedPaths []RejectedPlaybackPath `json:"rejectedPaths"`
	Cause         error                  `json:"-"`
}

func (e *NoCompatiblePathError) Error() string {
	var reasons []string
	for _, path := range e.RejectedPaths {
		reasons = append(reasons, path.Path+": "+strings.Join(path.Reasons, ", "))
	}
	return "no compatible playback path: " + strings.Join(reasons, "; ")
}
func (e *NoCompatiblePathError) Unwrap() error {
	if e != nil && e.Cause != nil {
		return e.Cause
	}
	return ErrNotAvailable
}
