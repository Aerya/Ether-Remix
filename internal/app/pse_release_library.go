package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/pse/ftp"
	"github.com/braedonsaunders/ether/internal/pse/releasedb"
	"github.com/braedonsaunders/ether/internal/pse/xdcc"
	"github.com/braedonsaunders/ether/internal/releaseparse"
)

func (a *App) releaseLibrary() releasedb.Library {
	cfg := a.cfg.Read()
	raw, _ := json.Marshal(struct {
		Indexers any
		SFR      any
		FTP      any
		XDCC     any
	}{ftpIndexers(xdccIndexers(cfg.Indexers)), releasedb.LoadSFRSettings(), ftp.LoadSettings(), xdcc.LoadSettings()})
	sum := sha256.Sum256(raw)
	return releasedb.Library{Accept: a.acceptReference, Store: releasedb.NewStore(a.db), Signature: hex.EncodeToString(sum[:]),
		Seal: func(s string) (string, error) { return a.cfg.Seal("pse-release-library", []byte(s)) },
		Open: func(s string) (string, error) { b, e := a.cfg.Unseal("pse-release-library", s); return string(b), e }}
}
func (a *App) rememberReleases(ctx context.Context, t *core.Title, rs []core.Release) {
	if pseRuntimeOf(a).associationPending.Load() || !releasedb.LibraryEnabled() || t == nil || a.db == nil {
		return
	}
	if err := a.releaseLibrary().Save(ctx, t.Key, rs); err != nil {
		a.log.Warn("release library write failed")
	}
}

// Owner-only console projection: no source URLs, headers, GUIDs or credentials.
type libraryRow struct {
	Name       string   `json:"name"`
	Source     string   `json:"source"`
	Protocol   string   `json:"protocol"`
	Size       int64    `json:"size"`
	Height     int      `json:"height"`
	VideoCodec string   `json:"videoCodec"`
	AudioCodec string   `json:"audioCodec"`
	Languages  []string `json:"languages"`
	Cached     []string `json:"cached"`
	Published  int64    `json:"published"`
	FirstSeen  int64    `json:"firstSeen"`
	LastSeen   int64    `json:"lastSeen"`
	State      string   `json:"state"`
}

func (a *App) pseReleaseLibraryAPI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodGet {
		w.WriteHeader(405)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	enabled := releasedb.LibraryEnabled()
	if pseRuntimeOf(a).associationPending.Load() {
		_ = json.NewEncoder(w).Encode(map[string]any{"enabled": enabled, "auditPending": true, "auditFailed": pseRuntimeOf(a).associationFailed.Load(), "preparing": true, "items": []browseItem{}, "total": 0, "page": 0, "references": 0, "ready": 0, "expired": 0, "failed": 0, "sourceChanged": 0, "lastSeen": 0, "metadata": 0, "titles": 0})
		return
	}
	if r.URL.Query().Get("browse") == "1" {
		a.pseReleaseBrowse(w, r)
		return
	}
	if r.URL.Query().Get("overview") == "1" {
		a.pseReleaseLibraryOverview(w, r)
		return
	}
	if r.URL.Query().Get("key") == "" {
		_ = json.NewEncoder(w).Encode(map[string]any{"enabled": enabled})
		return
	}
	key, err := core.ParseRatingKey(r.URL.Query().Get("key"))
	if err != nil {
		http.Error(w, "Invalid title", 400)
		return
	}
	rows := []libraryRow{}
	// Show metadata already indexed before beta activation too.
	lookup := key
	if key.Kind() == core.KindEpisode {
		lookup = core.ShowKey(key.TMDB())
	}
	metadata, err := releasedb.NewStore(a.db).Releases(r.Context(), lookup, releasedb.Filter{})
	if err != nil {
		http.Error(w, "Release library unavailable", 500)
		return
	}
	seen := map[string]bool{}
	entryKey := key
	if key.Kind() == core.KindShow {
		entryKey = core.EpisodeKey(key.TMDB(), 1, 1)
	}
	entries, err := a.releaseLibrary().Entries(r.Context(), entryKey)
	if err != nil {
		http.Error(w, "Release library unavailable", 500)
		return
	}
	for _, e := range entries {
		rel := e.Release
		state := "ready"
		if !e.Current {
			state = "source_changed"
		} else if e.Failed {
			state = "failed"
		} else if e.Expires <= time.Now().Unix() {
			state = "expired"
		}
		published := int64(0)
		if !rel.PubDate.IsZero() {
			published = rel.PubDate.Unix()
		}
		rows = append(rows, libraryRow{rel.Name, rel.Indexer, rel.Protocol, rel.Size, rel.Info.Height, rel.Info.VideoCodec, rel.Info.AudioCodec, rel.Info.Languages, rel.DebridCached, published, e.FirstSeen, e.LastSeen, state})
		seen[rel.Name+"\x00"+rel.Indexer] = true
	}
	for _, e := range metadata {
		if key.Kind() == core.KindEpisode && (e.Season != key.Season() || e.Episode != key.Episode()) {
			continue
		}
		if seen[e.Name+"\x00"+e.SourceName] {
			continue
		}
		rows = append(rows, libraryRow{Name: e.Name, Source: e.SourceName, Protocol: e.Protocol, Size: e.Size, Height: e.Height, VideoCodec: e.VideoCodec, AudioCodec: e.AudioCodec, Languages: e.Languages, Cached: e.DebridCached, FirstSeen: e.FirstSeen.Unix(), LastSeen: e.LastSeen.Unix(), State: "metadata"})
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"enabled": enabled, "releases": rows})
}

