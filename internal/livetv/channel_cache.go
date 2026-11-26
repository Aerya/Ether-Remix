package livetv

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/store"
)

// The channel snapshot keeps every request path off a full table decode.
// A large IPTV lineup is tens of thousands of channels, and every Plex
// lineup, grid, hub and tune request needs the channel list. Requests share
// one decoded, sorted snapshot that is rebuilt only when livetv_channels or
// livetv_owner_groups changes. Triggers bump livetv_channel_epoch on every
// write to either table, from any writer and any cluster node, so one indexed
// read per request decides whether both the channel snapshot and compiled
// owner-group rules are current.

const channelEpochSchema = `CREATE TABLE livetv_channel_epoch (id INTEGER PRIMARY KEY CHECK(id=1), generation BIGINT NOT NULL); INSERT INTO livetv_channel_epoch(id,generation) VALUES(1,1);`

func init() {
	sqlite := channelEpochSchema
	for _, event := range []string{"INSERT", "UPDATE", "DELETE"} {
		sqlite += fmt.Sprintf(` CREATE TRIGGER livetv_channel_epoch_%s AFTER %s ON livetv_channels BEGIN UPDATE livetv_channel_epoch SET generation=generation+1 WHERE id=1; END;`, strings.ToLower(event), event)
	}
	store.Register("livetv", 6, sqlite)
	store.RegisterDialect("livetv", 6, store.Postgres, channelEpochSchema+` CREATE FUNCTION livetv_channel_epoch_bump() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN UPDATE livetv_channel_epoch SET generation=generation+1 WHERE id=1; RETURN NULL; END $$;
CREATE TRIGGER livetv_channel_epoch AFTER INSERT OR UPDATE OR DELETE ON livetv_channels FOR EACH STATEMENT EXECUTE FUNCTION livetv_channel_epoch_bump();`)
	store.RegisterRestore("livetv_channel_epoch", restoreChannelEpoch, "livetv_channel_epoch")
}

// restoreChannelEpoch moves the epoch past both the restored and the live
// value, so no node keeps a snapshot of the pre-restore lineup.
func restoreChannelEpoch(ctx context.Context, tx *sql.Tx) (func(context.Context, *sql.Tx) error, error) {
	var before int64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(generation),0) FROM livetv_channel_epoch`).Scan(&before); err != nil {
		return nil, err
	}
	return func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO livetv_channel_epoch(id,generation) VALUES(1,?)
			ON CONFLICT(id) DO UPDATE SET generation=CASE WHEN livetv_channel_epoch.generation>=excluded.generation THEN livetv_channel_epoch.generation+1 ELSE excluded.generation END`, before+1)
		return err
	}, nil
}

type channelSnapshot struct {
	mu         sync.Mutex
	generation int64
	list       []Channel // sorted by number; never mutated once published
	archived   []Channel
	archiveIDs map[uint64]string
	bySource   map[string][]int
	byID       map[string]int
}

// allChannels returns every channel in number order. The slice is the
// caller's; the channels' nested slices are shared and must not be written
// in place.
func (s *Service) allChannels(ctx context.Context) ([]Channel, error) {
	list, _, err := s.channelSnapshot(ctx)
	if err != nil {
		return nil, err
	}
	return slices.Clone(list), nil
}

func (s *Service) channelSnapshot(ctx context.Context) ([]Channel, *channelSnapshot, error) {
	prevGroupRev, prevGroupIdx := s.getOwnerGroupCacheState()
	var gen int64
	var rawGroups string
	if err := s.o.DB.QueryRowContext(ctx,
		`SELECT generation, CASE WHEN generation=? THEN '' ELSE COALESCE((SELECT data FROM livetv_owner_groups WHERE id=1),'[]') END FROM livetv_channel_epoch WHERE id=1`,
		prevGroupRev,
	).Scan(&gen, &rawGroups); err != nil {
		return nil, nil, err
	}
	c := &s.chanSnap
	c.mu.Lock()
	var list []Channel
	if c.list != nil && c.generation == gen {
		list = c.list
		c.mu.Unlock()
	} else {
		// The epoch is read before the rows: a write in between leaves an older
		// generation on a newer list, which the next request rebuilds.
		loaded, err := channels(ctx, s.o.DB)
		if err != nil {
			c.mu.Unlock()
			return nil, nil, err
		}
		c.generation, c.list = gen, loaded
		c.byID = make(map[string]int, len(loaded))
		c.bySource = map[string][]int{}
		c.archived = nil
		c.archiveIDs = make(map[uint64]string)
		for i, ch := range loaded {
			if ch.ArchiveDays() > 0 && ch.Kind == "iptv" {
				c.archived = append(c.archived, ch)
				key := frontend.CatchUpChannelKey(ch.ID)
				if _, collision := c.archiveIDs[key]; collision {
					c.archiveIDs[key] = ""
				} else {
					c.archiveIDs[key] = ch.ID
				}
			}
			c.byID[ch.ID] = i
			c.bySource[ch.SourceID] = append(c.bySource[ch.SourceID], i)
		}
		list = loaded
		c.mu.Unlock()
	}
	if prevGroupIdx == nil || gen != prevGroupRev {
		idx, err := s.loadOwnerGroupIndex(ctx, gen, rawGroups)
		if err != nil {
			return nil, nil, err
		}
		s.setOwnerGroupCacheState(gen, idx)
	}
	return list, c, nil
}

