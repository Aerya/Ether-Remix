package vela

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/braedonsaunders/ether/internal/frontend"
)

// Keep wire grants comfortably inside the row cursor (10m), row visit (15m)
// and receipt (30m) lifetimes. Lazy eviction needs no idle worker.
const bootstrapAttempts = 3

// bootstrapChangedError is private: clients only see the bounded retry contract.
type bootstrapChangedError struct{}

func (*bootstrapChangedError) Error() string { return "bootstrap changed during assembly" }

const bootstrapTTL = 2 * time.Minute
const bootstrapBytes = 8 << 20

type bootstrapEntry struct {
	ready     chan struct{}
	revision  frontend.HomeRevision
	freshness frontend.PageFreshness
	expires   time.Time
	payload   json.RawMessage
}

type bootstrapDeadlineKey struct{}
type bootstrapDeadline struct{ expires time.Time }

func noteBootstrapDeadline(r *http.Request, expiry time.Time) {
	if lease, ok := r.Context().Value(bootstrapDeadlineKey{}).(*bootstrapDeadline); ok && !expiry.IsZero() && expiry.Before(lease.expires) {
		lease.expires = expiry
	}
}

type homeRevisionSource interface {
	HomeRevision(context.Context, *frontend.Viewer) (frontend.HomeRevision, bool, error)
}

func (i *instance) viewerSpoilerFree(ctx context.Context, v *frontend.Viewer) (bool, error) {
	if i.host.LiveEvents == nil || v == nil {
		return false, nil
	}
	sf, err := i.host.LiveEvents.SpoilerFree(ctx, v)
	if errors.Is(err, frontend.ErrNotAvailable) {
		return false, nil
	}
	return sf, err
}

func (i *instance) bootstrap(w http.ResponseWriter, r *http.Request) {
	v, token, err := i.viewer(r)
	if err != nil {
		fail(w, err, r)
		return
	}
	dev := requestDevice(r)
	avatar := i.viewerAvatar(r, v, token, dev)
	if local, ok := i.host.Experience.(interface {
		NoProviderWait(context.Context) context.Context
	}); ok {
		r = r.WithContext(local.NoProviderWait(r.Context()))
	}
	source, hasRevision := i.host.Experience.(homeRevisionSource)
	for attempt := 0; attempt < bootstrapAttempts; attempt++ {
		spoilerFree, err := i.viewerSpoilerFree(r.Context(), v)
		if err != nil {
			fail(w, err, r)
			return
		}
		// Credentials scope the image grants and cursor scope includes the complete
		// viewer policy. Do not reuse wire responses between refresh families/devices,
		// certificate-region preferences, or viewer spoiler-free preferences.
		scope := hashJSON([]any{v, dev, token, avatar, spoilerFree, r.URL.RequestURI(), i.appearanceDocument().etag, i.livePreviewMode(), i.liveEventsAvailable(), i.extrasAvailable(), i.extraPlaybackAvailable(), i.previewAvailable(), i.themeAvailable(), i.capabilitiesRevision(), i.signInMethods(), i.preferredRatingRegions(r), i.screensGeneration()})
		freshness := i.bootstrapFreshness()
		var before frontend.HomeRevision
		cacheable := false
		if hasRevision {
			before, cacheable, err = source.HomeRevision(r.Context(), v)
			if err != nil {
				fail(w, err, r)
				return
			}
		}
		var pending *bootstrapEntry
		if cacheable {
			i.bootstrapMu.Lock()
			entry := i.bootstraps[scope]
			if entry != nil && entry.payload == nil {
				i.bootstrapMu.Unlock()
				select {
				case <-entry.ready:
				case <-r.Context().Done():
					return
				}
				// Re-read the revision after the builder commits, within the same bounded budget.
				continue
			}
			if entry != nil && time.Now().Before(entry.expires) && entry.revision == before && entry.freshness == freshness {
				payload := entry.payload
				i.bootstrapMu.Unlock()
				varyLanguage(w)
				cache(w, r, payload)
				return
			}
			if i.bootstraps == nil {
				i.bootstraps = make(map[string]*bootstrapEntry)
			}
			total := 0
			for key, old := range i.bootstraps {
				if old.payload != nil && !time.Now().Before(old.expires) {
					delete(i.bootstraps, key)
					continue
				}
				total += len(old.payload)
			}
			// Admission bounds both pending builders and retained responses. A full
			// cache does not block other viewers or start background work.
			if len(i.bootstraps) < 32 && total < bootstrapBytes {
				pending = &bootstrapEntry{ready: make(chan struct{}), revision: before, freshness: freshness, expires: time.Now().Add(bootstrapTTL)}
				i.bootstraps[scope] = pending
			} else {
				delete(i.bootstraps, scope)
			}
			i.bootstrapMu.Unlock()
		}
		lease := &bootstrapDeadline{expires: time.Now().Add(bootstrapTTL)}
		buildRequest := r.WithContext(context.WithValue(r.Context(), bootstrapDeadlineKey{}, lease))
		if cacheable {
			if source, ok := i.host.Experience.(interface {
				BeginBootstrapRead(context.Context, *frontend.Viewer, frontend.HomeRevision) context.Context
			}); ok {
				buildRequest = buildRequest.WithContext(source.BeginBootstrapRead(buildRequest.Context(), v, before))
			}
		}
		out, buildErr := i.buildBootstrap(buildRequest, v, token, avatar, spoilerFree, attempt > 0)
		var payload json.RawMessage
		if buildErr == nil {
			payload, buildErr = json.Marshal(out)
		}
		if buildErr == nil && cacheable {
			after, available, revisionErr := source.HomeRevision(r.Context(), v)
			afterSpoilerFree, sfErr := i.viewerSpoilerFree(r.Context(), v)
			if revisionErr != nil {
				buildErr = revisionErr
			} else if sfErr != nil {
				buildErr = sfErr
			} else if !available || before != after || freshness != i.bootstrapFreshness() || afterSpoilerFree != spoilerFree {
				buildErr = frontend.ErrRowViewStale
			}
		}
		if pending != nil {
			i.bootstrapMu.Lock()
			total := len(payload)
			for key, old := range i.bootstraps {
				if key != scope {
					total += len(old.payload)
				}
			}
			if buildErr == nil && total <= bootstrapBytes {
				pending.payload = payload
				pending.expires = lease.expires
			} else {
				delete(i.bootstraps, scope)
			}
			close(pending.ready)
			i.bootstrapMu.Unlock()
		}
		if buildErr != nil {
			if errors.Is(buildErr, frontend.ErrRowViewStale) || errors.Is(buildErr, frontend.ErrLiveGuideStale) {
				if attempt+1 < bootstrapAttempts {
					continue
				}
				buildErr = &bootstrapChangedError{}
			}
			fail(w, buildErr, r)
			return
		}
		varyLanguage(w)
		cache(w, r, payload)
		return
	}
	fail(w, &bootstrapChangedError{}, r)
}

// Catalogue enrichment is a soft refresh (bounded by TTL). Policy revocations
// use HomeRevision.Policy; owner settings, grant rotations and library changes
// are hard inputs even when they do not write the shared policy epoch.
func (i *instance) bootstrapFreshness() frontend.PageFreshness {
	if i.host.PageFreshness == nil {
		return frontend.PageFreshness{}
	}
	f := i.host.PageFreshness()
	return frontend.PageFreshness{Config: f.Config, Layout: f.Layout, Grants: f.Grants, Accounts: f.Accounts, Safety: f.Safety, Libraries: f.Libraries, Images: f.Images, KindSync: f.KindSync, Screens: f.Screens}
}
