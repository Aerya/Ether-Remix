package releasedb

import (
	"context"
	"slices"
	"strconv"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/releaseparse"
)

// Alkodiques are individual verified files, separate from torrent_items.
// Read only the AD file link; neither import nor search calls the AD API.
const sfrAlkoSQL = `SELECT f.playback_id::text,f.filename,COALESCE(f.filesize,0),f.tmdb,
 f.kind,COALESCE(f.season,0),COALESCE(f.episode,0),l.source_url
 FROM alko_integration.files f JOIN alko_integration.source_links l USING(token)
 WHERE f.playback_id > COALESCE(NULLIF($1,'')::uuid,'00000000-0000-0000-0000-000000000000'::uuid) AND f.tmdb > 0 AND f.filesize > 0
 AND l.source_kind='alldebrid_f'
 AND (($2::bigint=0 AND (f.kind='movie' OR (f.kind='episode' AND f.season=1 AND f.episode=1)))
 OR (f.tmdb=$2 AND f.kind=$3 AND ($4::int=0 OR f.season=$4) AND ($5::int=0 OR f.episode=$5)))
 ORDER BY f.playback_id LIMIT $6`

func (p *pgxSFR) alkoRead(ctx context.Context, after string, req core.SearchRequest, limit int) ([]sfrRow, error) {
	if !p.alkoAvailable {
		return nil, nil
	}
	kind := "movie"
	if req.Type != core.TypeMovie {
		kind = "episode"
	}
	rows, err := p.conn.Query(ctx, sfrAlkoSQL, after, req.IDs.TMDB, kind, req.Season, req.Episode, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []sfrRow
	for rows.Next() {
		var r sfrRow
		var season, episode int
		if err := rows.Scan(&r.ID, &r.Name, &r.Size, &r.TMDB, &r.Media, &season, &episode, &r.AlkoURL); err != nil {
			return nil, err
		}
		if r.Media == "episode" {
			r.Media = "tv"
			r.Seasons = "[" + strconv.Itoa(season) + "]"
			r.Episodes = "[" + strconv.Itoa(episode) + "]"
		}
		r.Alko = true
		r.Cached = []string{"alldebrid"}
		out = append(out, r)
	}
	return out, rows.Err()
}

func sfrAlkoCandidates(rows []sfrRow) []core.Release {
	out := make([]core.Release, 0, len(rows))
	for _, r := range rows {
		if !core.ValidAllDebridFileURL(r.AlkoURL) || r.Size <= 0 || r.ID == "" {
			continue
		}
		info := releaseparse.Parse(r.Name)
		if !sfrAlkoLanguage(r.Name) {
			continue
		}
		out = append(out, core.Release{ID: "sfr-alko:" + r.ID, GUID: "sfr-alko:" + r.ID, Name: r.Name, Size: r.Size,
			Protocol: core.ProtocolHTTP, Indexer: "StreamFusion Reborn (Alkodiques)", TitleIDs: core.IDs{TMDB: r.TMDB},
			Info: info, StreamURL: r.AlkoURL, StreamDebrid: "alldebrid", StreamOnDemand: true, DebridCached: []string{"alldebrid"}})
	}
	return out
}

func (s *Service) sfrImportAlko(ctx context.Context, p *pgxSFR, every time.Duration) error {
	after := ""
	for {
		if err := s.sfrIdle(ctx); err != nil {
			return err
		}
		rows, err := p.alkoRead(ctx, after, core.SearchRequest{}, sfrPage)
		if err != nil {
			return err
		}
		// The next cycle refreshes the snapshot. Missing files lose their cached
		// proof even if the optional Alko tables later disappear or become unreadable.
		for i := range rows {
			rows[i].CacheUntil = s.d.Now().Add(2 * every).Unix()
		}
		if _, err = s.sfrSave(ctx, rows); err != nil {
			return err
		}
		if len(rows) < sfrPage {
			return nil
		}
		after = rows[len(rows)-1].ID
		if !sleepCtx(ctx, sfrPause) {
			return ctx.Err()
		}
	}
}

func sfrAlkoLanguage(name string) bool {
	info := releaseparse.Parse(name)
	langs := releaseLanguages(name, info.Languages)
	return slices.Contains(info.SubtitleLanguages, "french") || slices.Contains(langs, "french") || slices.Contains(langs, "multi") || slices.Contains(langs, "vostfr")
}
