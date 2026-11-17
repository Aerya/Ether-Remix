package plugins

import (
	"context"
	"time"
)

func manifestRefreshInterval(r Record) time.Duration {
	if r.ManifestRefreshHours >= 1 && r.ManifestRefreshHours <= 168 {
		return time.Duration(r.ManifestRefreshHours) * time.Hour
	}
	return stremioManifestRefresh
}

// SetManifestRefresh sets the manifest polling interval, independently of list refreshes.
func (m *Manager) SetManifestRefresh(ctx context.Context, id string, hours int) error {
	if hours < 1 || hours > 168 {
		return &ValidationError{Fields: []FieldError{{Field: "manifestRefreshHours", Message: "Choose between 1 and 168 hours"}}}
	}
	r, err := m.store.record(ctx, id)
	if err != nil {
		return err
	}
	if !r.Manifest.IsStremioOnly() || r.SourceKind != "url" {
		return ErrUnavailable
	}
	_, err = m.store.db.ExecContext(ctx, `UPDATE plugins_installed SET manifest_refresh_hours=? WHERE id=?`, hours, id)
	return err
}
