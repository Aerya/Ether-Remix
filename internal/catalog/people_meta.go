package catalog

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/tmdb"
)

type personMetaEntry struct {
	meta core.PersonMetadata
	at   time.Time
}

func (c *Catalog) savePersonMetadata(personID int64, p *tmdb.Person) {
	if personID <= 0 || p == nil {
		return
	}
	meta := core.PersonMetadata{
		KnownFor:  strings.TrimSpace(p.KnownForDepartment),
		Biography: strings.TrimSpace(p.Biography),
		Birthday:  strings.TrimSpace(p.Birthday),
	}
	now := c.now()
	c.personMu.Lock()
	if c.personMeta == nil || len(c.personMeta) >= 2048 {
		c.personMeta = map[int64]personMetaEntry{}
	}
	c.personMeta[personID] = personMetaEntry{meta: meta, at: now}
	c.personMu.Unlock()
}

// PersonMetadata returns cached provider profile metadata for a person and,
// unless prefetch is true, schedules a non-blocking background refresh when
// stale or absent. Reads never wait on the provider.
func (c *Catalog) PersonMetadata(ctx context.Context, personID int64, prefetch bool) core.PersonMetadata {
	if personID <= 0 {
		return core.PersonMetadata{}
	}
	now := c.now()
	c.personMu.Lock()
	entry, ok := c.personMeta[personID]
	fresh := ok && now.Sub(entry.at) < 24*time.Hour
	c.personMu.Unlock()
	if !fresh && !prefetch {
		c.ensurePersonMetadata(ctx, personID)
	}
	return entry.meta
}

func (c *Catalog) ensurePersonMetadata(ctx context.Context, personID int64) {
	client := c.tmdb()
	if personID <= 0 || client == nil {
		return
	}
	pd, ok := client.(personDetailsSource)
	if !ok {
		return
	}
	_ = c.hydrate(ctx, "person-meta:"+strconv.FormatInt(personID, 10), 0, func(hctx context.Context) error {
		bctx, cancel := context.WithTimeout(hctx, 3*time.Second)
		defer cancel()
		p, err := pd.PersonDetails(bctx, personID)
		if err == nil && p != nil {
			c.savePersonMetadata(personID, p)
		}
		return nil
	})
}
