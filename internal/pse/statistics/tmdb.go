// Package statistics fournit les repères TMDB et l'historique local d'Ether-Remix.
package statistics

import (
	"bufio"
	"compress/gzip"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/braedonsaunders/ether/internal/store"
)

func init() {
	store.Register("pse_tmdb_totals", 1, `CREATE TABLE pse_tmdb_totals(id INTEGER PRIMARY KEY CHECK(id=1), snapshot TEXT NOT NULL);`)
}

type TMDBTotals struct {
	Movies     int    `json:"movies"`
	Shows      int    `json:"shows"`
	ExportDate string `json:"exportDate"`
	UpdatedAt  int64  `json:"updatedAt"`
	CheckedAt  int64  `json:"checkedAt"`
	Error      string `json:"error,omitempty"`
}

type Service struct {
	HTTP   *http.Client
	Now    func() time.Time
	db     *sql.DB
	mu     sync.Mutex
	totals TMDBTotals
	busy   bool
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}
func (s *Service) Load(ctx context.Context, db *sql.DB) error {
	s.db = db
	if db == nil {
		return nil
	}
	var raw string
	err := db.QueryRowContext(ctx, "SELECT snapshot FROM pse_tmdb_totals WHERE id=1").Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	return json.Unmarshal([]byte(raw), &s.totals)
}
func (s *Service) Snapshot() TMDBTotals { s.mu.Lock(); defer s.mu.Unlock(); return s.totals }

// Les exports du jour sont tous publiés à 08:00 UTC selon TMDB.
func exportDate(now time.Time) time.Time {
	now = now.UTC()
	if now.Hour() < 8 {
		now = now.AddDate(0, 0, -1)
	}
	return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
}

// Refresh ne se produit jamais dans la requête HTTP de la console.
// Une erreur ne remplace pas le dernier relevé complet et persistant.
func (s *Service) Refresh(ctx context.Context) {
	now := s.now()
	date := exportDate(now)
	s.mu.Lock()
	if s.busy || s.totals.ExportDate == date.Format("2006-01-02") || (s.totals.CheckedAt > 0 && now.Sub(time.Unix(s.totals.CheckedAt, 0)) < time.Hour) {
		s.mu.Unlock()
		return
	}
	s.busy = true
	s.totals.CheckedAt = now.Unix()
	s.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	movies, err := s.count(ctx, "movie", date)
	shows := 0
	if err == nil {
		shows, err = s.count(ctx, "tv_series", date)
	}
	// L'export peut être en retard : reprendre la veille, en conservant la
	// même date pour les deux types.
	if err != nil && errors.Is(err, errExportMissing) {
		date = date.AddDate(0, 0, -1)
		movies, err = s.count(ctx, "movie", date)
		if err == nil {
			shows, err = s.count(ctx, "tv_series", date)
		}
	}
	if err != nil {
		s.mu.Lock()
		s.busy = false
		s.totals.Error = "Totaux TMDB indisponibles ; dernier relevé conservé."
		s.mu.Unlock()
		return
	}
	val := TMDBTotals{Movies: movies, Shows: shows, ExportDate: date.Format("2006-01-02"), UpdatedAt: now.Unix(), CheckedAt: now.Unix()}
	if s.db != nil {
		raw, _ := json.Marshal(val)
		if _, err = s.db.ExecContext(ctx, "INSERT INTO pse_tmdb_totals(id,snapshot) VALUES(1,?) ON CONFLICT(id) DO UPDATE SET snapshot=excluded.snapshot", string(raw)); err != nil {
			val.Error = "Totaux actualisés ; sauvegarde du relevé impossible."
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.busy = false
	s.totals = val
}

var errExportMissing = errors.New("TMDB export missing")

func (s *Service) count(ctx context.Context, kind string, date time.Time) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("https://files.tmdb.org/p/exports/%s_ids_%s.json.gz", kind, date.Format("01_02_2006")), nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("User-Agent", "Ether-Remix/1.0 (+https://github.com/Aerya/Ether-Remix)")
	client := s.HTTP
	if client == nil {
		client = &http.Client{Timeout: 90 * time.Second}
	}
	response, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer response.Body.Close()
	if response.StatusCode == 404 {
		return 0, errExportMissing
	}
	if response.StatusCode != 200 {
		return 0, errors.New("TMDB export unavailable")
	}
	compressed := &io.LimitedReader{R: response.Body, N: 256 << 20}
	reader, err := gzip.NewReader(compressed)
	if err != nil {
		return 0, err
	}
	defer reader.Close()
	expanded := &io.LimitedReader{R: reader, N: 1 << 30}
	scan := bufio.NewScanner(expanded)
	scan.Buffer(make([]byte, 64<<10), 1<<20)
	count := 0
	for scan.Scan() {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		var item struct {
			ID int64 `json:"id"`
		}
		if json.Unmarshal(scan.Bytes(), &item) != nil || item.ID <= 0 {
			return 0, errors.New("invalid TMDB export")
		}
		count++
	}
	if err := scan.Err(); err != nil {
		return 0, err
	}
	if count == 0 || compressed.N == 0 || expanded.N == 0 {
		return 0, errors.New("incomplete TMDB export")
	}
	return count, nil
}

func (s *Service) Run(ctx context.Context) {
	timer := time.NewTimer(time.Minute)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		s.Refresh(ctx)
		timer.Reset(time.Hour)
	}
}
