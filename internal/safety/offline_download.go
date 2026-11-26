package safety

import (
	"context"
	"database/sql"

	"github.com/braedonsaunders/ether/internal/core"
)

type downloadReceiptReader interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func offlineDownloadReceipt(ctx context.Context, db downloadReceiptReader, user int64, receipt string) (core.OfflineDownloadBinding, error) {
	var b core.OfflineDownloadBinding
	err := db.QueryRowContext(ctx, `SELECT release_id, version_id, etag FROM safety_offline_download_grants WHERE user_id=? AND receipt=?`, user, receipt).Scan(&b.Release, &b.VersionID, &b.ETag)
	if err == sql.ErrNoRows {
		err = nil
	}
	return b, err
}

func (s *Service) OfflineDownloadReceipt(ctx context.Context, u *core.User, receipt string) (core.OfflineDownloadBinding, error) {
	return offlineDownloadReceipt(ctx, s.db, u.ID, receipt)
}

func (s *Service) AdmitOfflineDownload(ctx context.Context, u *core.User, key core.RatingKey, receipt string, binding core.OfflineDownloadBinding, bytes int64) (core.DownloadLimitDecision, error) {
	s.downloadMu.Lock()
	defer s.downloadMu.Unlock()
	tx, err := s.beginDownload(ctx, u.ID)
	if err != nil {
		return core.DownloadLimitDecision{}, err
	}
	defer tx.Rollback()

	old, err := offlineDownloadReceipt(ctx, tx, u.ID, receipt)
	if err != nil {
		return core.DownloadLimitDecision{}, err
	}
	if old.ETag != "" {
		if old != binding {
			return core.DownloadLimitDecision{}, core.ErrSourceChanged
		}
		return core.DownloadLimitDecision{Allowed: true}, nil
	}
	if !u.IsOwner {
		now := s.nowTime()
		decision, err := s.admitDownload(ctx, tx, u, key, bytes, true, now)
		if err != nil || !decision.Allowed {
			return decision, err
		}
		day, _, _, _ := downloadPeriod("", now)
		if _, err := tx.ExecContext(ctx, `INSERT INTO safety_download_usage(user_id,day,item_key,bytes,offline_bytes) VALUES(?,?,?,?,?)
			ON CONFLICT(user_id,day,item_key) DO UPDATE SET offline_bytes=CASE
				WHEN safety_download_usage.offline_bytes>excluded.offline_bytes THEN safety_download_usage.offline_bytes ELSE excluded.offline_bytes END,
				bytes=CASE WHEN safety_download_usage.bytes>excluded.bytes THEN safety_download_usage.bytes ELSE excluded.bytes END`,
			u.ID, day, int64(key), bytes, bytes); err != nil {
			return core.DownloadLimitDecision{}, err
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO safety_offline_download_grants(user_id,receipt,release_id,version_id,etag) VALUES(?,?,?,?,?)`,
		u.ID, receipt, binding.Release, binding.VersionID, binding.ETag); err != nil {
		return core.DownloadLimitDecision{}, err
	}
	if err := tx.Commit(); err != nil {
		return core.DownloadLimitDecision{}, err
	}
	return core.DownloadLimitDecision{Allowed: true}, nil
}

func (s *Service) RemainingDailyDownloads(ctx context.Context, u *core.User) (int, int, bool) {
	allowance := s.DownloadAllowance(u)
	if u == nil || s.db == nil || allowance.LimitPeriod != "day" || allowance.LimitUnit != "titles" {
		return 0, 0, false
	}
	day, _, _, _ := downloadPeriod("day", s.nowTime())
	var used int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM safety_download_usage WHERE user_id=? AND day=?`, u.ID, day).Scan(&used)
	return allowance.LimitAmount, max(0, allowance.LimitAmount-used), err == nil
}