func (a *App) rememberSearchReferences(ctx context.Context, req core.SearchRequest, rs []core.Release) {
	req = releasedb.CheckSeriesIdentity(ctx, a.db, req)
	if !releasedb.LibraryEnabled() || req.IDs.TMDB <= 0 {
		return
	}
	key := core.MovieKey(req.IDs.TMDB)
	if req.Type == core.TypeEpisode {
		key = core.EpisodeKey(req.IDs.TMDB, req.Season, req.Episode)
	} else if req.Type != core.TypeMovie {
		return
	}
	verified := make([]core.Release, 0, len(rs))
	for _, r := range rs {
		if releaseparse.AssociationReason(req, r) == "" && releaseparse.ReferenceReason(r) == "" {
			verified = append(verified, r)
		}
	}
	a.rememberReleases(ctx, &core.Title{Key: key}, verified)
}

func (a *App) retainedReleaseLibrary(ctx context.Context, req core.SearchRequest) ([]core.Release, bool) {
	req = releasedb.CheckSeriesIdentity(ctx, a.db, req)
	if pseRuntimeOf(a).associationPending.Load() || !releasedb.LibraryEnabled() || core.IndexerPurpose(ctx) != core.IndexerPurposePlayback || core.IsUnattendedSearch(ctx) || req.IDs.TMDB <= 0 {
		return nil, false
	}
	key := core.MovieKey(req.IDs.TMDB)
	if req.Type == core.TypeEpisode {
		key = core.EpisodeKey(req.IDs.TMDB, req.Season, req.Episode)
	} else if req.Type != core.TypeMovie {
		return nil, false
	}
	var out []core.Release
	for _, r := range a.releaseLibrary().Candidates(ctx, key) {
		if releaseparse.AssociationReason(req, r) != "" || releaseparse.ReferenceReason(r) != "" {
			continue
		}
		if core.IndexerSkipped(ctx, r.IndexerID) || (core.TorrentOnlySearch(ctx) && r.Protocol != core.ProtocolTorrent) {
			continue
		}
		out = append(out, r)
	}
	return out, len(out) > 0
}

