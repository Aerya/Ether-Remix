package personalize

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"github.com/braedonsaunders/ether/internal/core"
)

// Pre-pages snapshots kept collection row placements in a separate part.
// Convert both parts together, retaining the current collection audience.
func (e *Engine) prepareLegacyTemplate(ctx context.Context, tx *sql.Tx, raw json.RawMessage, parts map[string]json.RawMessage) (json.RawMessage, error) {
	var shape map[string]json.RawMessage
	if err := json.Unmarshal(raw, &shape); err != nil {
		return nil, err
	}
	if _, ok := shape["pages"]; ok {
		return raw, nil
	}
	var items []struct {
		ID      int64
		Fields  map[string]json.RawMessage
		Targets struct {
			HomeRow    bool `json:"homeRow"`
			SectionRow bool `json:"sectionRow"`
			Schedule   *struct {
				core.Schedule
				StartDate string   `json:"startDate"`
				EndDate   string   `json:"endDate"`
				Devices   []string `json:"devices"`
			} `json:"schedule"`
		}
	}
	if len(parts["collections"]) > 0 {
		if err := json.Unmarshal(parts["collections"], &items); err != nil {
			return nil, err
		}
	}
	var placements []LegacyCollectionPlacement
	for _, item := range items {
		var slug, specJSON string
		if err := tx.QueryRowContext(ctx, `SELECT slug,spec_json FROM collections_specs WHERE id=?`, item.ID).Scan(&slug, &specJSON); err != nil {
			return nil, err
		}
		var sp core.CollectionSpec
		if err := json.Unmarshal([]byte(specJSON), &sp); err != nil {
			return nil, err
		}
		if b, ok := item.Fields["enabled"]; ok {
			if err := json.Unmarshal(b, &sp.Enabled); err != nil {
				return nil, err
			}
		}
		id := slug
		if id == "" {
			id = fmt.Sprint(item.ID)
		}
		p := LegacyCollectionPlacement{ID: id, MediaType: sp.MediaType, Enabled: sp.Enabled, HomeRow: item.Targets.HomeRow, SectionRow: item.Targets.SectionRow}
		if sp.Users != nil && !sp.Users.All {
			p.Viewers = append([]int64{}, sp.Users.IDs...)
		}
		if sc := item.Targets.Schedule; sc != nil {
			p.When = &sc.Schedule
			if sc.StartDate != "" || sc.EndDate != "" {
				p.When.Dates = []core.DateRange{{From: sc.StartDate, To: sc.EndDate}}
			}
			p.Devices = sc.Devices
		}
		placements = append(placements, p)
	}
	pages, err := e.convertLegacyLayout(ctx, string(raw), placements)
	if err != nil {
		return nil, err
	}
	return json.Marshal(struct {
		Pages []core.RowPage `json:"pages"`
	}{pages})
}
