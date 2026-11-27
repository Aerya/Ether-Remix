package screens

import (
	"context"
	"crypto/rand"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/favorites"
)

// ServiceOptions configures a screen resolver Service.
type ServiceOptions struct {
	Hub       LiveFeedHub
	Watch     WatchTargetResolver
	Favorites FavoriteEntities
	Now       func() time.Time
	// Documents indexes document screens and surfaces; nil disables them.
	Documents *DocRegistry
	// TopicKey seals stream topics; empty uses a key made at start.
	TopicKey []byte
}

// Service serves document screens and surfaces: data reads, actions and
// live streams. It does no background work; streams poll only while open.
type Service struct {
	favorites FavoriteEntities
	now       func() time.Time

	mu    sync.RWMutex
	hub   LiveFeedHub
	watch WatchTargetResolver

	docs       *DocRegistry
	live       LiveSubscriber
	caller     PluginCaller
	items      ItemFinder
	answers    *answerCache
	actionRate rateLimiter
	pushes     pluginWatch
	revs       *revStore
	extra      ExtraSources
	topicKey   []byte
}

// NewService constructs a screen resolver service.
func NewService(opts ServiceOptions) *Service {
	nowFn := opts.Now
	if nowFn == nil {
		nowFn = time.Now
	}
	key := opts.TopicKey
	if len(key) == 0 {
		key = make([]byte, 32)
		_, _ = rand.Read(key)
	}
	return &Service{
		favorites: opts.Favorites,
		now:       nowFn,
		hub:       opts.Hub,
		watch:     opts.Watch,
		docs:      opts.Documents,
		revs:      newRevStore(nowFn),
		answers:   newAnswerCache(),
		topicKey:  key,
	}
}

// Documents returns the document registry (nil when disabled).
func (s *Service) Documents() *DocRegistry {
	if s == nil {
		return nil
	}
	return s.docs
}

// SetExtraSources installs the resolver for app-owned source types.
func (s *Service) SetExtraSources(x ExtraSources) {
	if s != nil {
		s.extra = x
	}
}

func (s *Service) hubDeps() LiveFeedHub {
	h, _ := s.deps()
	return h
}

// SetHub swaps the live event hub implementation (used when W255's liveevents.Hub attaches).
func (s *Service) SetHub(h LiveFeedHub) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.hub = h
	s.mu.Unlock()
}

// SetWatchTargets swaps the watch-target matcher (used when W257's matcher attaches).
func (s *Service) SetWatchTargets(w WatchTargetResolver) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.watch = w
	s.mu.Unlock()
}

func (s *Service) deps() (LiveFeedHub, WatchTargetResolver) {
	s.mu.RLock()
	h, w := s.hub, s.watch
	s.mu.RUnlock()
	return h, w
}

// Navigation lists top-level plugin screens published to frontendID and
// permitted for viewer, matching bootstrap.navigation = [{kind:"screen", id, title, icon?}].
func (s *Service) loadFavorites(ctx context.Context, viewer *core.User) (*favoriteSet, error) {
	fs := newFavoriteSet()
	if s.favorites == nil || viewer == nil {
		return fs, nil
	}
	keys, err := s.favorites.ListEntityKeys(ctx, viewer)
	if err != nil {
		return nil, err
	}
	for _, k := range keys {
		fs.add(k)
	}
	return fs, nil
}

func startOfDayUTC(t time.Time) time.Time {
	u := t.UTC()
	return time.Date(u.Year(), u.Month(), u.Day(), 0, 0, 0, 0, time.UTC)
}

// heroEventBefore ranks candidate events for the hero section:
// favorite live > close live (unless spoiler-hidden) > ranked/other live >
// favorite upcoming > other upcoming > favorite final > other final > other.
func stateOrder(state string) int {
	switch state {
	case core.LiveEventStateIn:
		return 0
	case core.LiveEventStatePre:
		return 1
	case core.LiveEventStateDelayed:
		return 2
	case core.LiveEventStatePost:
		return 3
	case core.LiveEventStatePostponed:
		return 4
	case core.LiveEventStateCanceled:
		return 5
	default:
		return 6
	}
}

func bestCompetitorRank(ev core.LiveEvent) int {
	best := 1 << 30
	for _, c := range ev.Competitors {
		if c.Rank > 0 && c.Rank < best {
			best = c.Rank
		}
	}
	return best
}

func parseEventStart(raw string) time.Time {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}
	}
	if t, err := time.Parse(time.RFC3339Nano, raw); err == nil {
		return t.UTC()
	}
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return t.UTC()
	}
	return time.Time{}
}

type favoriteSet struct {
	full       map[string]bool
	byFeed     map[string]map[string]bool // "pluginID:feed" -> { "kind:id", "id" }
	byFeedKind map[string][]string        // "pluginID:feed:kind" -> local entity IDs
}

func newFavoriteSet() *favoriteSet {
	return &favoriteSet{
		full:       map[string]bool{},
		byFeed:     map[string]map[string]bool{},
		byFeedKind: map[string][]string{},
	}
}

func (f *favoriteSet) add(rawKey string) {
	if f == nil {
		return
	}
	parsed, ok := favorites.ParseEntityKey(rawKey)
	if !ok {
		return
	}
	if f.full[rawKey] {
		return
	}
	f.full[rawKey] = true
	scope := parsed.PluginID + ":" + parsed.Feed
	m := f.byFeed[scope]
	if m == nil {
		m = map[string]bool{}
		f.byFeed[scope] = m
	}
	m[parsed.Kind+":"+parsed.ID] = true
	m[parsed.ID] = true
	kindScope := scope + ":" + parsed.Kind
	f.byFeedKind[kindScope] = append(f.byFeedKind[kindScope], parsed.ID)
}

func (f *favoriteSet) hasAnyForFeed(pluginID, feed string) bool {
	if f == nil || len(f.full) == 0 {
		return false
	}
	return len(f.byFeed[pluginID+":"+feed]) > 0
}

func (f *favoriteSet) localIDs(pluginID, feed, entityKind string) []string {
	if f == nil || len(f.full) == 0 {
		return nil
	}
	ids := f.byFeedKind[pluginID+":"+feed+":"+entityKind]
	if len(ids) == 0 {
		return nil
	}
	out := slices.Clone(ids)
	sort.Strings(out)
	return out
}

func (f *favoriteSet) hasKey(rawKey string) bool {
	return f != nil && f.full[rawKey]
}

func (f *favoriteSet) matchesEvent(pluginID, feed, entityKind string, ev core.LiveEvent) bool {
	if f == nil || len(f.full) == 0 {
		return false
	}
	matchEntityID := func(raw string) bool {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			return false
		}
		if f.full[raw] {
			return true
		}
		scope := pluginID + ":" + feed
		m := f.byFeed[scope]
		if len(m) == 0 {
			return false
		}
		if entityKind != "" && m[entityKind+":"+raw] {
			return true
		}
		return m[raw]
	}
	for _, c := range ev.Competitors {
		if matchEntityID(c.EntityID) {
			return true
		}
	}
	for _, r := range ev.Ranking {
		if matchEntityID(r.EntityID) {
			return true
		}
	}
	return false
}