// Overview contains counts and catalogue labels only; never stored descriptors.
func (a *App) pseReleaseLibraryOverview(w http.ResponseWriter, r *http.Request) {
	if !releasedb.LibraryEnabled() {
		_ = json.NewEncoder(w).Encode(map[string]any{"enabled": false})
		return
	}
	ctx := r.Context()
	fail := func() { http.Error(w, "Release library unavailable", 500) }
	var references, ready, expired, failed, changed, lastSeen, metadata, titles int64
	signature := a.releaseLibrary().Signature
	now := time.Now().Unix()
	err := a.db.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(CASE WHEN source_signature=? AND failed=0 AND expires>? THEN 1 ELSE 0 END),0), COALESCE(SUM(CASE WHEN source_signature=? AND failed=0 AND expires<=? THEN 1 ELSE 0 END),0), COALESCE(SUM(CASE WHEN source_signature=? AND failed<>0 THEN 1 ELSE 0 END),0), COALESCE(SUM(CASE WHEN source_signature<>? THEN 1 ELSE 0 END),0), COALESCE(MAX(last_seen),0) FROM pse_release_library`, signature, now, signature, now, signature, signature).Scan(&references, &ready, &expired, &failed, &changed, &lastSeen)
	if err != nil {
		fail()
		return
	}
	if err = a.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pse_releases`).Scan(&metadata); err != nil {
		fail()
		return
	}
	const keys = `SELECT title_key FROM pse_release_library UNION SELECT title_key FROM pse_releases`
	if err = a.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM (`+keys+`) AS library_keys`).Scan(&titles); err != nil {
		fail()
		return
	}
	var quarantined, backfill int64
	_ = a.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pse_reference_backfill`).Scan(&backfill)
	_ = a.db.QueryRowContext(ctx, `SELECT quarantined FROM pse_association_audit WHERE version=?`, releasedb.AssociationAuditVersion).Scan(&quarantined)
	if r.URL.Query().Get("summary") == "1" {
		_ = json.NewEncoder(w).Encode(map[string]any{"enabled": true, "references": references, "ready": ready, "expired": expired, "failed": failed, "sourceChanged": changed, "lastSeen": lastSeen, "metadata": metadata, "titles": titles, "quarantined": quarantined, "referenceBackfill": backfill})
		return
	}
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 0 {
		page = 0
	}
	if page > 1000000 {
		page = 1000000
	}
	rows, err := a.db.QueryContext(ctx, `SELECT title_key, MAX(last_seen) FROM (SELECT title_key,last_seen FROM pse_release_library UNION ALL SELECT title_key,last_seen FROM pse_releases) AS observations GROUP BY title_key ORDER BY MAX(last_seen) DESC,title_key LIMIT 50 OFFSET ?`, page*50)
	if err != nil {
		fail()
		return
	}
	type item struct {
		Key      string `json:"key"`
		Title    string `json:"title"`
		LastSeen int64  `json:"lastSeen"`
	}
	items := []item{}
	for rows.Next() {
		var k int64
		var seen int64
		if err = rows.Scan(&k, &seen); err != nil {
			break
		}
		items = append(items, item{Key: core.RatingKey(k).String(), LastSeen: seen})
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		fail()
		return
	}
	for i := range items {
		k, _ := core.ParseRatingKey(items[i].Key)
		lookup := k
		if k.Kind() == core.KindEpisode {
			lookup = core.ShowKey(k.TMDB())
		}
		var name string
		if a.catalog != nil {
			if title, err := a.catalog.StoredTitle(ctx, lookup); err == nil && title != nil {
				name = title.Title
			}
		}
		if name == "" {
			name = "TMDB " + strconv.FormatInt(k.TMDB(), 10)
		}
		if k.Kind() == core.KindEpisode {
			name += fmt.Sprintf(" · S%02dE%02d", k.Season(), k.Episode())
		}
		items[i].Title = name
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"enabled": true, "references": references, "ready": ready, "expired": expired, "failed": failed, "sourceChanged": changed, "lastSeen": lastSeen, "metadata": metadata, "titles": titles, "page": page, "items": items})
}