// numberKey orders channel numbers naturally: 2 < 10, 4.1 < 4.2 < 5.
type numberKey struct {
	major, minor int
	raw          string
}

func parseNumberKey(n string) numberKey {
	k := numberKey{raw: n}
	major, rest, _ := strings.Cut(strings.TrimSpace(n), ".")
	k.major = leadingInt(major)
	k.minor = leadingInt(rest)
	return k
}

func leadingInt(s string) int {
	end := 0
	for end < len(s) && s[end] >= '0' && s[end] <= '9' {
		end++
	}
	n, _ := strconv.Atoi(s[:end])
	return n
}

func (a numberKey) compare(b numberKey) int {
	if a.major != b.major {
		return a.major - b.major
	}
	if a.minor != b.minor {
		return a.minor - b.minor
	}
	return strings.Compare(a.raw, b.raw)
}

// sortChannels sorts by natural channel number, stable on the input order.
func sortChannels(cs []Channel) {
	keys := make([]numberKey, len(cs))
	idx := make([]int, len(cs))
	for i := range cs {
		keys[i] = parseNumberKey(cs[i].Number)
		idx[i] = i
	}
	slices.SortStableFunc(idx, func(a, b int) int { return keys[a].compare(keys[b]) })
	sorted := make([]Channel, len(cs))
	for i, j := range idx {
		sorted[i] = cs[j]
	}
	copy(cs, sorted)
}

func decodeChannelRows(rows *sql.Rows) ([]Channel, error) {
	defer rows.Close()
	out := []Channel{}
	for rows.Next() {
		var b []byte
		var c Channel
		if err := rows.Scan(&b); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(b, &c); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// importSnapshot is every stored channel as a source import reads and
// decodes it before taking the writer, with the channel epoch it was read
// at. Every write to livetv_channels advances that epoch (triggers, from any
// writer or node), so inside the import's write transaction an unchanged
// epoch proves the rows are still these and nothing is read or decoded
// there; a changed epoch reads them again in the transaction. The decoded
// list is the import's own, never a shared snapshot.
type importSnapshot struct {
	gen  int64
	list []Channel // as channels() returns them
	// raw is the stored row of every IPTV channel and of every channel of
	// the importing source: what the import diffs against and what
	// duplicate-backup matching decodes afresh.
	raw map[string][]byte
}

func readImportSnapshot(ctx context.Context, db *sql.DB, sourceID string) (*importSnapshot, error) {
	p := &importSnapshot{raw: map[string][]byte{}}
	// The epoch is read before the rows: a write in between leaves an older
	// epoch on newer rows, which the transaction then reads again.
	if err := db.QueryRowContext(ctx, `SELECT generation FROM livetv_channel_epoch WHERE id=1`).Scan(&p.gen); err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, `SELECT data FROM livetv_channels ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	p.list = []Channel{}
	for rows.Next() {
		var b []byte
		var c Channel
		if err := rows.Scan(&b); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(b, &c); err != nil {
			return nil, err
		}
		if c.Kind == "iptv" || c.SourceID == sourceID {
			p.raw[c.ID] = b
		}
		p.list = append(p.list, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sortChannels(p.list)
	return p, nil
}

// current returns every channel as tx sees it, and whether that is the
// snapshot (so its rows may stand in for reads in tx).
func (p *importSnapshot) current(ctx context.Context, tx *sql.Tx) ([]Channel, bool, error) {
	var gen int64
	if err := tx.QueryRowContext(ctx, `SELECT generation FROM livetv_channel_epoch WHERE id=1`).Scan(&gen); err != nil {
		return nil, false, err
	}
	if p != nil && gen == p.gen {
		return p.list, true, nil
	}
	list, err := channels(ctx, tx)
	return list, false, err
}

// sourceRows is the stored row of every channel of sourceID, as
// writeSourceChannels would read them.
func (p *importSnapshot) sourceRows(sourceID string) map[string][]byte {
	out := map[string][]byte{}
	for _, c := range p.list {
		if c.SourceID == sourceID {
			out[c.ID] = p.raw[c.ID]
		}
	}
	return out
}

// iptvAfter is every IPTV channel, freshly decoded and in channels() order,
// as livetv_channels holds them once an import of sourceID has written
// written (id to stored row) and deleted its other channels. channels()
// orders ids bytewise only on SQLite (BINARY collation), so only SQLite
// callers use it.
func (p *importSnapshot) iptvAfter(sourceID string, written map[string][]byte) ([]Channel, error) {
	byID := make(map[string][]byte, len(p.raw)+len(written))
	for _, c := range p.list {
		if c.Kind == "iptv" && c.SourceID != sourceID {
			byID[c.ID] = p.raw[c.ID]
		}
	}
	for id, b := range written {
		byID[id] = b
	}
	ids := make([]string, 0, len(byID))
	for id := range byID {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	out := make([]Channel, 0, len(ids))
	for _, id := range ids {
		var c Channel
		if err := json.Unmarshal(byID[id], &c); err != nil {
			return nil, err
		}
		if c.Kind == "iptv" {
			out = append(out, c)
		}
	}
	sortChannels(out)
	return out, nil
}
