package app

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/lru"
	"github.com/braedonsaunders/ether/internal/resolver"
)

// libraryProfileCacheEntries bounds the per-title library membership bitset.
// A title is asked for on every resolve, so the cache keeps the request path
// free of repeated filter SQL; the libraries, catalogue and config generations
// invalidate it on any edit.
const libraryProfileCacheEntries = 8192

// libraryBits stores lazy per-library membership bits over R (the set of
// libraries referenced by quality rules). Inline uint64 words avoid heap
// allocations for up to 64 referenced libraries.
type libraryBits struct {
	known0, member0     uint64
	knownExt, memberExt []uint64
}

func (b *libraryBits) has(pos int) bool {
	if pos < 64 {
		return (b.known0 & (uint64(1) << pos)) != 0
	}
	w := (pos - 64) / 64
	if w >= len(b.knownExt) {
		return false
	}
	return (b.knownExt[w] & (uint64(1) << (pos % 64))) != 0
}

func (b *libraryBits) is(pos int) bool {
	if pos < 64 {
		return (b.member0 & (uint64(1) << pos)) != 0
	}
	w := (pos - 64) / 64
	if w >= len(b.memberExt) {
		return false
	}
	return (b.memberExt[w] & (uint64(1) << (pos % 64))) != 0
}

func (b *libraryBits) set(pos int, in bool) {
	if pos < 64 {
		mask := uint64(1) << pos
		b.known0 |= mask
		if in {
			b.member0 |= mask
		} else {
			b.member0 &^= mask
		}
		return
	}
	w := (pos - 64) / 64
	for len(b.knownExt) <= w {
		b.knownExt = append(b.knownExt, 0)
		b.memberExt = append(b.memberExt, 0)
	}
	mask := uint64(1) << (pos % 64)
	b.knownExt[w] |= mask
	if in {
		b.memberExt[w] |= mask
	} else {
		b.memberExt[w] &^= mask
	}
}

// libraryProfiles implements resolver.LibraryProfiles: whether a title is in a
// library named by a quality rule. R is the union of libraries across all
// rules; membership over R is cached as a bitset per show key in an 8192-entry
// LRU, invalidated on library, catalogue, or config edits and a 30 s TTL.
type libraryProfiles struct {
	app *App

	mu         sync.Mutex
	gen        uint64
	catalogGen uint64
	configGen  uint64
	at         time.Time
	refIndex   map[core.LibraryID]int
	refLibs    map[core.LibraryID]core.Library
	cache      *lru.Cache[core.RatingKey, libraryBits]
}

func newLibraryProfiles(a *App) *libraryProfiles {
	return &libraryProfiles{
		app:   a,
		cache: lru.New[core.RatingKey, libraryBits](lru.Options[core.RatingKey, libraryBits]{MaxEntries: libraryProfileCacheEntries}),
	}
}

// membershipFor returns a resolver.Membership lookup backed by a.libProfiles.
func (a *App) membershipFor(ctx context.Context, t *core.Title) resolver.Membership {
	if a == nil || a.libProfiles == nil || t == nil {
		return nil
	}
	return func(id core.LibraryID) bool {
		return a.libProfiles.InLibrary(ctx, t, id)
	}
}

// InLibrary reports whether t is a member of library id.
func (l *libraryProfiles) InLibrary(ctx context.Context, t *core.Title, id core.LibraryID) bool {
	if l == nil || l.app == nil || l.app.libraries == nil || t == nil || id <= 0 {
		return false
	}
	// Plugin titles carry their owning library directly on the projection,
	// avoiding both a libraries read and a kindCatalog.Item lookup.
	if t.Key.IsPluginKey() && t.LibraryID > 0 {
		return t.LibraryID == id
	}
	key := libraryProfileKey(t)
	gen, catalogGen, configGen, pos, lib, haveLib, hit, member := l.lookup(key, id)
	if hit {
		return member
	}
	if !haveLib {
		loaded, err := l.app.libraries.Get(ctx, id)
		if err != nil {
			return false
		}
		lib = loaded
	}
	in, known := l.checkMembership(ctx, lib, t, key)
	if known && pos >= 0 {
		l.store(gen, catalogGen, configGen, key, id, pos, lib, in)
	}
	return in
}

