package sessions

import (
	"context"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
)

type offlineDownloadSources struct {
	opener     frontend.OfflineDownloadOpener
	kindSource func(context.Context, *core.Viewer, core.RatingKey) (*core.KindInput, error)
}

// NewOfflineDownloadSources binds the playback source opener for this host.
// Only backends with a detached opener can advertise the feature.
func NewOfflineDownloadSources(playback core.Playback, kindSource func(context.Context, *core.Viewer, core.RatingKey) (*core.KindInput, error)) frontend.OfflineDownloadSources {
	opener, ok := playback.(frontend.OfflineDownloadOpener)
	if !ok {
		return nil
	}
	return offlineDownloadSources{opener: opener, kindSource: kindSource}
}

func (s offlineDownloadSources) OpenOfflineDownload(ctx context.Context, viewer *core.Viewer, title *core.Title, caps core.ClientCaps, release string, declaration *frontend.OriginalPlaybackCapabilities) (*frontend.OfflineDownloadSource, error) {
	var kind *core.KindInput
	if title.Key.IsPluginKey() {
		if s.kindSource == nil {
			return nil, core.ErrNotAvailable
		}
		var err error
		kind, err = s.kindSource(ctx, viewer, title.Key)
		if err != nil || kind == nil {
			return nil, core.ErrNotAvailable
		}
		if core.IsAutoVersionSelector(release) {
			release = ""
		}
		kind.PreferSourceID, kind.Exact = release, release != ""
	}
	return s.opener.OpenOriginalDownload(ctx, viewer, title, caps, release, kind, declaration)
}
