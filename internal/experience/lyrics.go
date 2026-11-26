package experience

import (
	"context"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
)

// PluginLyrics is the plugin catalogue's lyrics source.
type PluginLyrics interface {
	Lyrics(ctx context.Context, key core.RatingKey) (core.Lyrics, error)
}

var _ frontend.TitleLyrics = (*Service)(nil)

// TitleLyrics reads lyrics for a title the viewer may see.
func (s *Service) TitleLyrics(ctx context.Context, viewer *core.Viewer, key core.RatingKey) (core.Lyrics, error) {
	ctx = core.WithCatalogViewer(ctx, viewer)
	v, err := s.viewer(ctx, viewer)
	if err != nil {
		return core.Lyrics{}, err
	}
	source, ok := s.d.PluginLibraries.(PluginLyrics)
	if !ok || !key.IsPluginKey() {
		return core.Lyrics{}, core.ErrNotFound
	}
	item, err := s.pluginItem(core.WithNoContainerExpand(ctx), v, key)
	if err != nil {
		return core.Lyrics{}, err
	}
	if !item.Playable || !item.Title.LyricsDeclared {
		return core.Lyrics{}, core.ErrNotFound
	}
	return source.Lyrics(ctx, key)
}

type pluginLyricsHint interface {
	LyricsAvailability(key core.RatingKey) frontend.LyricAvailability
}

var _ frontend.TitleLyricsHint = (*Service)(nil)

func (s *Service) TitleLyricsAvailability(t core.Title) frontend.LyricAvailability {
	if !t.Key.IsPluginKey() || !t.LyricsDeclared {
		return frontend.LyricsAbsent
	}
	if hint, ok := s.d.PluginLibraries.(pluginLyricsHint); ok {
		return hint.LyricsAvailability(t.Key)
	}
	return frontend.LyricsAbsent
}

// LyricsAvailable reports whether a lyrics source is wired.
func (s *Service) LyricsAvailable(context.Context) bool {
	_, ok := s.d.PluginLibraries.(PluginLyrics)
	return ok
}