func (l *libraryProfiles) generations() (libGen, catalogGen, configGen uint64) {
	if l.app.libraries != nil {
		libGen = l.app.libraries.Generation()
	}
	if l.app.catalog != nil {
		catalogGen = l.app.catalog.WriteGeneration()
	}
	if l.app.cfg != nil {
		configGen = l.app.cfg.Generation()
	}
	return libGen, catalogGen, configGen
}

func (l *libraryProfiles) lookup(key core.RatingKey, id core.LibraryID) (gen, catalogGen, configGen uint64, pos int, lib core.Library, haveLib, hit, member bool) {
	gen, catalogGen, configGen = l.generations()
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.refIndex == nil || l.gen != gen || l.configGen != configGen || l.catalogGen != catalogGen || time.Since(l.at) > 30*time.Second {
		l.cache.Clear()
		l.gen = gen
		l.catalogGen = catalogGen
		l.configGen = configGen
		l.at = time.Now()
		l.refIndex = l.buildRefIndex()
		l.refLibs = map[core.LibraryID]core.Library{}
	}
	pos, ok := l.refIndex[id]
	if !ok {
		pos = -1
		return gen, catalogGen, configGen, pos, core.Library{}, false, false, false
	}
	lib, haveLib = l.refLibs[id]
	if bits, found := l.cache.Get(key); found && bits.has(pos) {
		return gen, catalogGen, configGen, pos, lib, haveLib, true, bits.is(pos)
	}
	return gen, catalogGen, configGen, pos, lib, haveLib, false, false
}

func (l *libraryProfiles) buildRefIndex() map[core.LibraryID]int {
	q := l.app.cfg.Read().Quality
	var ids []core.LibraryID
	seen := map[core.LibraryID]bool{}
	for _, r := range q.Rules {
		for _, raw := range r.Libraries {
			id := core.LibraryID(raw)
			if id > 0 && !seen[id] {
				seen[id] = true
				ids = append(ids, id)
			}
		}
	}
	slices.Sort(ids)
	idx := make(map[core.LibraryID]int, len(ids))
	for i, id := range ids {
		idx[id] = i
	}
	return idx
}

func (l *libraryProfiles) store(gen, catalogGen, configGen uint64, key core.RatingKey, id core.LibraryID, pos int, lib core.Library, in bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.gen != gen || l.catalogGen != catalogGen || l.configGen != configGen {
		return
	}
	if l.refLibs == nil {
		l.refLibs = map[core.LibraryID]core.Library{}
	}
	l.refLibs[id] = lib
	bits, _ := l.cache.Get(key)
	bits.set(pos, in)
	l.cache.Add(key, bits)
}

// checkMembership evaluates whether t belongs to lib. The second return value
// distinguishes a definitive answer (cacheable) from an error or cancelled
// lookup (must not be cached so a subsequent call can retry).
func (l *libraryProfiles) checkMembership(ctx context.Context, lib core.Library, t *core.Title, key core.RatingKey) (in, known bool) {
	if lib.Source == core.LibraryPlugin {
		if !t.Key.IsPluginKey() {
			return false, true
		}
		if t.LibraryID > 0 {
			return t.LibraryID == lib.ID, true
		}
		if l.app.pluginsRT == nil || l.app.pluginsRT.kindCatalog == nil {
			return false, false
		}
		piv, err := l.app.pluginsRT.kindCatalog.Item(ctx, t.Key)
		if err != nil {
			if errors.Is(err, core.ErrNotFound) && ctx.Err() == nil {
				return false, true
			}
			return false, false
		}
		return piv.Library.ID == lib.ID || (piv.Library.PluginID == lib.PluginID && piv.Library.KindKey == lib.KindKey), true
	}
	if t.Key.IsPluginKey() {
		return false, true
	}
	media := titleMedia(t)
	if media == core.MediaAny {
		return false, true
	}
	if lib.MediaType != "" && string(lib.MediaType) != media {
		return false, true
	}
	if core.IsBuiltinLibrary(lib.ID) {
		if len(core.BuiltinLibraryExtraRules(lib.Rules)) == 0 && len(lib.Sources) == 0 {
			return true, true
		}
		if l.app.catalog == nil {
			return false, false
		}
		ok, err := l.app.catalog.TitleInFilter(ctx, libraryScopeFilter(lib, core.Filter{}), key)
		if err != nil {
			return false, false
		}
		return ok, true
	}
	if lib.Source != core.LibraryCatalog && lib.Source != core.LibraryCollection {
		return false, true
	}
	if lib.Source == core.LibraryCollection && lib.CollectionID <= 0 {
		return false, true
	}
	if l.app.catalog == nil {
		return false, false
	}
	ok, err := l.app.catalog.TitleInFilter(ctx, libraryScopeFilter(lib, core.Filter{}), key)
	if err != nil {
		return false, false
	}
	return ok, true
}

