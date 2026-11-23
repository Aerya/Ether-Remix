package playback

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

type recordingChoiceResolver struct {
	core.Resolver
	mu      sync.Mutex
	records []recordedChoice
}

type recordedChoice struct {
	userID  int64
	key     core.RatingKey
	release core.Release
}

func (r *recordingChoiceResolver) RecordViewerChoice(_ context.Context, userID int64, title *core.Title, _ string, rel core.Release, _ *core.MediaInfo) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.records = append(r.records, recordedChoice{userID: userID, key: title.Key, release: rel})
	return nil
}

func (r *recordingChoiceResolver) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.records)
}

func TestViewerVersionChoiceRecordsOnlyAfterRealPlayOnExplicitVersion(t *testing.T) {
	h := newHarness(t, nil, []string{"rel-alt"})
	rec := &recordingChoiceResolver{Resolver: h.resolver}
	h.m.deps.Resolver = rec

	user := &core.User{ID: 42, Username: "viewer"}
	title := h.catalog.titles[movieKey]
	rel := core.Release{ID: "rel-alt", Name: "Movie.2160p.DV.REMUX"}

	makeSession := func(id, prefer string, dur time.Duration) *session {
		s := &session{
			id:         id,
			ident:      identity{userID: user.ID, clientID: "vela", key: movieKey},
			user:       *user,
			title:      title,
			caps:       defaultCaps("vela"),
			prefer:     prefer,
			stopCancel: func() {},
			cur:        &generation{res: &core.Resolution{Release: rel}, info: core.MediaInfo{Duration: dur}},
		}
		h.m.mu.Lock()
		h.m.sessions[id] = s
		h.m.mu.Unlock()
		t.Cleanup(func() {
			h.m.mu.Lock()
			delete(h.m.sessions, id)
			h.m.mu.Unlock()
		})
		return s
	}

	// 1. Auto playback (prefer == "") NEVER records a viewer choice.
	makeSession("s-auto", "", 100*time.Minute)
	h.m.PlaybackProgress(user.ID, "vela", movieKey, 1*time.Minute, "playing")
	h.m.PlaybackProgress(user.ID, "vela", movieKey, 15*time.Minute, "playing")
	if rec.count() != 0 {
		t.Fatalf("auto playback recorded %d choices, want 0", rec.count())
	}

	// 2. Explicit version quick sample (< 5% of 100m = 5m) does NOT record.
	h.m.mu.Lock()
	delete(h.m.sessions, "s-auto")
	h.m.mu.Unlock()
	s := makeSession("s-explicit", "slot:alt0", 100*time.Minute)
	h.m.NoteStartPosition(s.id, 0)
	h.m.PlaybackProgress(user.ID, "vela", movieKey, 2*time.Minute, "playing")
	if rec.count() != 0 {
		t.Fatalf("quick sample recorded %d choices, want 0", rec.count())
	}

	// 3. Bare seek jump (from 2m to 30m in a single tick > maxProgressDelta) does NOT record.
	h.m.PlaybackProgress(user.ID, "vela", movieKey, 30*time.Minute, "playing")
	if rec.count() != 0 {
		t.Fatalf("bare seek jump recorded %d choices, want 0", rec.count())
	}

	// 4. Continued real playback from 30m -> 31m -> 32m -> 33m -> 34m (total played >= 5m) records ONCE!
	for _, pos := range []time.Duration{31 * time.Minute, 32 * time.Minute, 33 * time.Minute, 34 * time.Minute} {
		h.m.PlaybackProgress(user.ID, "vela", movieKey, pos, "playing")
	}
	if rec.count() != 1 {
		t.Fatalf("real play recorded %d choices, want 1", rec.count())
	}
	if rec.records[0].userID != 42 || rec.records[0].key != movieKey || rec.records[0].release.ID != "rel-alt" {
		t.Fatalf("recorded = %+v", rec.records[0])
	}

	// 5. Further ticks do NOT double-record.
	h.m.PlaybackProgress(user.ID, "vela", movieKey, 35*time.Minute, "playing")
	if rec.count() != 1 {
		t.Fatalf("repeated progress recorded %d choices, want 1", rec.count())
	}
}

func TestViewerVersionChoiceFollowsResumeAfterSeconds(t *testing.T) {
	h := newHarness(t, nil, []string{"rel-alt"})
	rec := &recordingChoiceResolver{Resolver: h.resolver}
	h.m.deps.Resolver = rec

	cfg := h.m.config()
	cfg.Server.ResumeAfterSeconds = 30
	h.m.SetConfig(cfg)

	user := &core.User{ID: 43, Username: "viewer"}
	title := h.catalog.titles[movieKey]
	rel := core.Release{ID: "rel-alt", Name: "Movie.1080p"}
	s := &session{
		id:         "s-resume-after",
		ident:      identity{userID: user.ID, clientID: "vela", key: movieKey},
		user:       *user,
		title:      title,
		caps:       defaultCaps("vela"),
		prefer:     "slot:alt0",
		stopCancel: func() {},
		cur:        &generation{res: &core.Resolution{Release: rel}, info: core.MediaInfo{Duration: 100 * time.Minute}},
	}
	h.m.mu.Lock()
	h.m.sessions[s.id] = s
	h.m.mu.Unlock()
	t.Cleanup(func() {
		h.m.mu.Lock()
		delete(h.m.sessions, s.id)
		h.m.mu.Unlock()
	})

	h.m.NoteStartPosition(s.id, 0)
	h.m.PlaybackProgress(user.ID, "vela", movieKey, 20*time.Second, "playing")
	if rec.count() != 0 {
		t.Fatalf("below 30s threshold recorded %d choices, want 0", rec.count())
	}
	// 35s is far below 5% of 100m (5m), yet crosses the configured 30s threshold.
	h.m.PlaybackProgress(user.ID, "vela", movieKey, 35*time.Second, "playing")
	if rec.count() != 1 {
		t.Fatalf("at 35s with ResumeAfterSeconds=30 recorded %d choices, want 1", rec.count())
	}
}
