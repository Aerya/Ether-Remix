package frontend

import (
	"context"
	"time"

	"github.com/braedonsaunders/ether/internal/plexguid"
	plexmeta "github.com/braedonsaunders/ether/internal/plexmeta"
)

// LibraryGUIDs reads cached library identities and schedules host refreshes.
// ExactKey distinguishes ambiguous hierarchy identities from missing entries.
type LibraryGUIDs interface {
	Cached(context.Context, RatingKey) string
	CachedMany(context.Context, []RatingKey) map[RatingKey]string
	Schedule(...RatingKey)
	ExactKey(context.Context, string) (RatingKey, error)
	Lookup(context.Context, string) (RatingKey, error)
}

type LibraryCredit = plexguid.Credit
type LibraryTitleCredits = plexguid.TitleCredits
type LibraryPersonMapping = plexguid.PersonMapping

// IsLibraryPersonTagKey reports whether s is a real 24-hex provider person id.
func IsLibraryPersonTagKey(s string) bool { return plexguid.IsPersonTagKey(s) }

// LibraryCredits is the optional provider-credits and person-tagKey surface
// implemented by the GUID resolver.
type LibraryCredits interface {
	CachedCredits(context.Context, RatingKey, string) (LibraryTitleCredits, bool)
	CachedCreditsMany(context.Context, []RatingKey, map[RatingKey]string) map[RatingKey]LibraryTitleCredits
	ResolveCreditsBounded(context.Context, RatingKey, string, time.Duration) (LibraryTitleCredits, bool)
	RecordPersonMappings(context.Context, []LibraryPersonMapping)
	PersonByTagKey(context.Context, string) (LibraryPersonMapping, bool)
	PersonByTMDB(context.Context, int64) (LibraryPersonMapping, bool)
	PeopleByTMDB(context.Context, []int64) map[int64]LibraryPersonMapping
}

var ErrAmbiguousLibraryGUID = plexguid.ErrAmbiguous

// Metadata overlay values retain catalogue identity while enriching display.
type MetadataOverlay = plexmeta.Overlay
type CachedMetadataOverlay = plexmeta.Cached
type MetadataPerson = plexmeta.Person

const RemoteMetadataArt = plexmeta.RemoteArt

type LibraryMetadata interface {
	Cached(context.Context, RatingKey) (CachedMetadataOverlay, bool)
	CachedMany(context.Context, []RatingKey) map[RatingKey]CachedMetadataOverlay
	Schedule(...RatingKey)
}

func MetadataImageHost(host string) bool { return plexmeta.ImageHost(host) }
