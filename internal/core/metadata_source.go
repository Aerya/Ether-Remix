package core

import (
	"context"
	"errors"
	"sync"
	"time"
)

type MetadataSourceID string

const (
	MetadataTMDB MetadataSourceID = "tmdb"
	MetadataTVDB MetadataSourceID = "tvdb"
)

var ErrUnsupported = errors.New("metadata operation unsupported")

type MetadataSource interface {
	ID() MetadataSourceID
	Capabilities() MetadataCaps
	Configured() bool
	Search(context.Context, MetadataSearch) ([]MetadataHit, error)
	Lookup(context.Context, ProviderRef) (*Title, error)
	Seasons(context.Context, ProviderRef, EpisodeOrder) ([]SeasonSummary, error)
	Episodes(context.Context, ProviderRef, EpisodeOrder, int) ([]EpisodeDetail, error)
	FindByExternal(context.Context, IDs, MediaType) ([]ProviderRef, error)
	Images(context.Context, ProviderRef) (Artwork, error)
	Person(context.Context, ProviderRef) (*Person, error)
	Lists(context.Context, ListKind, MediaType, int) ([]MetadataHit, error)
}

type MetadataCaps struct {
	Movies, Shows, People, Images, Search, Lists bool
	EpisodeOrders                                []string
}

// ProviderRef identifies an item in one provider's namespace.
type ProviderRef struct {
	Source MetadataSourceID `json:"source"`
	ID     string           `json:"id"`
	Type   MediaType        `json:"type"`
}

type EpisodeOrder struct {
	Source MetadataSourceID `json:"source"`
	Kind   string           `json:"kind"`
}

const (
	OrderAired    = "aired"
	OrderDVD      = "dvd"
	OrderAbsolute = "absolute"
)

type SeasonSummary struct {
	Ref          ProviderRef
	Season       int
	Title        string
	Summary      string
	PosterPath   string
	AirDate      string
	EpisodeCount int
}

type EpisodeDetail struct {
	Aired           *EpisodeNumber
	Ref             ProviderRef
	IDs             IDs
	Season, Episode int
	Absolute        int
	AirDate         string // YYYY-MM-DD
	Title, Summary  string
	StillPath       string
	RuntimeMinutes  int
}

type MetadataSearch struct {
	Title    string
	Year     int
	Type     MediaType
	Language string
}

type MetadataHit struct {
	Released   time.Time
	Adult      bool
	Ref        ProviderRef
	IDs        IDs
	Title      string
	Year       int
	Summary    string
	PosterPath string
}

type Artwork = ArtworkImages

type ListKind string

const (
	ListTrending ListKind = "trending"
	ListPopular  ListKind = "popular"
	ListNew      ListKind = "new"
)

// MetadataSources is safe for concurrent use. Registration order is retained
// so the first configured source can be used as a default.
type MetadataSources struct {
	mu      sync.RWMutex
	sources []MetadataSource
}

func NewMetadataSources(sources ...MetadataSource) *MetadataSources {
	r := &MetadataSources{}
	for _, source := range sources {
		r.Register(source)
	}
	return r
}

func (r *MetadataSources) Register(source MetadataSource) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, current := range r.sources {
		if current.ID() == source.ID() {
			r.sources[i] = source
			return
		}
	}
	r.sources = append(r.sources, source)
}

func (r *MetadataSources) Get(id MetadataSourceID) MetadataSource {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, source := range r.sources {
		if source.ID() == id {
			return source
		}
	}
	return nil
}

func (r *MetadataSources) Configured() []MetadataSource {
	r.mu.RLock()
	sources := append([]MetadataSource(nil), r.sources...)
	r.mu.RUnlock()
	configured := sources[:0]
	for _, source := range sources {
		if source.Configured() {
			configured = append(configured, source)
		}
	}
	return configured
}

type metadataRefreshContextKey struct{}

// WithMetadataRefresh requests fresh source data for an explicit metadata
// refresh. Providers may reuse responses fetched during the same refresh.
func WithMetadataRefresh(ctx context.Context) context.Context {
	if MetadataRefreshRequested(ctx) {
		return ctx
	}
	return context.WithValue(ctx, metadataRefreshContextKey{}, time.Now())
}

func MetadataRefreshRequested(ctx context.Context) bool {
	return !MetadataRefreshStarted(ctx).IsZero()
}

// MetadataRefreshStarted distinguishes responses from this explicit action
// from entries already in the provider cache when the action began.
func MetadataRefreshStarted(ctx context.Context) time.Time {
	started, _ := ctx.Value(metadataRefreshContextKey{}).(time.Time)
	return started
}
