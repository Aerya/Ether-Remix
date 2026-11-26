package frontend

import (
	"context"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

// EpisodeOrder reads the viewer's presented ordering. The host owns saved
// groups and applies them to stored catalogue pages before adapters render.
type EpisodeOrder interface {
	Customized(context.Context, *Viewer) (bool, error)
	Grouped(context.Context, *Viewer, []RatingKey) (map[RatingKey]bool, error)
	Get(context.Context, *Viewer, RatingKey) (*Title, error)
	Children(context.Context, *Viewer, RatingKey) ([]Title, error)
	Grandchildren(context.Context, *Viewer, RatingKey) ([]Title, error)
	OrderStoredGrandchildren(context.Context, *Viewer, RatingKey, []Title) ([]Title, error)
	Position(context.Context, *Viewer, RatingKey) (season, episode int, name string, ok bool)
}

// EpisodeCoordinateResolver resolves an episode in the effective order.
type EpisodeCoordinateResolver interface {
	ResolveEpisode(context.Context, RatingKey, int, int) (RatingKey, error)
}

type AiredEpisodeTally = core.AiredEpisodeTally

// AiredEpisodeTallyReader reads stored counts for a batch of shows.
type AiredEpisodeTallyReader interface {
	AiredEpisodeTallies(context.Context, int64, []RatingKey, time.Time, []RatingKey) (map[RatingKey]AiredEpisodeTally, error)
}
