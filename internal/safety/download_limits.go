package safety

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

func init() {
	// One row per title a viewer queued on a UTC day; bytes starts at the
	// estimated size reserved at admit and is reconciled when prepared and
	// completed. Limits are sums over a day or a month.
	store.Register("safety", 3, `CREATE TABLE safety_download_usage(user_id INTEGER NOT NULL, day TEXT NOT NULL, item_key INTEGER NOT NULL, bytes INTEGER NOT NULL DEFAULT 0, PRIMARY KEY(user_id, day, item_key));`)
	store.RegisterViewerTable("safety", "safety_download_usage", "user_id", store.ViewerMergeDrop)
	store.Register("safety", 4, `
		ALTER TABLE safety_download_usage ADD COLUMN offline_bytes BIGINT NOT NULL DEFAULT 0;
		CREATE INDEX safety_download_usage_item ON safety_download_usage(user_id, item_key, day);
		CREATE TABLE safety_offline_download_grants (
			user_id BIGINT NOT NULL, receipt TEXT NOT NULL, release_id TEXT NOT NULL,
			version_id TEXT NOT NULL, etag TEXT NOT NULL, PRIMARY KEY(user_id, receipt)
		);
	`)
	store.RegisterViewerTable("safety", "safety_offline_download_grants", "user_id", store.ViewerMergeDrop)
}

var _ core.DownloadLimitPolicy = (*Service)(nil)

func (s *Service) nowTime() time.Time {
	if s != nil && s.now != nil {
		return s.now().UTC()
	}
	return time.Now().UTC()
}

// SetDownloadDefaults installs the server-wide download limit, read on every
// decision so a saved setting applies at once.
func (s *Service) SetDownloadDefaults(fn func() core.DownloadAllowance) {
	s.mu.Lock()
	s.downloadDefaults = fn
	s.mu.Unlock()
}

// DownloadAllowance is the server default with u's overrides. The owner is
// never subject to download count/size limits or per-viewer quality caps,
// while the server-wide NoTranscode switch still applies.
func (s *Service) DownloadAllowance(u *core.User) core.DownloadAllowance {
	var out core.DownloadAllowance
	s.mu.RLock()
	fn := s.downloadDefaults
	s.mu.RUnlock()
	if fn != nil {
		out = fn()
	}
	if u == nil || u.IsOwner {
		return core.DownloadAllowance{NoTranscode: out.NoTranscode}
	}
	p := s.ForUser(u)
	switch p.DownloadLimitUnit {
	case "none":
		out.LimitUnit, out.LimitAmount, out.LimitPeriod = "", 0, ""
	case "titles", "gb":
		out.LimitUnit, out.LimitAmount, out.LimitPeriod = p.DownloadLimitUnit, p.DownloadLimitAmount, p.DownloadLimitPeriod
	}
	if out.LimitAmount <= 0 || (out.LimitUnit != "titles" && out.LimitUnit != "gb") {
		out.LimitUnit, out.LimitAmount = "", 0
	}
	if out.LimitPeriod != "month" {
		out.LimitPeriod = "day"
	}
	if p.DownloadMaxHeight > 0 && (out.MaxHeight == 0 || p.DownloadMaxHeight < out.MaxHeight) {
		out.MaxHeight = p.DownloadMaxHeight
	}
	out.NoTranscode = out.NoTranscode || p.DownloadTranscodingDisabled
	return out
}

// downloadPeriod is the half-open [from, to) range of UTC days the limit
// covers at now, as the day strings stored in safety_download_usage, plus the
// UTC instant when the period resets.
func downloadPeriod(period string, now time.Time) (day, from, to string, resetAt time.Time) {
	now = now.UTC()
	day = now.Format("2006-01-02")
	if period == "month" {
		first := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
		next := first.AddDate(0, 1, 0)
		return day, first.Format("2006-01-02"), next.Format("2006-01-02"), next
	}
	next := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, 1)
	return day, day, next.Format("2006-01-02"), next
}

