package livetv

import (
	"context"
	"database/sql"
	"strings"

	"github.com/braedonsaunders/ether/internal/store"
)

const guideVisibilityHistoricalSchema = `CREATE TABLE livetv_guide_visibility(kind TEXT NOT NULL,a TEXT NOT NULL,b TEXT NOT NULL,PRIMARY KEY(kind,a,b));`

func init() {
	store.Register("livetv", 23, guideVisibilityHistoricalSchema+`
 INSERT INTO livetv_guide_visibility SELECT 'configured','','' FROM livetv_owner_guides WHERE id=1;
 INSERT OR IGNORE INTO livetv_guide_visibility SELECT 'all','','' FROM livetv_owner_guides o,json_each(o.data) g WHERE coalesce(json_extract(g.value,'$.hidden'),0)=0 AND json_extract(g.value,'$.all')=1;
 INSERT OR IGNORE INTO livetv_guide_visibility SELECT 'group',json_extract(m.value,'$.sourceId'),json_extract(m.value,'$.name') FROM livetv_owner_guides o,json_each(o.data) g,json_each(g.value,'$.groups') m WHERE coalesce(json_extract(g.value,'$.hidden'),0)=0;
 INSERT OR IGNORE INTO livetv_guide_visibility SELECT 'channel',m.value,'' FROM livetv_owner_guides o,json_each(o.data) g,json_each(g.value,'$.channels') m WHERE coalesce(json_extract(g.value,'$.hidden'),0)=0;
 `)
	store.RegisterDialect("livetv", 23, store.Postgres, guideVisibilityHistoricalSchema+`
 INSERT INTO livetv_guide_visibility SELECT 'configured','','' FROM livetv_owner_guides WHERE id=1;
 INSERT INTO livetv_guide_visibility SELECT 'all','','' FROM livetv_owner_guides o CROSS JOIN LATERAL jsonb_array_elements(o.data::jsonb) g WHERE coalesce((g->>'hidden')::boolean,false)=false AND (g->>'all')::boolean=true ON CONFLICT DO NOTHING;
 INSERT INTO livetv_guide_visibility SELECT 'group',m->>'sourceId',m->>'name' FROM livetv_owner_guides o CROSS JOIN LATERAL jsonb_array_elements(o.data::jsonb) g CROSS JOIN LATERAL jsonb_array_elements(g->'groups') m WHERE coalesce((g->>'hidden')::boolean,false)=false ON CONFLICT DO NOTHING;
 INSERT INTO livetv_guide_visibility SELECT 'channel',m #>> '{}','' FROM livetv_owner_guides o CROSS JOIN LATERAL jsonb_array_elements(o.data::jsonb) g CROSS JOIN LATERAL jsonb_array_elements(g->'channels') m WHERE coalesce((g->>'hidden')::boolean,false)=false ON CONFLICT DO NOTHING;
 `)
}

func saveGroupVisibility(ctx context.Context, tx *sql.Tx, gs []OwnerGroup) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM livetv_group_visibility`); err != nil {
		return err
	}
	insert := func(kind, a, b string) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO livetv_group_visibility(kind,a,b) VALUES(?,?,?) ON CONFLICT DO NOTHING`, kind, a, b)
		return err
	}
	for _, g := range gs {
		if len(g.Rules) > 0 {
			continue
		}
		prefix := ""
		if g.Hidden {
			prefix = "hidden_"
		}
		if g.All {
			if err := insert(prefix+"all", "", ""); err != nil {
				return err
			}
		}
		for _, group := range g.Groups {
			if err := insert(prefix+"group", group.SourceID, group.Name); err != nil {
				return err
			}
		}
		for _, id := range g.Channels {
			if err := insert(prefix+"channel", id, ""); err != nil {
				return err
			}
		}
	}
	return nil
}

// Indexed membership checks touch only search candidates, even if a saved group
// explicitly selects all 10,000 channels. Overlaps are a union, never duplicates;
// channels in a Hidden group are omitted unless a visible group also includes them,
// while ungrouped channels remain searchable.
func (s *Service) searchVisibleGroupIDs(ctx context.Context, ids []string) (map[string]bool, error) {
	visible := map[string]bool{}
	d := store.DialectOf(s.o.DB)
	sourceExpr := `coalesce(` + store.JSONText(d, "c.data", "sourceId") + `,'')`
	groupExpr := `coalesce(` + store.JSONText(d, "c.data", "group") + `,'')`
	if idx, err := s.cachedOwnerGroupIndex(ctx); err == nil && idx != nil && idx.hasRules {
		for start := 0; start < len(ids); start += 400 {
			chunk := ids[start:min(start+400, len(ids))]
			args := make([]any, len(chunk))
			for i, id := range chunk {
				args[i] = id
			}
			query := `SELECT c.id, ` + sourceExpr + `, ` + groupExpr + ` FROM livetv_channels c WHERE c.id IN (` + strings.TrimSuffix(strings.Repeat("?,", len(chunk)), ",") + `)`
			rows, err := s.o.DB.QueryContext(ctx, query, args...)
			if err != nil {
				return nil, err
			}
			for rows.Next() {
				var id, srcID, grp string
				if err = rows.Scan(&id, &srcID, &grp); err != nil {
					break
				}
				if len(idx.memberships(srcID, grp, id)) > 0 || !idx.inHiddenGroup(srcID, grp, id) {
					visible[id] = true
				}
			}
			re := rows.Err()
			rows.Close()
			if err != nil {
				return nil, err
			}
			if re != nil {
				return nil, re
			}
		}
		return visible, nil
	}
	for start := 0; start < len(ids); start += 400 {
		chunk := ids[start:min(start+400, len(ids))]
		args := make([]any, len(chunk))
		for i, id := range chunk {
			args[i] = id
		}
		query := `SELECT c.id FROM livetv_channels c WHERE c.id IN (` + strings.TrimSuffix(strings.Repeat("?,", len(chunk)), ",") + `) AND (` +
			`EXISTS(SELECT 1 FROM livetv_group_visibility WHERE kind='all')` +
			` OR EXISTS(SELECT 1 FROM livetv_group_visibility WHERE kind='channel' AND a=c.id AND b='')` +
			` OR EXISTS(SELECT 1 FROM livetv_group_visibility WHERE kind='group' AND a=` + sourceExpr + ` AND b=` + groupExpr + `)` +
			` OR (NOT EXISTS(SELECT 1 FROM livetv_group_visibility WHERE kind='hidden_all')` +
			` AND NOT EXISTS(SELECT 1 FROM livetv_group_visibility WHERE kind='hidden_channel' AND a=c.id AND b='')` +
			` AND NOT EXISTS(SELECT 1 FROM livetv_group_visibility WHERE kind='hidden_group' AND a=` + sourceExpr + ` AND b=` + groupExpr + `)))`
		rows, err := s.o.DB.QueryContext(ctx, query, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id string
			if err = rows.Scan(&id); err != nil {
				break
			}
			visible[id] = true
		}
		re := rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
		if re != nil {
			return nil, re
		}
	}
	return visible, nil
}
