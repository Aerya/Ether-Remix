package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/pse/ftp"
	"github.com/braedonsaunders/ether/internal/pse/releasedb"
	"github.com/braedonsaunders/ether/internal/pse/xdcc"
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
	return releasedb.Library{Store: releasedb.NewStore(a.db), Signature: hex.EncodeToString(sum[:]),
		Seal: func(s string) (string, error) { return a.cfg.Seal("pse-release-library", []byte(s)) },
		Open: func(s string) (string, error) { b, e := a.cfg.Unseal("pse-release-library", s); return string(b), e }}
}
func (a *App) rememberReleases(ctx context.Context, t *core.Title, rs []core.Release) {
	if !releasedb.LibraryEnabled() || t == nil || a.db == nil {
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
	if !releasedb.LibraryEnabled() || req.IDs.TMDB <= 0 {
		return
	}
	key := core.MovieKey(req.IDs.TMDB)
	if req.Type == core.TypeEpisode {
		key = core.EpisodeKey(req.IDs.TMDB, req.Season, req.Episode)
	} else if req.Type != core.TypeMovie {
		return
	}
	a.rememberReleases(ctx, &core.Title{Key: key}, rs)
}

func (a *App) retainedReleaseLibrary(ctx context.Context, req core.SearchRequest) ([]core.Release, bool) {
	if !releasedb.LibraryEnabled() || core.IndexerPurpose(ctx) != core.IndexerPurposePlayback || core.IsUnattendedSearch(ctx) || req.IDs.TMDB <= 0 {
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
		if core.IndexerSkipped(ctx, r.IndexerID) || (core.TorrentOnlySearch(ctx) && r.Protocol != core.ProtocolTorrent) {
			continue
		}
		out = append(out, r)
	}
	return out, len(out) > 0
}