// AdmitDownload reserves estimatedBytes against u's download usage today and
// reports whether u is within the limit. A title already counted today is
// free, so queuing it again, resuming a range download, or restarting it is
// not a new download. A storage error admits the download: a limit must not
// turn a database hiccup into a refusal.
func (s *Service) AdmitDownload(ctx context.Context, u *core.User, key core.RatingKey, estimatedBytes int64) core.DownloadLimitDecision {
	if u == nil || u.IsOwner || s.db == nil {
		return core.DownloadLimitDecision{Allowed: true}
	}
	s.downloadMu.Lock()
	defer s.downloadMu.Unlock()
	tx, err := s.beginDownload(ctx, u.ID)
	if err != nil {
		return core.DownloadLimitDecision{Allowed: true}
	}
	defer tx.Rollback()
	decision, err := s.admitDownload(ctx, tx, u, key, max(0, estimatedBytes), false, s.nowTime())
	if err != nil || tx.Commit() != nil {
		return core.DownloadLimitDecision{Allowed: true}
	}
	return decision
}

func (s *Service) beginDownload(ctx context.Context, user int64) (*sql.Tx, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	if err := store.LockKey(ctx, store.DialectOf(s.db), tx, fmt.Sprintf("download-usage/%d", user)); err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	return tx, nil
}

func (s *Service) admitDownload(ctx context.Context, tx *sql.Tx, u *core.User, key core.RatingKey, bytes int64, offline bool, now time.Time) (core.DownloadLimitDecision, error) {
	allowance := s.DownloadAllowance(u)
	day, from, to, resetAt := downloadPeriod(allowance.LimitPeriod, now)
	var existingBytes int64
	err := tx.QueryRowContext(ctx, `SELECT bytes FROM safety_download_usage WHERE user_id=? AND day=? AND item_key=?`, u.ID, day, int64(key)).Scan(&existingBytes)
	counted := err == nil
	if err != nil && err != sql.ErrNoRows {
		return core.DownloadLimitDecision{}, err
	}
	if counted && (!offline || bytes <= existingBytes) {
		return core.DownloadLimitDecision{Allowed: true}, nil
	}
	if allowance.LimitUnit != "" {
		var titles, usedBytes int64
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(bytes),0) FROM safety_download_usage WHERE user_id=? AND day>=? AND day<?`, u.ID, from, to).Scan(&titles, &usedBytes); err != nil {
			return core.DownloadLimitDecision{}, err
		}
		limitBytes := int64(allowance.LimitAmount) << 30
		if (allowance.LimitUnit == "titles" && !counted && titles >= int64(allowance.LimitAmount)) ||
			(allowance.LimitUnit == "gb" && (usedBytes >= limitBytes || bytes-existingBytes > limitBytes-usedBytes)) {
			return core.DownloadLimitDecision{Unit: allowance.LimitUnit, Period: allowance.LimitPeriod, ResetAt: resetAt}, nil
		}
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO safety_download_usage(user_id,day,item_key,bytes) VALUES(?,?,?,?)
		ON CONFLICT(user_id,day,item_key) DO UPDATE SET bytes=excluded.bytes`, u.ID, day, int64(key), bytes)
	return core.DownloadLimitDecision{Allowed: true, Reserved: !counted, Unit: allowance.LimitUnit, Period: allowance.LimitPeriod, ResetAt: resetAt}, err
}

