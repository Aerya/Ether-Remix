package releasedb

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/releaseparse"
)

// SFRIdentity binds cached search results to the current database and privacy
// choice, without exposing credentials in a generated indexer's URL.
func SFRIdentity(c SFRSettings) string { return sfrSignature(c) }

// SearchSFR reads candidates directly. No StreamFusion HTTP warm-up, debrid
// download or remote metadata lookup happens on this latency-sensitive path.
func SearchSFR(ctx context.Context, cfg SFRSettings, req core.SearchRequest) ([]core.Release, error) {
	if !cfg.Enabled || req.IDs.TMDB <= 0 || (req.Type != core.TypeMovie && req.Type != core.TypeShow && req.Type != core.TypeEpisode) {
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	reader, err := openPgxSFR(ctx, cfg.URL)
	if err != nil {
		return nil, sfrSearchError(err)
	}
	defer reader.close()
	p := reader.(*pgxSFR)
	p.cacheAvailable = false // playback verifies against its configured providers, not stored hints
	media := "movie"
	if req.Type != core.TypeMovie {
		media = "tv"
	}
	// Season and episode constraints precede LIMIT, so other seasons cannot
	// consume the result budget. Missing parsed numbering is checked in Go.
	query := sfrSelectSQL + ` AND tmdb_id=$2 AND tmdb_media_type=$5
 AND ($6::int=0 OR COALESCE((parsed_data->'seasons')::jsonb,'[]'::jsonb)='[]'::jsonb OR (parsed_data->'seasons')::jsonb @> to_jsonb(ARRAY[$6::int]))
 AND ($7::int=0 OR COALESCE((parsed_data->'episodes')::jsonb,'[]'::jsonb)='[]'::jsonb OR (parsed_data->'episodes')::jsonb @> to_jsonb(ARRAY[$7::int]))` + sfrFiltersSQL
	rows, err := p.read(ctx, 200, query, "", req.IDs.TMDB, cfg.Private, 200, media, req.Season, req.Episode)
	if err != nil {
		return nil, sfrSearchError(err)
	}
	return sfrCandidates(rows, req), nil
}

func sfrCandidates(rows []sfrRow, req core.SearchRequest) []core.Release {
	out := make([]core.Release, 0, len(rows))
	seen := map[string]bool{}
	for _, r := range rows {
		hash := strings.ToLower(r.Hash)
		if len(hash) != 40 {
			continue
		}
		if _, err := hex.DecodeString(hash); err != nil {
			continue
		}
		if seen[hash] {
			continue
		}
		info := releaseparse.Parse(r.Name)
		if req.Type != core.TypeMovie {
			seasons, episodes := sfrNumbers(r.Seasons), sfrNumbers(r.Episodes)
			if len(seasons) == 0 {
				seasons = info.Seasons
			}
			if len(episodes) == 0 {
				episodes = info.Episodes
			}
			if req.Season > 0 && !sfrContains(seasons, req.Season) {
				continue
			}
			if req.Episode > 0 && len(episodes) > 0 && !sfrContains(episodes, req.Episode) {
				continue
			}
		}
		seen[hash] = true
		// Cache facts from PostgreSQL never bypass the configured provider's
		// availability/health checks. Only stable descriptors enter search caches.
		out = append(out, core.Release{ID: hash, GUID: hash, InfoHash: hash, MagnetURL: "magnet:?xt=urn:btih:" + hash + "&dn=" + url.QueryEscape(r.Name), Name: r.Name, Size: r.Size, Protocol: core.ProtocolTorrent, Indexer: sfrName, TitleIDs: core.IDs{TMDB: r.TMDB}, Info: info, PubDate: time.Unix(r.Stamp, 0)})
	}
	return out
}
func sfrContains(ns []int, n int) bool {
	for _, v := range ns {
		if v == n {
			return true
		}
	}
	return false
}

func sfrNumbers(raw string) []int { var out []int; _ = json.Unmarshal([]byte(raw), &out); return out }

func sfrSearchError(err error) error {
	var pg *pgconn.PgError
	if errors.As(err, &pg) && (pg.Code == "28P01" || pg.Code == "28000" || pg.Code == "42501") {
		return fmt.Errorf("StreamFusion database access refused: %w", core.ErrUnauthorized)
	}
	return fmt.Errorf("StreamFusion database unavailable: %w", core.ErrSourceUnavailable)
}
