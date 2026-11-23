package core

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"strconv"
	"strings"
)

// RatingKey is the Plex ratingKey of an Ether item. It is derived from TMDB
// identifiers so a title keeps the same key before and after anything about
// it is resolved or cached.
//
// Layout (decimal, kept below 2^53 so JavaScript clients parse it safely):
//
//	kind * 10^15 + payload
//
//	KindMovie       payload = tmdb movie id
//	KindShow        payload = tmdb tv id
//	KindSeason      payload = show*10^4 + season           (season < 10^4)
//	KindEpisode     payload = show*10^7 + season*10^4 + ep  (show < 10^8, season < 1000, ep < 10^4)
//	KindCollection  payload = tmdb collection id (franchise)
//	KindSmart       payload = local smart-collection / generated hub id;
//	                payloads 701*10^12 through 703*10^12 are reserved
//	                for section-specific persisted collections;
//	                payloads in [EphemeralSmartBase, EphemeralSmartEnd)
//	                are reserved for ephemeral (in-memory, per-viewer)
//	                generated collections, and payloads from
//	                EphemeralSmartEnd up for personalize's Year in Review
//	                collections; database ids stay far below both
//	KindPlaylist    payload = local playlist id
//	KindExtra       payload = compact extra id from the extras registry
//	KindLocal       payload = local file id (spike / test library), or
//	                PluginKeyBase + seq for plugin items (spec §2.2)
type RatingKey int64

type KeyKind int

const (
	KindMovie      KeyKind = 1
	KindShow       KeyKind = 2
	KindSeason     KeyKind = 3
	KindEpisode    KeyKind = 4
	KindCollection KeyKind = 5
	KindSmart      KeyKind = 6
	KindPlaylist   KeyKind = 7
	KindExtra      KeyKind = 8
	KindLocal      KeyKind = 9
)

const keyBase = 1_000_000_000_000_000 // 10^15

// MaxTitleRuleKeys bounds the number of rating keys in one "title" visibility
// condition so compiled key IN (...) predicates stay bounded.
const MaxTitleRuleKeys = 500

// PluginKeyBase and PluginKeyMaxSeq carve out the KindLocal payload range
// [PluginKeyBase+1, PluginKeyBase+PluginKeyMaxSeq) for plugin items while
// keeping every RatingKey below 2^53 (spec §2.2).
const (
	PluginKeyBase   int64 = 5_000_000_000_000
	PluginKeyMaxSeq int64 = 2_000_000_000_000
	// PluginKeyOffset is the base integer added to plugin_items.seq to produce
	// its RatingKey: RatingKey(PluginKeyOffset + seq) == PluginKey(seq).
	PluginKeyOffset int64 = int64(KindLocal)*keyBase + PluginKeyBase

	// PluginPartKeyBase carves out [PluginPartKeyBase+PluginPartStride, ...)
	// inside KindLocal for synthetic per-part RatingKeys of multi-part plugin
	// items (hints.parts / fields.parts), keeping every key strictly below 2^53.
	PluginPartKeyBase int64 = 7_000_000_000_000
	PluginPartStride  int64 = 10_000
	PluginPartMaxSeq  int64 = 19_900_000
)

// PluginPersonIDBase and PluginPersonIDSpan carve out deterministic positive
// person IDs in (PluginPersonIDBase, PluginPersonIDBase+PluginPersonIDSpan)
// for cast credited on plugin items. Values stay far above TMDB person
// IDs (< 10^9) and safely below 2^53 for JavaScript clients.
const (
	PluginPersonIDBase int64 = 4_000_000_000_000
	PluginPersonIDSpan int64 = 1_000_000_000_000
)

// PluginPersonID derives a deterministic positive synthetic person ID from a
// person's display name (case- and whitespace-folded). Returns 0 when blank.
func PluginPersonID(name string) int64 {
	norm := strings.Join(strings.Fields(strings.ToLower(name)), " ")
	if norm == "" {
		return 0
	}
	sum := sha256.Sum256([]byte("plugin-person:" + norm))
	offset := binary.BigEndian.Uint64(sum[:8])%uint64(PluginPersonIDSpan-1) + 1
	return PluginPersonIDBase + int64(offset)
}

// IsPluginPersonID reports whether id is in the synthetic plugin-person ID range.
func IsPluginPersonID(id int64) bool {
	return id > PluginPersonIDBase && id < PluginPersonIDBase+PluginPersonIDSpan
}

