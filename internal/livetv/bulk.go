package livetv

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

// ChannelBatch changes only the named fields, preserving stream credentials,
// programming and concurrent edits to unrelated fields. All IDs commit together.
type ChannelBatch struct {
	IDs      []string `json:"ids"`
	Enabled  *bool    `json:"enabled,omitempty"`
	Favorite *bool    `json:"favorite,omitempty"`
	Group    *string  `json:"group,omitempty"`
	// Numbers updates each keyed channel's display number in the same
	// transaction, preserving ProviderNumber so owner renumbering sticks across
	// provider refreshes.
	Numbers map[string]string `json:"numbers,omitempty"`
	// ShowApps and HideApps change only the named apps; each channel keeps
	// its own choice for every other app. ClearApps removes per-channel
	// overrides for the named apps so they inherit their group's rule again.
	ShowApps  []string `json:"showApps,omitempty"`
	HideApps  []string `json:"hideApps,omitempty"`
	ClearApps []string `json:"clearApps,omitempty"`
}

func (s *Service) UpdateChannels(ctx context.Context, change ChannelBatch) (int, error) {
	numbers := map[string]string{}
	if len(change.Numbers) > 0 {
		if len(change.Numbers) > 50000 {
			return 0, fmt.Errorf("select between 1 and 50000 channels")
		}
		numbers = make(map[string]string, len(change.Numbers))
		for id, raw := range change.Numbers {
			if id == "" {
				return 0, fmt.Errorf("channel id is required")
			}
			num := strings.TrimSpace(raw)
			if num == "" || len(num) > 32 {
				return 0, fmt.Errorf("channel number must be between 1 and 32 characters")
			}
			numbers[id] = num
		}
	}
	ids := slices.Clone(change.IDs)
	if len(ids) == 0 && len(numbers) > 0 {
		ids = make([]string, 0, len(numbers))
		for id := range numbers {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 || len(ids) > 50000 {
		return 0, fmt.Errorf("select between 1 and 50000 channels")
	}
	patch := map[string]any{}
	show, err := normalizeHiddenApps(change.ShowApps)
	if err != nil {
		return 0, err
	}
	hide, err := normalizeHiddenApps(change.HideApps)
	if err != nil {
		return 0, err
	}
	clearList, err := normalizeHiddenApps(change.ClearApps)
	if err != nil {
		return 0, err
	}
	for _, app := range show {
		if slices.Contains(hide, app) || slices.Contains(clearList, app) {
			return 0, fmt.Errorf("show or hide %s, not both", app)
		}
	}
	for _, app := range hide {
		if slices.Contains(clearList, app) {
			return 0, fmt.Errorf("hide or clear %s, not both", app)
		}
	}
	apps := len(show)+len(hide)+len(clearList) > 0
	if change.Enabled != nil {
		patch["enabled"] = *change.Enabled
	}
	if change.Favorite != nil {
		patch["favorite"] = *change.Favorite
	}
	if change.Group != nil {
		group := strings.TrimSpace(*change.Group)
		if len(group) > 200 {
			return 0, fmt.Errorf("group name is too long")
		}
		patch["group"] = group
		patch["customGroup"] = true
	}
	if len(patch) == 0 && !apps && len(numbers) == 0 {
		return 0, fmt.Errorf("choose a channel change")
	}
	slices.Sort(ids)
	ids = slices.Compact(ids)
	if ids[0] == "" {
		return 0, fmt.Errorf("channel id is required")
	}
	if len(numbers) > 0 {
		for id := range numbers {
			if _, ok := slices.BinarySearch(ids, id); !ok {
				return 0, fmt.Errorf("channel %s is not in ids", id)
			}
		}
	}
	// Read the owner-group index before opening the transaction: it uses
	// the pool, and on a single-connection database a pool read inside the
	// transaction waits for the transaction forever.
	var groupIdx *ownerGroupIndex
	if apps {
		groupIdx, _ = s.cachedOwnerGroupIndex(ctx)
	}
	tx, err := s.o.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if err := s.lockChannelBatchSources(ctx, tx, ids); err != nil {
		return 0, err
	}
	dialect := store.DialectOf(s.o.DB)
	if apps || len(numbers) > 0 {
		var current map[string]channelAppOverridePair
		if apps {
			current, err = channelAppOverrides(ctx, dialect, tx, ids)
			if err != nil {
				return 0, err
			}
		}
		perID := make([]channelPatchEntry, 0, len(ids))
		for _, id := range ids {
			item := make(map[string]any, len(patch)+3)
			for k, v := range patch {
				item[k] = v
			}
			if num, ok := numbers[id]; ok {
				item["number"] = num
			}
			if apps {
				cur := current[id]
				nextHidden := slices.DeleteFunc(slices.Clone(cur.hidden), func(app string) bool {
					return slices.Contains(show, app) || slices.Contains(clearList, app)
				})
				nextHidden, _ = normalizeHiddenApps(append(nextHidden, hide...))

				nextShown := slices.DeleteFunc(slices.Clone(cur.shown), func(app string) bool {
					return slices.Contains(hide, app) || slices.Contains(clearList, app) || slices.Contains(show, app)
				})
				for _, app := range show {
					if groupShow, hasRule := groupIdx.groupAppRule(cur.sourceID, cur.group, id, app); hasRule && !groupShow {
						nextShown = append(nextShown, app)
					}
				}
				nextShown, _ = normalizeHiddenApps(nextShown)

				item["hiddenApps"] = nextHidden
				item["shownApps"] = nextShown
			}
			payload, err := json.Marshal(item)
			if err != nil {
				return 0, err
			}
			perID = append(perID, channelPatchEntry{id: id, payload: string(payload)})
		}
		if err := patchChannelEntries(ctx, tx, dialect, perID); err != nil {
			return 0, err
		}
	} else {
		payload, err := json.Marshal(patch)
		if err != nil {
			return 0, err
		}
		expression := "json_patch(data,?)"
		if dialect == store.Postgres {
			expression = "(data::jsonb || ?::jsonb)::text"
		}
		if err := patchChannels(ctx, tx, expression, string(payload), ids); err != nil {
			return 0, err
		}
	}
	if err = tx.Commit(); err != nil {
		return 0, err
	}
	// Favourites, grouping, renumbering and app visibility do not interrupt
	// playback. Taking channels off air does.
	if change.Enabled != nil && !*change.Enabled {
		for _, id := range ids {
			s.invalidate(id, "")
		}
	}
	if err = s.refreshVirtualGuideIfWarm(ctx); err != nil {
		return 0, err
	}
	s.wakeSourceRefresh()
	s.wakeHealthSweep()
	s.wakeBreakLoop()
	s.wakePersonalLoop()
	s.bumpLineup()
	return len(ids), nil
}

type channelPatchEntry struct {
	id      string
	payload string
}

// patchChannelEntries applies per-channel JSON patches in chunks so large
// renumbers run in a few statements rather than firing per-statement triggers
// once per channel. Each chunk uses 3 parameters per row (<= 600 <= SQLite 999).
func patchChannelEntries(ctx context.Context, tx *sql.Tx, dialect store.Dialect, entries []channelPatchEntry) error {
	for start := 0; start < len(entries); start += 200 {
		batch := entries[start:min(start+200, len(entries))]
		args := make([]any, 0, len(batch)*3)
		var cases strings.Builder
		cases.WriteString("CASE id")
		for _, e := range batch {
			if dialect == store.Postgres {
				cases.WriteString(" WHEN ? THEN ?::jsonb")
			} else {
				cases.WriteString(" WHEN ? THEN ?")
			}
			args = append(args, e.id, e.payload)
		}
		cases.WriteString(" END")
		for _, e := range batch {
			args = append(args, e.id)
		}
		marks := strings.TrimSuffix(strings.Repeat("?,", len(batch)), ",")
		expr := "json_patch(data," + cases.String() + ")"
		if dialect == store.Postgres {
			expr = "(data::jsonb || (" + cases.String() + "))::text"
		}
		result, err := tx.ExecContext(ctx, "UPDATE livetv_channels SET data="+expr+" WHERE id IN ("+marks+")", args...)
		if err != nil {
			return err
		}
		n, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if n != int64(len(batch)) {
			return core.ErrNotFound
		}
	}
	return nil
}

func patchChannels(ctx context.Context, tx *sql.Tx, expression, payload string, ids []string) error {
	for start := 0; start < len(ids); start += 500 {
		batch := ids[start:min(start+500, len(ids))]
		args := make([]any, 0, len(batch)+1)
		args = append(args, payload)
		for _, id := range batch {
			args = append(args, id)
		}
		marks := strings.TrimSuffix(strings.Repeat("?,", len(batch)), ",")
		result, e := tx.ExecContext(ctx, "UPDATE livetv_channels SET data="+expression+" WHERE id IN ("+marks+")", args...)
		if e != nil {
			return e
		}
		n, e := result.RowsAffected()
		if e != nil {
			return e
		}
		if n != int64(len(batch)) {
			return core.ErrNotFound
		}
	}
	return nil
}

type channelAppOverridePair struct {
	sourceID string
	group    string
	hidden   []string
	shown    []string
}

// channelAppOverrides reads only the per-channel app override lists and group
// keys of the selected channels. A missing channel is reported by the update that follows.
func channelAppOverrides(ctx context.Context, d store.Dialect, tx *sql.Tx, ids []string) (map[string]channelAppOverridePair, error) {
	out := make(map[string]channelAppOverridePair, len(ids))
	sourceCol := "COALESCE(" + store.JSONText(d, "data", "sourceId") + ", '')"
	groupCol := "COALESCE(" + store.JSONText(d, "data", "group") + ", '')"
	hiddenCol := store.JSONText(d, "data", "hiddenApps")
	shownCol := store.JSONText(d, "data", "shownApps")
	for start := 0; start < len(ids); start += 500 {
		batch := ids[start:min(start+500, len(ids))]
		args := make([]any, len(batch))
		for i, id := range batch {
			args[i] = id
		}
		marks := strings.TrimSuffix(strings.Repeat("?,", len(batch)), ",")
		rows, err := tx.QueryContext(ctx, "SELECT id, "+sourceCol+", "+groupCol+", "+hiddenCol+", "+shownCol+" FROM livetv_channels WHERE id IN ("+marks+")", args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id string
			var pair channelAppOverridePair
			var rawHidden, rawShown sql.NullString
			if err := rows.Scan(&id, &pair.sourceID, &pair.group, &rawHidden, &rawShown); err != nil {
				rows.Close()
				return nil, err
			}
			if rawHidden.Valid && rawHidden.String != "" {
				if err := json.Unmarshal([]byte(rawHidden.String), &pair.hidden); err != nil {
					rows.Close()
					return nil, fmt.Errorf("live TV channel %s hiddenApps: %w", id, err)
				}
			}
			if rawShown.Valid && rawShown.String != "" {
				if err := json.Unmarshal([]byte(rawShown.String), &pair.shown); err != nil {
					rows.Close()
					return nil, fmt.Errorf("live TV channel %s shownApps: %w", id, err)
				}
			}
			out[id] = pair
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// Source refresh takes this same lock before reading owner overrides. Acquire
// every source in a stable order before touching channels, including sources
// whose row does not yet exist (hence advisory locks rather than row locks).
func (s *Service) lockSourceEdits(ctx context.Context, tx *sql.Tx, ids []string) error {
	ids = slices.Clone(ids)
	slices.Sort(ids)
	for _, id := range slices.Compact(ids) {
		if id == "" {
			continue
		}
		if err := store.LockKey(ctx, store.DialectOf(s.o.DB), tx, "livetv-source-edit:"+id); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) lockChannelBatchSources(ctx context.Context, tx *sql.Tx, ids []string) error {
	if store.DialectOf(s.o.DB) != store.Postgres {
		return nil
	}
	var sources []string
	for start := 0; start < len(ids); start += 500 {
		batch := ids[start:min(start+500, len(ids))]
		args := make([]any, len(batch))
		for i, id := range batch {
			args[i] = id
		}
		marks := strings.TrimSuffix(strings.Repeat("?,", len(batch)), ",")
		rows, err := tx.QueryContext(ctx, "SELECT DISTINCT COALESCE("+store.JSONText(store.Postgres, "data", "sourceId")+",'') FROM livetv_channels WHERE id IN ("+marks+")", args...)
		if err != nil {
			return err
		}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			sources = append(sources, id)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
	}
	return s.lockSourceEdits(ctx, tx, sources)
}
