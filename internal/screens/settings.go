package screens

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

// Surface settings are a viewer's player-menu switches for player overlays
// ("Score", "Scores ticker"). They live with the viewer's other Vela
// preferences, so they follow the profile across devices.

const (
	settingsSurface = "vela"
	settingsClient  = "screens"
	settingsPrefix  = "surface:"
)

// SurfaceSettings reads and writes the switches.
type SurfaceSettings struct {
	DB  *sql.DB
	Now func() time.Time
}

// Load returns the viewer's remembered switches by surface wire id.
func (s SurfaceSettings) Load(ctx context.Context, viewerID int64) (map[string]bool, error) {
	out := map[string]bool{}
	if s.DB == nil || viewerID <= 0 {
		return out, nil
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT pref_key, value FROM frontend_preferences WHERE viewer_id=? AND surface=? AND client=?`, viewerID, settingsSurface, settingsClient)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return nil, err
		}
		if id, ok := strings.CutPrefix(key, settingsPrefix); ok {
			out[id] = value == "true"
		}
	}
	return out, rows.Err()
}

// Set remembers one switch.
func (s SurfaceSettings) Set(ctx context.Context, viewerID int64, surfaceID string, enabled bool) error {
	if s.DB == nil || viewerID <= 0 {
		return nil
	}
	now := time.Now
	if s.Now != nil {
		now = s.Now
	}
	value := "false"
	if enabled {
		value = "true"
	}
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO frontend_preferences(viewer_id,surface,client,pref_key,value,revision,updated_at) VALUES(?,?,?,?,?,1,?)
		 ON CONFLICT(viewer_id,surface,client,pref_key) DO UPDATE SET value=excluded.value,revision=frontend_preferences.revision+1,updated_at=excluded.updated_at`,
		viewerID, settingsSurface, settingsClient, settingsPrefix+surfaceID, value, now().UnixMilli())
	return err
}
