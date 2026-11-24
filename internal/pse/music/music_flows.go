package music

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/store"
)

const SourceFlow = "pse-flow"

func init() {
	store.Register("pse_music_flows", 1, `CREATE TABLE pse_music_flows (
 viewer_id INTEGER NOT NULL, ref TEXT NOT NULL, config TEXT NOT NULL,
 revision INTEGER NOT NULL DEFAULT 1, attempted_at INTEGER NOT NULL DEFAULT 0,
 succeeded_at INTEGER NOT NULL DEFAULT 0, matched INTEGER NOT NULL DEFAULT 0,
 missing INTEGER NOT NULL DEFAULT 0, error TEXT NOT NULL DEFAULT '',
 PRIMARY KEY(viewer_id,ref));`)
	store.RegisterViewerTable("pse_music_flows", "pse_music_flows", "viewer_id", store.ViewerMergeMove, "ref")
}

// MusicFlow is a viewer-owned, periodically refreshed playlist definition.
// Credentials belong to the administrator's environment, never to this record.
type MusicFlow struct {
	Ref       string `json:"ref"`
	Viewer    int64  `json:"viewer"`
	Name      string `json:"name"`
	Mode      string `json:"mode"`
	Hours     int    `json:"hours"`
	Size      int    `json:"size"`
	Enabled   bool   `json:"enabled"`
	URL       string `json:"url,omitempty"`
	Seed      int64  `json:"seed,omitempty"`
	Revision  int64  `json:"revision"`
	Attempted int64  `json:"attempted"`
	Succeeded int64  `json:"succeeded"`
	Matched   int    `json:"matched"`
	Missing   int    `json:"missing"`
	Error     string `json:"error,omitempty"`
}

func (a *PlaylistAPI) flows(ctx context.Context, viewer int64) ([]MusicFlow, error) {
	rows, err := a.Sync.Store.DB.QueryContext(ctx, `SELECT viewer_id,ref,config,revision,attempted_at,succeeded_at,matched,missing,error FROM pse_music_flows WHERE (?=0 OR viewer_id=?) ORDER BY viewer_id,ref`, viewer, viewer)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []MusicFlow{}
	for rows.Next() {
		var f MusicFlow
		var raw string
		var v int64
		var ref string
		if err = rows.Scan(&v, &ref, &raw, &f.Revision, &f.Attempted, &f.Succeeded, &f.Matched, &f.Missing, &f.Error); err != nil {
			return nil, err
		}
		var cfg MusicFlow
		if err = json.Unmarshal([]byte(raw), &cfg); err != nil {
			return nil, err
		}
		cfg.Viewer = v
		cfg.Ref = ref
		cfg.Revision = f.Revision
		cfg.Attempted = f.Attempted
		cfg.Succeeded = f.Succeeded
		cfg.Matched = f.Matched
		cfg.Missing = f.Missing
		cfg.Error = f.Error
		out = append(out, cfg)
	}
	return out, rows.Err()
}