func MovieKey(tmdb int64) RatingKey    { return RatingKey(int64(KindMovie)*keyBase + tmdb) }
func ShowKey(tmdb int64) RatingKey     { return RatingKey(int64(KindShow)*keyBase + tmdb) }
func CollectionKey(id int64) RatingKey { return RatingKey(int64(KindCollection)*keyBase + id) }
func SmartKey(id int64) RatingKey      { return RatingKey(int64(KindSmart)*keyBase + id) }
func PlaylistKey(id int64) RatingKey   { return RatingKey(int64(KindPlaylist)*keyBase + id) }
func ExtraKey(id int64) RatingKey      { return RatingKey(int64(KindExtra)*keyBase + id) }

func LocalKey(id int64) RatingKey {
	if id >= PluginKeyBase {
		panic(fmt.Sprintf("core.LocalKey: payload %d overlaps PluginKeyBase (%d)", id, PluginKeyBase))
	}
	return RatingKey(int64(KindLocal)*keyBase + id)
}

// PluginKey builds a plugin item RatingKey from a stored sequence number in
// [1, PluginKeyMaxSeq) (spec §2.2).
func PluginKey(seq int64) RatingKey {
	if seq < 1 || seq >= PluginKeyMaxSeq {
		panic(fmt.Sprintf("core.PluginKey: seq %d out of range [1, %d)", seq, PluginKeyMaxSeq))
	}
	return RatingKey(int64(KindLocal)*keyBase + PluginKeyBase + seq)
}

// IsPluginKey reports whether k is a plugin item RatingKey or a synthetic
// per-part RatingKey derived from a multi-part plugin item (spec §2.2).
func (k RatingKey) IsPluginKey() bool {
	return k.IsPluginItemKey() || k.IsPluginPartKey()
}

// IsPluginItemKey reports whether k is a stored plugin_items RatingKey.
func (k RatingKey) IsPluginItemKey() bool {
	if k.Kind() != KindLocal {
		return false
	}
	p := k.payload()
	return p > PluginKeyBase && p < PluginKeyBase+PluginKeyMaxSeq
}

// PluginPartKey builds a deterministic synthetic RatingKey for zero-based
// partIndex of a stored plugin item RatingKey. Every valid result stays below
// 2^53 so JavaScript clients parse it safely.
func PluginPartKey(parent RatingKey, partIndex int) (RatingKey, bool) {
	if !parent.IsPluginItemKey() || partIndex < 0 || int64(partIndex) >= PluginPartStride {
		return 0, false
	}
	seq := parent.PluginSeq()
	if seq < 1 || seq >= PluginPartMaxSeq {
		return 0, false
	}
	return RatingKey(int64(KindLocal)*keyBase + PluginPartKeyBase + seq*PluginPartStride + int64(partIndex)), true
}

// PluginPart unpacks a synthetic part RatingKey into its stored parent plugin
// RatingKey and zero-based partIndex.
func (k RatingKey) PluginPart() (parent RatingKey, partIndex int, ok bool) {
	if k.Kind() != KindLocal {
		return 0, 0, false
	}
	p := k.payload()
	if p < PluginPartKeyBase+PluginPartStride || p >= PluginPartKeyBase+PluginPartMaxSeq*PluginPartStride {
		return 0, 0, false
	}
	off := p - PluginPartKeyBase
	seq := off / PluginPartStride
	idx := int(off % PluginPartStride)
	if seq < 1 || seq >= PluginPartMaxSeq {
		return 0, 0, false
	}
	return PluginKey(seq), idx, true
}

// IsPluginPartKey reports whether k is a synthetic per-part plugin RatingKey.
func (k RatingKey) IsPluginPartKey() bool {
	_, _, ok := k.PluginPart()
	return ok
}

// PluginSeq returns the stored sequence number (1 .. PluginKeyMaxSeq-1) of a
// stored plugin item RatingKey, or 0 when k is not a stored plugin item key.
func (k RatingKey) PluginSeq() int64 {
	if !k.IsPluginItemKey() {
		return 0
	}
	return k.payload() - PluginKeyBase
}

// SectionCollectionKey gives a persisted smart collection a distinct Plex
// identity per library. The reserved range is below ephemeral smart keys.
const sectionCollectionBase int64 = 700_000_000_000_000
const sectionCollectionStride int64 = 1_000_000_000_000

func SectionCollectionKey(k RatingKey, t MediaType) RatingKey {
	if k.Kind() != KindSmart || k.payload() <= 0 || k.payload() >= sectionCollectionStride {
		return k
	}
	var n int64
	switch t {
	case TypeMovie:
		n = 1
	case TypeShow:
		n = 2
	default:
		return k
	}
	return SmartKey(sectionCollectionBase + n*sectionCollectionStride + k.payload())
}