// titleMedia classifies a title as a movie or a show for library matching.
func titleMedia(t *core.Title) string {
	switch t.Type {
	case core.TypeMovie:
		return core.MediaMovie
	case core.TypeShow, core.TypeSeason, core.TypeEpisode:
		return string(core.TypeShow)
	}
	switch t.Key.Kind() {
	case core.KindMovie:
		return core.MediaMovie
	case core.KindShow, core.KindSeason, core.KindEpisode:
		return string(core.TypeShow)
	}
	return core.MediaAny
}

// Seasons and episodes inherit membership and the cached profile of their show.
func libraryProfileKey(t *core.Title) core.RatingKey {
	if (t.Type == core.TypeSeason || t.Type == core.TypeEpisode) && t.ShowKey != 0 {
		return t.ShowKey
	}
	switch t.Key.Kind() {
	case core.KindSeason, core.KindEpisode:
		return core.ShowKey(t.Key.TMDB())
	}
	return t.Key
}

// PolicyKey binds durable feed evidence to the quality rules and the filters
// of the libraries those rules reference.
func (l *libraryProfiles) PolicyKey(ctx context.Context) string {
	q := l.app.cfg.Read().Quality
	refSet := map[core.LibraryID]bool{}
	for _, r := range q.Rules {
		for _, id := range r.Libraries {
			refSet[core.LibraryID(id)] = true
		}
	}
	var refLibs []core.Library
	if len(refSet) > 0 && l.app.libraries != nil {
		libs, err := l.app.libraries.List(ctx)
		if err != nil {
			return "unavailable"
		}
		for _, lib := range libs {
			if refSet[lib.ID] {
				refLibs = append(refLibs, lib)
			}
		}
	}
	rules := slices.Clone(q.Rules)
	for i := range rules {
		rules[i].From = nil
	}
	b, _ := json.Marshal(struct {
		Default           string               `json:"default"`
		DefaultAway       string               `json:"defaultAway,omitempty"`
		DefaultLimits     config.QualityLimits `json:"defaultLimits,omitzero"`
		DefaultAwayLimits config.QualityLimits `json:"defaultAwayLimits,omitzero"`
		Rules             []config.QualityRule `json:"rules"`
		Libraries         any                  `json:"libraries,omitempty"`
	}{
		Default:           q.Default,
		DefaultAway:       q.DefaultAway,
		DefaultLimits:     q.DefaultLimits,
		DefaultAwayLimits: q.DefaultAwayLimits,
		Rules:             rules,
		Libraries:         refLibs,
	})
	return fmt.Sprintf("%x", sha256.Sum256(b))
}

func (a *App) playbackProfile(caps core.ClientCaps, user *core.User, key core.RatingKey) core.QualityProfile {
	ctx := context.Background()
	// Plugin items open their own ByteSource; video release filters have no meaning.
	if key.IsPluginKey() {
		return core.QualityProfile{}
	}
	// Encoded keys identify movie/show and the parent show without hydrating
	// metadata or calling a catalogue provider on the playback path.
	return a.resolver.ProfileForTitle(ctx, caps, user, &core.Title{Key: key})
}