func (a *PlaylistAPI) saveFlow(ctx context.Context, f MusicFlow) error {
	f.Name = strings.TrimSpace(f.Name)
	if f.Viewer <= 0 || len(f.Ref) < 1 || len(f.Ref) > 64 || strings.ContainsAny(f.Ref, " /\\") || f.Name == "" || len(f.Name) > 120 || f.Hours < 1 || f.Hours > 168 || f.Size < 1 || f.Size > 200 {
		return errors.New("paramètres de playlist invalides")
	}
	switch f.Mode {
	case "mix":
		f.URL = ""
		f.Seed = 0
	case "playlist":
		if len(f.URL) > 2048 {
			return errors.New("lien trop long")
		}
		if _, _, err := parsePlaylistURL(f.URL); err != nil {
			return err
		}
		f.Seed = 0
	case "audiomuse":
		if f.Seed <= 0 {
			return errors.New("choisissez un titre local accessible")
		}
		f.URL = ""
		if _, err := a.audioMuseSeed(ctx, f.Viewer, f.Seed); err != nil {
			return err
		}
	default:
		return errors.New("type de playlist inconnu")
	}
	raw, _ := json.Marshal(f)
	tx, err := a.Sync.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if f.Revision == 0 {
		var count int
		if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM pse_music_flows WHERE viewer_id=?`, f.Viewer).Scan(&count); err != nil {
			return err
		}
		if count >= 20 {
			return errors.New("limite de 20 playlists programmées atteinte")
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO pse_music_flows(viewer_id,ref,config) VALUES(?,?,?)`, f.Viewer, f.Ref, string(raw))
	} else {
		res, e := tx.ExecContext(ctx, `UPDATE pse_music_flows SET config=?,revision=revision+1,attempted_at=0,error='' WHERE viewer_id=? AND ref=? AND revision=?`, string(raw), f.Viewer, f.Ref, f.Revision)
		err = e
		if err == nil {
			n, _ := res.RowsAffected()
			if n != 1 {
				return errors.New("playlist modifiée : rechargez la page")
			}
		}
	}
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (a *PlaylistAPI) deleteFlow(ctx context.Context, viewer int64, ref string) error {
	tx, err := a.Sync.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `DELETE FROM pse_music_flows WHERE viewer_id=? AND ref=?`, viewer, ref); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM pse_music_playlists WHERE viewer_id=? AND source=? AND ref=?`, viewer, SourceFlow, ref); err != nil {
		return err
	}
	return tx.Commit()
}

// SyncFlows runs due definitions after restart as well as during normal operation.
// Failed attempts are spaced by the configured cadence, preserving the last snapshot.
func (a *PlaylistAPI) SyncFlows(ctx context.Context) {
	fs, err := a.flows(ctx, 0)
	if err != nil {
		return
	}
	for _, f := range fs {
		if ctx.Err() != nil {
			return
		}
		if f.Enabled && a.Known(ctx, f.Viewer) {
			_ = a.runFlow(ctx, f.Viewer, f.Ref, false)
		}
	}
}

func (a *PlaylistAPI) runFlow(ctx context.Context, viewer int64, ref string, force bool) error {
	a.Sync.run.Lock()
	defer a.Sync.run.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	fs, err := a.flows(ctx, viewer)
	if err != nil {
		return err
	}
	var f MusicFlow
	for _, v := range fs {
		if v.Ref == ref {
			f = v
			break
		}
	}
	if f.Ref == "" {
		return errors.New("playlist inconnue")
	}
	now := a.Sync.Store.now().Unix()
	if !force && (!f.Enabled || (f.Attempted != 0 && now-f.Attempted < int64(f.Hours)*3600)) {
		return nil
	}
	a.Sync.mark(viewer, 1)
	defer a.Sync.mark(viewer, -1)
	if _, err = a.Sync.Store.DB.ExecContext(ctx, `UPDATE pse_music_flows SET attempted_at=? WHERE viewer_id=? AND ref=? AND revision=?`, now, viewer, ref, f.Revision); err != nil {
		return err
	}
	runCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	tracks, missing, err := a.flowTracks(runCtx, f)
	if err == nil && len(tracks) == 0 {
		err = errors.New("aucun titre disponible ; dernier contenu conservé")
	}
	if err != nil {
		// Never persist upstream errors: they may contain URLs or bearer credentials.
		reportCtx, cancelReport := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancelReport()
		_, _ = a.Sync.Store.DB.ExecContext(reportCtx, `UPDATE pse_music_flows SET error=? WHERE viewer_id=? AND ref=? AND revision=?`, "Actualisation impossible ; dernier contenu conservé. Vérifiez la source et les titres accessibles.", viewer, ref, f.Revision)
		return errors.New("actualisation impossible ; dernier contenu conservé")
	}
	tx, err := a.Sync.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `UPDATE pse_music_flows SET succeeded_at=?,matched=?,missing=?,error='' WHERE viewer_id=? AND ref=? AND revision=?`, now, len(tracks), missing, viewer, ref, f.Revision)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return errors.New("playlist modifiée pendant l'actualisation")
	}
	raw, _ := json.Marshal(tracks)
	_, err = tx.ExecContext(ctx, `INSERT INTO pse_music_playlists(id,viewer_id,source,ref,name,tracks,synced_at) VALUES(?,?,?,?,?,?,?) ON CONFLICT(viewer_id,source,ref) DO UPDATE SET name=excluded.name,tracks=excluded.tracks,synced_at=excluded.synced_at`, PlaylistID(viewer, SourceFlow, ref), viewer, SourceFlow, ref, f.Name, string(raw), now)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (a *PlaylistAPI) flowTracks(ctx context.Context, f MusicFlow) ([]Track, int, error) {
	switch f.Mode {
	case "audiomuse":
		return a.audioMuseTracks(ctx, f)
	case "playlist":
		p, err := a.preview(ctx, f.Viewer, f.URL)
		if err != nil {
			return nil, 0, err
		}
		// A partial response must not silently remove the tail of a living playlist.
		if p.Truncated {
			return nil, 0, errors.New("source trop longue")
		}
		out := []Track{}
		seen := map[int64]bool{}
		missing := p.Skipped
		for _, entry := range p.Tracks {
			var t Track
			ok := false
			if entry.track != nil {
				t = *entry.track
				ok = true
			} else {
				t, ok, err = a.Sync.findTrackResult(ctx, entry.Artist, entry.Title)
				if err != nil {
					return nil, 0, err
				}
				ok = ok && flowName(t.Artist.Name) == flowName(entry.Artist) && flowName(t.Title) == flowName(entry.Title)
			}
			if !ok {
				missing++
				continue
			}
			if !seen[t.ID] && len(out) < f.Size {
				out = append(out, t)
				seen[t.ID] = true
			}
		}
		return out, missing, nil
	case "mix":
		lib := NewLibrary(a.Sync.Store.DB)
		// Rotate each pool independently, then interleave favourites, recent listens
		// and seldom-played favourites so one large pool cannot erase the others.
		opts := []ListOptions{{Starred: true, Order: "starred", Limit: 1000}, {Played: true, Order: "played", Limit: 1000}, {Starred: true, Order: "starred", Limit: 1000}}
		pools := make([][]Track, len(opts))
		period := a.Sync.Store.now().Unix() / (int64(f.Hours) * 3600)
		for i, o := range opts {
			items, err := lib.List(ctx, f.Viewer, KindTrack, o)
			if err != nil {
				return nil, 0, err
			}
			for _, it := range items {
				if i == 2 && it.PlayCount > 1 {
					continue
				}
				var t Track
				if json.Unmarshal(it.Data, &t) == nil && t.ID > 0 {
					pools[i] = append(pools[i], t)
				}
			}
			sort.Slice(pools[i], func(j, k int) bool { return flowRank(f, period, pools[i][j].ID) < flowRank(f, period, pools[i][k].ID) })
		}
		out := []Track{}
		seen := map[int64]bool{}
		for round := 0; round < 1000 && len(out) < f.Size; round++ {
			for _, pool := range pools {
				if round < len(pool) && !seen[pool[round].ID] && len(out) < f.Size {
					t := pool[round]
					seen[t.ID] = true
					out = append(out, t)
				}
			}
		}
		return out, 0, nil
	}
	return nil, 0, errors.New("type inconnu")
}
func flowRank(f MusicFlow, period, id int64) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("%d:%s:%d:%d", f.Viewer, f.Ref, period, id))))
}

// Keep version markers and punctuation significant; only case and whitespace vary.
func flowName(s string) string { return strings.ToLower(strings.Join(strings.Fields(s), " ")) }