func (k RatingKey) CollectionSection() (RatingKey, MediaType) {
	if k.Kind() != KindSmart {
		return k, ""
	}
	p := k.payload() - sectionCollectionBase
	if p <= sectionCollectionStride || p >= 3*sectionCollectionStride {
		return k, ""
	}
	id := p % sectionCollectionStride
	if id == 0 {
		return k, ""
	}
	switch p / sectionCollectionStride {
	case 1:
		return SmartKey(id), TypeMovie
	case 2:
		return SmartKey(id), TypeShow
	}
	return k, ""
}

// EphemeralSmartBase and EphemeralSmartEnd bound the KindSmart payloads
// reserved for ephemeral generated collections. The range stops where
// personalize's Year in Review collections begin (payload 9*10^14 and
// up), and database smart-collection ids stay far below it.
const (
	EphemeralSmartBase int64 = 800_000_000_000_000
	EphemeralSmartEnd  int64 = 900_000_000_000_000
)

// EphemeralSmartKey builds an ephemeral collection key from a sequence
// number (1 .. 10^14-1).
func EphemeralSmartKey(seq int64) RatingKey {
	return SmartKey(EphemeralSmartBase + seq)
}

// IsEphemeral reports whether k is an ephemeral generated collection key.
func (k RatingKey) IsEphemeral() bool {
	p := k.payload()
	return k.Kind() == KindSmart && p >= EphemeralSmartBase && p < EphemeralSmartEnd
}
func SeasonKey(show int64, season int) RatingKey {
	return RatingKey(int64(KindSeason)*keyBase + show*10_000 + int64(season))
}
func EpisodeKey(show int64, season, episode int) RatingKey {
	return RatingKey(int64(KindEpisode)*keyBase + show*10_000_000 + int64(season)*10_000 + int64(episode))
}

func (k RatingKey) Kind() KeyKind  { return KeyKind(int64(k) / keyBase) }
func (k RatingKey) payload() int64 { return int64(k) % keyBase }

// TMDB returns the TMDB id for movie, show, and collection keys, and the show
// id for season and episode keys.
func (k RatingKey) TMDB() int64 {
	switch k.Kind() {
	case KindSeason:
		return k.payload() / 10_000
	case KindEpisode:
		return k.payload() / 10_000_000
	default:
		return k.payload()
	}
}

// Season returns the season number for season and episode keys.
func (k RatingKey) Season() int {
	switch k.Kind() {
	case KindSeason:
		return int(k.payload() % 10_000)
	case KindEpisode:
		return int(k.payload() / 10_000 % 1000)
	}
	return 0
}

// Episode returns the episode number for episode keys.
func (k RatingKey) Episode() int {
	if k.Kind() == KindEpisode {
		return int(k.payload() % 10_000)
	}
	return 0
}

// Parent returns the season key of an episode and the show key of a season.
func (k RatingKey) Parent() RatingKey {
	switch k.Kind() {
	case KindEpisode:
		return SeasonKey(k.TMDB(), k.Season())
	case KindSeason:
		return ShowKey(k.TMDB())
	}
	return 0
}

// ExtraID returns the extras-registry id of an extra key.
func (k RatingKey) ExtraID() int64 {
	if k.Kind() == KindExtra {
		return k.payload()
	}
	return 0
}

func (k RatingKey) String() string { return strconv.FormatInt(int64(k), 10) }

func (k RatingKey) Valid() bool {
	switch k.Kind() {
	case KindMovie, KindShow, KindSeason, KindEpisode, KindCollection, KindSmart, KindPlaylist, KindExtra, KindLocal:
		return k.payload() > 0 || k.Kind() == KindSeason
	}
	return false
}

// IsTitleKey reports whether k is a catalogue movie, show, or non-plugin local
// title RatingKey (the keys stored in catalog_titles).
func (k RatingKey) IsTitleKey() bool {
	if !k.Valid() {
		return false
	}
	switch k.Kind() {
	case KindMovie, KindShow:
		return true
	case KindLocal:
		return k.payload() > 0 && k.payload() < PluginKeyBase
	}
	return false
}

func ParseRatingKey(s string) (RatingKey, error) {
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("ratingKey %q: %w", s, err)
	}
	k := RatingKey(n)
	if !k.Valid() {
		return 0, fmt.Errorf("ratingKey %q: unknown kind", s)
	}
	return k, nil
}