// ReconcileDownloadBytes replaces the reserved estimate with the prepared
// download's actual size and checks that it still fits within the viewer's GB
// limit. When refused, the caller should abort the download and release the
// reservation if AdmitDownload inserted it.
func (s *Service) ReconcileDownloadBytes(ctx context.Context, u *core.User, key core.RatingKey, actualBytes int64) core.DownloadLimitDecision {
	if u == nil || u.IsOwner || s.db == nil || actualBytes <= 0 {
		return core.DownloadLimitDecision{Allowed: true}
	}
	s.downloadMu.Lock()
	defer s.downloadMu.Unlock()

	tx, err := s.beginDownload(ctx, u.ID)
	if err != nil {
		return core.DownloadLimitDecision{Allowed: true}
	}
	defer tx.Rollback()
	var rowDay string
	var offlineBytes int64
	if err := tx.QueryRowContext(ctx, `SELECT day, offline_bytes FROM safety_download_usage WHERE user_id=? AND item_key=? ORDER BY day DESC LIMIT 1`, u.ID, int64(key)).Scan(&rowDay, &offlineBytes); err != nil {
		return core.DownloadLimitDecision{Allowed: true}
	}
	actualBytes = max(actualBytes, offlineBytes)
	allowance := s.DownloadAllowance(u)
	if allowance.LimitUnit == "gb" {
		_, from, to, resetAt := downloadPeriod(allowance.LimitPeriod, s.nowTime())
		if rowDay >= from && rowDay < to {
			var otherBytes int64
			if err := tx.QueryRowContext(ctx, `SELECT COALESCE(SUM(bytes),0) FROM safety_download_usage WHERE user_id=? AND day>=? AND day<? AND NOT (day=? AND item_key=?)`, u.ID, from, to, rowDay, int64(key)).Scan(&otherBytes); err != nil {
				return core.DownloadLimitDecision{Allowed: true}
			}
			limitBytes := int64(allowance.LimitAmount) << 30
			if otherBytes+actualBytes > limitBytes {
				return core.DownloadLimitDecision{
					Allowed: false,
					Unit:    "gb",
					Period:  allowance.LimitPeriod,
					ResetAt: resetAt,
				}
			}
		}
	}
	_, _ = tx.ExecContext(ctx, `UPDATE safety_download_usage SET bytes=CASE WHEN offline_bytes>? THEN offline_bytes ELSE ? END WHERE user_id=? AND day=? AND item_key=?`, actualBytes, actualBytes, u.ID, rowDay, int64(key))
	_ = tx.Commit()
	return core.DownloadLimitDecision{Allowed: true}
}

// RecordDownloadBytes stores the final size of a finished download against the
// day it was queued, for the gigabyte limit.
func (s *Service) RecordDownloadBytes(ctx context.Context, u *core.User, key core.RatingKey, bytes int64) {
	if u == nil || u.IsOwner || s.db == nil || bytes <= 0 {
		return
	}
	s.downloadMu.Lock()
	defer s.downloadMu.Unlock()
	// The newest row for the title: a download queued before midnight UTC and
	// finished after it belongs to the day it was queued.
	_, _ = s.db.ExecContext(ctx, `UPDATE safety_download_usage SET bytes=CASE WHEN offline_bytes>? THEN offline_bytes ELSE ? END WHERE user_id=? AND item_key=? AND day=(SELECT MAX(day) FROM safety_download_usage WHERE user_id=? AND item_key=?)`, bytes, bytes, u.ID, int64(key), u.ID, int64(key))
}

// ReleaseDownloadReservation removes the newest usage row for an uncompleted
// download that failed, was refused during reconciliation, or was canceled
// before finishing. Callers invoke this only when AdmitDownload returned
// Reserved=true so previously completed downloads today stay counted.
func (s *Service) ReleaseDownloadReservation(ctx context.Context, u *core.User, key core.RatingKey) {
	if u == nil || u.IsOwner || s.db == nil {
		return
	}
	s.downloadMu.Lock()
	defer s.downloadMu.Unlock()
	_, _ = s.db.ExecContext(ctx, `DELETE FROM safety_download_usage WHERE user_id=? AND item_key=? AND offline_bytes=0 AND day=(SELECT MAX(day) FROM safety_download_usage WHERE user_id=? AND item_key=?)`, u.ID, int64(key), u.ID, int64(key))
}
