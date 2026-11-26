package app

import (
	"context"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/loudness"
	"github.com/braedonsaunders/ether/internal/state"
)

// buildLoudness measures an audio item once a viewer has played it through
// (from the source playback just used) and answers the measurements to every
// frontend. Nothing is measured ahead of use.
func (a *App) buildLoudness() {
	if a == nil || a.db == nil || a.state == nil || a.transcoder == nil || a.pluginsRT == nil ||
		a.pluginsRT.kindResolver == nil || a.pluginsRT.pluginitems == nil {
		return
	}
	resolver, items := a.pluginsRT.kindResolver, a.pluginsRT.pluginitems
	svc := loudness.New(loudness.Options{
		DB:     a.db,
		FFmpeg: a.transcoder.FFmpegPath,
		Logger: a.log,
		Open: func(ctx context.Context, viewer *core.Viewer, key core.RatingKey) (*frontend.KindMedia, error) {
			return resolver.OpenKind(ctx, viewer, key, nil)
		},
		AlbumOf: func(ctx context.Context, key core.RatingKey) (core.RatingKey, []core.RatingKey, bool) {
			it, _, err := items.GetByKey(ctx, key)
			if err != nil || it.ParentID == "" {
				return 0, nil, false
			}
			parentRef := core.NewItemRef(it.Ref.Plugin(), it.Ref.Kind(), it.ParentID)
			parent, _, err := items.GetByRef(ctx, parentRef)
			if err != nil {
				return 0, nil, false
			}
			page, err := items.Children(ctx, parentRef, "", 200)
			if err != nil || page.NextCursor != "" {
				return 0, nil, false // an album too large to be one album
			}
			var tracks []core.RatingKey
			for _, c := range page.Items {
				if c.Playable && c.Level == it.Level {
					tracks = append(tracks, c.Key)
				}
			}
			return parent.Key, tracks, len(tracks) > 0
		},
	})
	ctx, cancel := context.WithCancel(context.Background())
	go svc.Run(ctx)
	unsub := a.state.Subscribe(func(ev state.Event) {
		if (ev.Kind == state.EventCompleted || ev.Kind == state.EventScrobble) && !ev.Imported && !ev.Account {
			svc.Completed(ev.UserID, ev.Key)
		}
	})
	a.closers = append(a.closers, unsub, cancel)
	a.loudness = svc
}

// frontendLoudness is the loudness reader frontends see; nil when off.
func (a *App) frontendLoudness() frontend.LoudnessReader {
	if a == nil || a.loudness == nil {
		return nil
	}
	return a.loudness
}

// frontendSkipMarkers exposes the local-only marker reader to frontends.
func (a *App) frontendSkipMarkers() frontend.CachedMarkerRanges {
	if a == nil || a.markers == nil {
		return nil
	}
	return a.markers
}

// historyPlays reads a viewer's completed plugin plays for recaps.
func (a *App) historyPlays() func(context.Context, int64, time.Time, time.Time, int) ([]core.HistoryPlay, error) {
	if a == nil || a.state == nil {
		return nil
	}
	return a.state.PluginHistoryPlays
}

type recordingPrefs struct{ a *App }

func (a *App) recordingPrefs() recordingPrefs { return recordingPrefs{a: a} }

func (p recordingPrefs) get(ctx context.Context, uid int64, lib core.LibraryID, recs []string) (map[string]core.RatingKey, error) {
	if p.a.state == nil {
		return nil, core.ErrNotAvailable
	}
	return p.a.state.RecordingPreferences(ctx, uid, lib, recs)
}

func (p recordingPrefs) set(ctx context.Context, uid int64, lib core.LibraryID, rec string, preferred core.RatingKey, hide []core.RatingKey) error {
	if p.a.state == nil {
		return core.ErrNotAvailable
	}
	return p.a.state.SetRecordingPreference(ctx, uid, lib, rec, preferred, hide)
}

func (p recordingPrefs) hidden(ctx context.Context, uid int64) ([]core.RatingKey, error) {
	if p.a.state == nil {
		return nil, nil
	}
	return p.a.state.HiddenRecordingCopies(ctx, uid)
}
