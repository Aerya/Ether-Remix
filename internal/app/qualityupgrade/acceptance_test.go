package qualityupgrade_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/app"
	"github.com/braedonsaunders/ether/internal/auth"
	"github.com/braedonsaunders/ether/internal/catalog"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend/kit/ids"
	"github.com/braedonsaunders/ether/internal/identity"
	"github.com/braedonsaunders/ether/internal/libraries"
	"github.com/braedonsaunders/ether/internal/plextv"
	"github.com/braedonsaunders/ether/internal/resolver"
	"github.com/braedonsaunders/ether/internal/safety"
	"github.com/braedonsaunders/ether/internal/store"
	"github.com/braedonsaunders/ether/internal/transcode"
	"github.com/braedonsaunders/ether/internal/webclient"
)

const (
	ownerPlexToken = "fixture-owner-token"
	tightPlexToken = "fixture-tight-token"
	loosePlexToken = "fixture-loose-token"

	velaDeviceID = "vela-device-1"
	jfDeviceID   = "jf-device-1"
)

type fixtureIDs struct {
	ownerID      int64
	tightID      int64
	looseID      int64
	animeMovieID core.LibraryID
	animeShowID  core.LibraryID
	velaTokens   map[int64]string
	jfTokens     map[int64]string
	hasUHDOnly   bool
}

type fixtureSource struct {
	id   string
	body []byte
}

func (s *fixtureSource) ReadAt(p []byte, off int64) (int, error) {
	if off >= int64(len(s.body)) {
		return 0, io.EOF
	}
	n := copy(p, s.body[off:])
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}
func (s *fixtureSource) Size() int64       { return int64(len(s.body)) }
func (s *fixtureSource) Name() string      { return "movie.mkv" }
func (s *fixtureSource) ReleaseID() string { return s.id }
func (s *fixtureSource) Close() error      { return nil }

// fixtureMedia serves three deterministic releases (2160p HDR Remux,
// 1080p WEB-DL, 720p WEB-DL) for every movie and episode in the local fixture
// so normal Plex & Vela HTTP playback paths exercise real profile ranking.
type fixtureMedia struct {
	body       []byte
	mu         sync.Mutex
	lastOpened string
}

func seasonsFor(q core.SearchRequest) []int {
	if q.Season > 0 {
		return []int{q.Season}
	}
	return nil
}

func episodesFor(q core.SearchRequest) []int {
	if q.Episode > 0 {
		return []int{q.Episode}
	}
	return nil
}

func (m *fixtureMedia) Name() string { return "quality-upgrade-fixture" }

func (m *fixtureMedia) Search(_ context.Context, q core.SearchRequest) ([]core.Release, error) {
	var base string
	tag := "2020"
	if q.IDs.TMDB != 0 {
		base = fmt.Sprintf("t%d-%s", q.IDs.TMDB, q.Type)
		if q.Type == core.TypeEpisode {
			base = fmt.Sprintf("t%d-s%02de%02d", q.IDs.TMDB, q.Season, q.Episode)
			tag = fmt.Sprintf("S%02dE%02d", q.Season, q.Episode)
		}
	} else {
		slug := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(q.Title), " ", "-"))
		if slug == "" {
			return nil, nil
		}
		base = fmt.Sprintf("title-%s-%s", slug, q.Type)
		if q.Type == core.TypeEpisode {
			base = fmt.Sprintf("title-%s-s%02de%02d", slug, q.Season, q.Episode)
			tag = fmt.Sprintf("S%02dE%02d", q.Season, q.Episode)
		}
	}
	uhd := core.Release{
		ID:       base + "-2160p",
		GUID:     base + "-2160p",
		Indexer:  m.Name(),
		Name:     fmt.Sprintf("%s.%s.2160p.UHD.BluRay.REMUX.HDR.HEVC.AC3", q.Title, tag),
		Size:     18 << 30,
		TitleIDs: q.IDs,
		Info: core.ReleaseInfo{
			Title:      q.Title,
			Year:       2020,
			Seasons:    seasonsFor(q),
			Episodes:   episodesFor(q),
			Height:     2160,
			Source:     core.SourceRemux,
			VideoCodec: "hevc",
			AudioCodec: "ac3",
			HDR10:      true,
		},
	}
	if q.IDs.TMDB == 5003 || strings.EqualFold(q.Title, "UHD Only Film") {
		return []core.Release{uhd}, nil
	}
	return []core.Release{
		uhd,
		{
			ID:       base + "-1080p",
			GUID:     base + "-1080p",
			Indexer:  m.Name(),
			Name:     fmt.Sprintf("%s.%s.1080p.WEB-DL.H264.AC3", q.Title, tag),
			Size:     5 << 30,
			TitleIDs: q.IDs,
			Info: core.ReleaseInfo{
				Title:      q.Title,
				Year:       2020,
				Seasons:    seasonsFor(q),
				Episodes:   episodesFor(q),
				Height:     1080,
				Source:     core.SourceWEBDL,
				VideoCodec: "h264",
				AudioCodec: "ac3",
			},
		},
		{
			ID:       base + "-720p",
			GUID:     base + "-720p",
			Indexer:  m.Name(),
			Name:     fmt.Sprintf("%s.%s.720p.WEB-DL.H264.AAC", q.Title, tag),
			Size:     2 << 30,
			TitleIDs: q.IDs,
			Info: core.ReleaseInfo{
				Title:      q.Title,
				Year:       2020,
				Seasons:    seasonsFor(q),
				Episodes:   episodesFor(q),
				Height:     720,
				Source:     core.SourceWEBDL,
				VideoCodec: "h264",
				AudioCodec: "aac",
			},
		},
	}, nil
}

func (m *fixtureMedia) FetchNZB(_ context.Context, r core.Release) ([]byte, error) {
	return []byte("NZB:" + r.ID), nil
}
func (m *fixtureMedia) Check(context.Context, []byte) (bool, error) { return true, nil }
func (m *fixtureMedia) CheckRelease(context.Context, string, []byte) (bool, error) {
	return true, nil
}
func (m *fixtureMedia) Open(_ context.Context, releaseID string, _ []byte) (core.MediaSource, error) {
	m.mu.Lock()
	m.lastOpened = releaseID
	m.mu.Unlock()
	payload := append([]byte("RELEASE:"+releaseID+"\n"), m.body...)
	return &fixtureSource{id: releaseID, body: payload}, nil
}
func (m *fixtureMedia) lastRelease() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.lastOpened
}
func (m *fixtureMedia) Prefetch(context.Context, string, []byte, int64, int64) error { return nil }

type fixtureHLS struct {
	id    string
	lease core.EncoderInputLease
}

func (s *fixtureHLS) ID() string { return s.id }
func (s *fixtureHLS) ServePlaylist(w http.ResponseWriter, _ *http.Request) {
	_, _ = io.WriteString(w, "#EXTM3U\n")
}
func (s *fixtureHLS) ServeSegment(w http.ResponseWriter, _ *http.Request, _ int) {
	_, _ = io.WriteString(w, "segment")
}
func (s *fixtureHLS) SwapInput(context.Context, string, core.MediaInfo, time.Duration) error {
	return nil
}
func (s *fixtureHLS) LastAccess() time.Time { return time.Now() }
func (s *fixtureHLS) Close() error          { return nil }

type decideRecord struct {
	info core.MediaInfo
	caps core.ClientCaps
	opts core.DecideOptions
	dec  core.Decision
}

// fixtureTranscoder delegates to real transcode.Decide without invoking ffmpeg
// and records every Decide call (including primary & lifted original evaluations).
type fixtureTranscoder struct {
	media   *fixtureMedia
	mu      sync.Mutex
	live    map[string]core.HLSSession
	max     int
	records []decideRecord
}

func (t *fixtureTranscoder) resetRecords() {
	t.mu.Lock()
	t.records = nil
	t.mu.Unlock()
}

func (t *fixtureTranscoder) getRecords() []decideRecord {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]decideRecord(nil), t.records...)
}

func (t *fixtureTranscoder) releaseHint(ctx context.Context, input string) string {
	if strings.Contains(input, "2160p") || strings.Contains(input, "1080p") || strings.Contains(input, "720p") {
		return input
	}
	if strings.HasPrefix(input, "http://") || strings.HasPrefix(input, "https://") {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, input, nil)
		if err == nil {
			req.Header.Set("Range", "bytes=0-127")
			if resp, err := (&http.Client{Timeout: 2 * time.Second}).Do(req); err == nil {
				buf, _ := io.ReadAll(io.LimitReader(resp.Body, 128))
				resp.Body.Close()
				if s := string(buf); strings.HasPrefix(s, "RELEASE:") {
					return s
				}
			}
		}
	}
	if t.media != nil {
		return t.media.lastRelease()
	}
	return input
}

func (t *fixtureTranscoder) Probe(ctx context.Context, input string) (*core.MediaInfo, error) {
	hint := t.releaseHint(ctx, input)
	height, kbps, codec, audio, ch, container := 1080, 9000, "h264", "ac3", 6, "mkv"
	switch {
	case strings.Contains(hint, "2160p"):
		height, kbps, codec, audio, ch, container = 2160, 22000, "hevc", "ac3", 6, "mkv"
	case strings.Contains(hint, "720p"):
		height, kbps, codec, audio, ch, container = 720, 3500, "h264", "aac", 2, "mp4"
	}
	return &core.MediaInfo{
		Container:   container,
		Duration:    90 * time.Minute,
		Size:        8 << 20,
		BitrateKbps: kbps,
		Streams: []core.StreamInfo{
			{Index: 0, Type: "video", Codec: codec, Width: height * 16 / 9, Height: height, BitrateKbps: kbps - 640, Default: true},
			{Index: 1, Type: "audio", Codec: audio, Channels: ch, BitrateKbps: 640, Default: true, Language: "eng"},
		},
	}, nil
}

func (t *fixtureTranscoder) Decide(info core.MediaInfo, caps core.ClientCaps, opts core.DecideOptions) core.Decision {
	dec := transcode.Decide(info, caps, opts, transcode.Policy{})
	t.mu.Lock()
	t.records = append(t.records, decideRecord{info: info, caps: caps, opts: opts, dec: dec})
	t.mu.Unlock()
	return dec
}

func (t *fixtureTranscoder) StartHLS(_ context.Context, req core.HLSRequest) (core.HLSSession, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.live == nil {
		t.live = map[string]core.HLSSession{}
	}
	if len(t.live) >= t.max {
		return nil, transcode.ErrTooManyTranscodes
	}
	id := req.SessionID
	if id == "" {
		id = "hls"
	}
	s := &fixtureHLS{id: id, lease: req.InputLease}
	t.live[id] = s
	return s, nil
}

func (t *fixtureTranscoder) Session(id string) (core.HLSSession, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	s, ok := t.live[id]
	return s, ok
}

func (t *fixtureTranscoder) Stop(id string) {
	t.mu.Lock()
	s := t.live[id]
	delete(t.live, id)
	t.mu.Unlock()
	if h, ok := s.(*fixtureHLS); ok && h.lease != nil {
		h.lease.SealInput()
	}
}

func (t *fixtureTranscoder) HWAccel() core.HWAccel { return core.HWAccel{Name: "none"} }

func stubOfflineWebClients(t *testing.T, dir string) {
	t.Helper()
	// Prevent background jellyfin-web and Vela web bundle downloads during offline tests.
	jfDir := filepath.Join(dir, "clients", "jellyfin-web", webclient.Version)
	if err := os.MkdirAll(jfDir, 0755); err != nil {
		t.Fatalf("mkdir jellyfin-web: %v", err)
	}
	if err := os.WriteFile(filepath.Join(jfDir, "index.html"), []byte("<html>stub</html>"), 0600); err != nil {
		t.Fatalf("write jellyfin-web index: %v", err)
	}
	marker := fmt.Sprintf("%s %s %d\n", webclient.Version, webclient.SHA256, webclient.Size)
	if err := os.WriteFile(filepath.Join(dir, "clients", "jellyfin-web", "version-"+webclient.Version+".sha256"), []byte(marker), 0600); err != nil {
		t.Fatalf("write jellyfin-web marker: %v", err)
	}
	velaDir := filepath.Join(dir, "clients", "vela")
	if err := os.MkdirAll(velaDir, 0755); err != nil {
		t.Fatalf("mkdir vela: %v", err)
	}
	if err := os.WriteFile(filepath.Join(velaDir, "index.html"), []byte("<html>stub</html>"), 0600); err != nil {
		t.Fatalf("write vela index: %v", err)
	}
}

func discardLog() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func freeLoopbackPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("allocate loopback port: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	return port
}

func libraryFilterRules(f core.Filter) []core.LibraryExclusion {
	return []core.LibraryExclusion{{Action: "show", MediaType: string(f.Type), Match: "all", Rules: core.LibraryPredicateConditions(f)}}
}

func seedV0320Fixture(t *testing.T, dir string) fixtureIDs {
	t.Helper()
	stubOfflineWebClients(t, dir)
	ctx := context.Background()

	db, err := store.Open(ctx, dir)
	if err != nil {
		t.Fatalf("open fixture db: %v", err)
	}
	users, err := auth.New(ctx, db, auth.Options{Logger: discardLog()})
	if err != nil {
		db.Close()
		t.Fatalf("auth.New: %v", err)
	}
	if err := users.SetOwner(ctx, plextv.Account{ID: 1001, Username: "owner"}, ownerPlexToken); err != nil {
		db.Close()
		t.Fatalf("SetOwner: %v", err)
	}
	if err := users.SyncFriends(ctx, []plextv.Friend{
		{PlexID: 1002, Username: "tight", Token: tightPlexToken, AllLibs: true},
		{PlexID: 1003, Username: "loose", Token: loosePlexToken, AllLibs: true},
	}); err != nil {
		db.Close()
		t.Fatalf("SyncFriends: %v", err)
	}
	owner, err := users.Owner(ctx)
	if err != nil || owner == nil {
		db.Close()
		t.Fatalf("owner: %v", err)
	}
	allUsers, err := users.Users(ctx)
	if err != nil {
		db.Close()
		t.Fatalf("Users: %v", err)
	}
	idsOut := fixtureIDs{
		ownerID:    owner.ID,
		velaTokens: map[int64]string{},
		jfTokens:   map[int64]string{},
	}
	for _, u := range allUsers {
		switch u.PlexID {
		case 1002:
			idsOut.tightID = u.ID
		case 1003:
			idsOut.looseID = u.ID
		}
	}
	if idsOut.tightID == 0 || idsOut.looseID == 0 {
		db.Close()
		t.Fatalf("missing friend users: %+v", allUsers)
	}

	// Mint Vela and Jellyfin grants so real Vela & Jellyfin HTTP requests work.
	grants, err := identity.NewGrants(db, users, 24*time.Hour, nil)
	if err != nil {
		db.Close()
		t.Fatalf("identity.NewGrants: %v", err)
	}
	for _, uid := range []int64{idsOut.ownerID, idsOut.tightID, idsOut.looseID} {
		tok, err := grants.Issue(ctx, uid, nil, "vela", core.Device{ID: velaDeviceID})
		if err != nil {
			db.Close()
			t.Fatalf("issue vela grant uid=%d: %v", uid, err)
		}
		if _, err := grants.IssueRefresh(ctx, "vela", tok, core.Device{ID: velaDeviceID}); err != nil {
			db.Close()
			t.Fatalf("issue vela refresh uid=%d: %v", uid, err)
		}
		idsOut.velaTokens[uid] = tok
		jtok, err := grants.Issue(ctx, uid, nil, "jellyfin", core.Device{ID: jfDeviceID})
		if err != nil {
			db.Close()
			t.Fatalf("issue jellyfin grant uid=%d: %v", uid, err)
		}
		idsOut.jfTokens[uid] = jtok
	}

	// Retired v0.3.20 per-viewer safety bitrate caps.
	safe, err := safety.New(ctx, db)
	if err != nil {
		db.Close()
		t.Fatalf("safety.New: %v", err)
	}
	if err := safe.Put(ctx, idsOut.tightID, safety.Policy{MaxBitrateKbps: 4000}); err != nil {
		db.Close()
		t.Fatalf("safety tight: %v", err)
	}
	if err := safe.Put(ctx, idsOut.looseID, safety.Policy{MaxBitrateKbps: 12000}); err != nil {
		db.Close()
		t.Fatalf("safety loose: %v", err)
	}

	cat, err := catalog.New(db, nil, config.Catalog{}, discardLog())
	if err != nil {
		db.Close()
		t.Fatalf("catalog.New: %v", err)
	}
	rel := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	idsOut.hasUHDOnly = true
	titles := []core.Title{
		{Key: core.MovieKey(5001), Type: core.TypeMovie, IDs: core.IDs{TMDB: 5001}, Title: "Regular Film", Year: 2020, Released: rel, Genres: []string{"Drama"}},
		{Key: core.MovieKey(5002), Type: core.TypeMovie, IDs: core.IDs{TMDB: 5002}, Title: "Anime Film", Year: 2020, Released: rel, Genres: []string{"Animation"}},
		{Key: core.MovieKey(5003), Type: core.TypeMovie, IDs: core.IDs{TMDB: 5003}, Title: "UHD Only Film", Year: 2020, Released: rel, Genres: []string{"Drama"}},
		{Key: core.ShowKey(6001), Type: core.TypeShow, IDs: core.IDs{TMDB: 6001}, Title: "Regular Series", Year: 2020, Released: rel, Genres: []string{"Drama"}},
		{Key: core.EpisodeKey(6001, 1, 1), Type: core.TypeEpisode, IDs: core.IDs{TMDB: 6001}, ShowKey: core.ShowKey(6001), Season: 1, Episode: 1, Title: "Regular Ep 1", Year: 2020, Released: rel},
		{Key: core.ShowKey(6002), Type: core.TypeShow, IDs: core.IDs{TMDB: 6002}, Title: "Anime Series", Year: 2020, Released: rel, Genres: []string{"Animation"}},
		{Key: core.EpisodeKey(6002, 1, 1), Type: core.TypeEpisode, IDs: core.IDs{TMDB: 6002}, ShowKey: core.ShowKey(6002), Season: 1, Episode: 1, Title: "Anime Ep 1", Year: 2020, Released: rel},
	}
	for _, ti := range titles {
		if err := cat.UpsertTitle(ctx, ti, true); err != nil {
			db.Close()
			t.Fatalf("UpsertTitle %s: %v", ti.Title, err)
		}
		if _, err := db.ExecContext(ctx, `UPDATE catalog_titles SET detail=1, detail_at=? WHERE key=?`, time.Now().Unix(), int64(ti.Key)); err != nil {
			db.Close()
			t.Fatalf("mark detail %s: %v", ti.Title, err)
		}
	}

	libs, err := libraries.New(ctx, db, libraries.Options{})
	if err != nil {
		db.Close()
		t.Fatalf("libraries.New: %v", err)
	}
	animeMovies, err := libs.Create(ctx, "Anime Films", core.TypeMovie, libraryFilterRules(core.Filter{Type: core.TypeMovie, Genres: []string{"Animation"}}), "hide", core.Presentations{})
	if err != nil {
		db.Close()
		t.Fatalf("create Anime Films: %v", err)
	}
	if err := libs.SetQualityProfile(ctx, animeMovies.ID, "Anime 1080p"); err != nil {
		db.Close()
		t.Fatalf("set Anime Films profile: %v", err)
	}
	animeShows, err := libs.Create(ctx, "Anime Shows", core.TypeShow, libraryFilterRules(core.Filter{Type: core.TypeShow, Genres: []string{"Animation"}}), "hide", core.Presentations{})
	if err != nil {
		db.Close()
		t.Fatalf("create Anime Shows: %v", err)
	}
	if err := libs.SetQualityProfile(ctx, animeShows.ID, "Anime 1080p"); err != nil {
		db.Close()
		t.Fatalf("set Anime Shows profile: %v", err)
	}
	idsOut.animeMovieID = animeMovies.ID
	idsOut.animeShowID = animeShows.ID

	if _, err := db.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		db.Close()
		t.Fatalf("wal checkpoint: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close fixture db: %v", err)
	}

	// Write a genuine v0.3.20 config.json using exact v0.3.20 field names and shapes.
	port := freeLoopbackPort(t)
	rawV0320 := map[string]any{
		"setupComplete": true,
		"server": map[string]any{
			"name":               "Ether v0.3.20 Fixture",
			"machineId":          "v0320-quality-upgrade-fixture-0001",
			"bindAddress":        "127.0.0.1",
			"port":               port,
			"portExplicit":       true,
			"defaultStreamLimit": 64,
			"maxStreams":         64,
		},
		"plex": map[string]any{
			"token":   ownerPlexToken,
			"claimed": false,
		},
		"availability": map[string]any{
			"monitor": false,
		},
		"frontends": map[string]any{
			"plex":     map[string]any{"enabled": true},
			"vela":     map[string]any{"enabled": true},
			"jellyfin": map[string]any{"enabled": true, "settings": map[string]any{"port": 0, "discovery": false}},
		},
		"transcode": map[string]any{
			"ffmpegPath":           "/not-installed/ffmpeg",
			"remoteMaxBitrateKbps": 8000,
		},
		"quality": map[string]any{
			"profiles": []map[string]any{
				// Identical pair (except episode-only limit) -> merges into "4K Home".
				{"name": "4K Home", "mediaType": "movie", "maxHeight": 2160, "allowHdr": true, "allowDolbyVision": true, "customFormatScores": map[string]int{"source-webdl": 100}},
				{"name": "4K Home", "mediaType": "tv", "maxHeight": 2160, "allowHdr": true, "allowDolbyVision": true, "episodeMaxSizeMbPerMin": 120, "customFormatScores": map[string]int{"source-webdl": 100}},
				// Divergent pair -> movie stays "Cinema" (2160p HDR), TV splits into "Cinema (TV)" (1080p SDR).
				{"name": "Cinema", "mediaType": "movie", "maxHeight": 2160, "allowHdr": true, "allowDolbyVision": true, "customFormatScores": map[string]int{"source-remux": 200}},
				{"name": "Cinema", "mediaType": "tv", "maxHeight": 1080, "allowHdr": false, "allowDolbyVision": false, "customFormatScores": map[string]int{"source-webdl": 150}},
				// Away movie & TV profiles.
				{"name": "Travel 1080p", "mediaType": "movie", "maxHeight": 1080, "allowHdr": false},
				{"name": "Travel 1080p", "mediaType": "tv", "maxHeight": 1080, "allowHdr": false},
				{"name": "Travel TV 720p", "mediaType": "tv", "maxHeight": 720, "allowHdr": false},
				// Custom library profile.
				{"name": "Anime 1080p", "mediaType": "movie", "maxHeight": 1080, "allowHdr": false},
				{"name": "Anime 1080p", "mediaType": "tv", "maxHeight": 1080, "allowHdr": false},
				// Device & viewer rule profiles.
				{"name": "Roku Compact", "mediaType": "movie", "maxHeight": 1080, "allowHdr": false},
				{"name": "Roku Compact", "mediaType": "tv", "maxHeight": 1080, "allowHdr": false},
				{"name": "Viewer Special", "mediaType": "movie", "maxHeight": 1080, "allowHdr": false},
				{"name": "Viewer Special", "mediaType": "tv", "maxHeight": 1080, "allowHdr": false},
			},
			"default":      "4K Home",
			"defaultMovie": "Cinema",
			"defaultTv":    "Cinema",
			"remote":       "Travel 1080p",
			"remoteMovie":  "Travel 1080p",
			"remoteTv":     "Travel TV 720p",
			"rules": []map[string]any{
				{
					"product":              "Roku",
					"mediaType":            "movie",
					"profile":              "Roku Compact",
					"maxHeight":            720,
					"convertSurroundAudio": true,
				},
				{
					"userId":    idsOut.tightID,
					"profile":   "Viewer Special",
					"maxHeight": 1080,
				},
			},
		},
	}
	rawBytes, err := json.MarshalIndent(rawV0320, "", "  ")
	if err != nil {
		t.Fatalf("marshal v0.3.20 config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), rawBytes, 0600); err != nil {
		t.Fatalf("write v0.3.20 config: %v", err)
	}
	return idsOut
}

// startEtherApp boots Ether through the normal app.New + app.Run path so
// frontends (Plex, Vela, Jellyfin) mount cleanly on a.Handler(), then returns
// a stop func that shuts Run down cleanly before closing App.
func startEtherApp(t *testing.T, dir string, media *fixtureMedia, tc *fixtureTranscoder, idsOut fixtureIDs) (*app.App, func()) {
	t.Helper()
	a, err := app.New(context.Background(), app.Options{
		DataDir:           dir,
		Logger:            discardLog(),
		FixtureMedia:      media,
		FixtureTranscoder: tc,
	})
	if err != nil {
		t.Fatalf("app.New(%s): %v", dir, err)
	}
	runCtx, cancel := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() {
		runDone <- a.Run(runCtx)
	}()

	// Wait until frontends (Vela & Jellyfin) are mounted on a.Handler().
	deadline := time.Now().Add(10 * time.Second)
	mounted := false
	for time.Now().Before(deadline) {
		req := httptest.NewRequest(http.MethodGet, "http://localhost/jellyfin/Users/Me", nil)
		req.RemoteAddr = "127.0.0.1:44000"
		req.Header.Set("Authorization", fmt.Sprintf(`MediaBrowser Client="Web", Device="Browser", DeviceId=%q, Version="1.0", Token=%q`, jfDeviceID, idsOut.jfTokens[idsOut.ownerID]))
		w := httptest.NewRecorder()
		a.Handler().ServeHTTP(w, req)
		if w.Code != http.StatusNotFound {
			mounted = true
			break
		}
		select {
		case err := <-runDone:
			a.Close()
			t.Fatalf("app.Run exited before frontends mounted: %v", err)
		case <-time.After(25 * time.Millisecond):
		}
	}
	if !mounted {
		cancel()
		<-runDone
		a.Close()
		t.Fatalf("frontends did not mount within timeout")
	}

	stop := func() {
		cancel()
		select {
		case <-runDone:
		case <-time.After(15 * time.Second):
			t.Errorf("app.Run did not exit within 15s on shutdown")
		}
		a.Close()
	}
	return a, stop
}

func readConfigFromDisk(t *testing.T, dir string) (config.Config, map[string]any) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatalf("read config.json: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("unmarshal raw config.json: %v", err)
	}
	var cfg config.Config
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("unmarshal config.Config: %v", err)
	}
	return cfg, doc
}

func assertMigratedPersistence(t *testing.T, dir string, idsOut fixtureIDs) config.Config {
	t.Helper()
	cfg, doc := readConfigFromDisk(t, dir)

	qDoc, _ := doc["quality"].(map[string]any)
	if qDoc == nil {
		t.Fatalf("missing quality section in config.json")
	}
	if v, _ := qDoc["migratedRules"].(float64); v < 1 {
		t.Fatalf("quality.migratedRules = %v, want >= 1", qDoc["migratedRules"])
	}
	for _, legacyKey := range []string{"rulesMigration", "remote", "defaultMovie", "defaultTv", "remoteMovie", "remoteTv"} {
		if _, exists := qDoc[legacyKey]; exists {
			t.Fatalf("legacy key quality.%s survived migration: %v", legacyKey, qDoc[legacyKey])
		}
	}
	profilesDoc, _ := qDoc["profiles"].([]any)
	for i, item := range profilesDoc {
		p, _ := item.(map[string]any)
		if _, has := p["mediaType"]; has {
			t.Fatalf("quality.profiles[%d] still has mediaType", i)
		}
	}
	if tcDoc, _ := doc["transcode"].(map[string]any); tcDoc != nil {
		if v, _ := tcDoc["remoteMaxBitrateKbps"].(float64); v != 0 {
			t.Fatalf("transcode.remoteMaxBitrateKbps = %v, want 0 after migration", v)
		}
	}

	// Verify merged/split profiles and server-assigned unique rule IDs.
	home4K, ok := core.FindProfile(cfg.Quality.Profiles, "4K Home")
	if !ok || home4K.EpisodeMaxSizeMBPerMin != 120 {
		t.Fatalf("merged 4K Home profile lost episode limit: ok=%v prof=%+v", ok, home4K)
	}
	cinemaMovie, okM := core.FindProfile(cfg.Quality.Profiles, "Cinema")
	cinemaTV, okT := core.FindProfile(cfg.Quality.Profiles, "Cinema (TV)")
	if !okM || !okT || cinemaMovie.MaxHeight != 2160 || !cinemaMovie.AllowHDR || cinemaTV.MaxHeight != 1080 || cinemaTV.AllowHDR {
		t.Fatalf("divergent Cinema split wrong: movie=(%v %+v) tv=(%v %+v)", okM, cinemaMovie, okT, cinemaTV)
	}
	seenIDs := map[string]bool{}
	for i, r := range cfg.Quality.Rules {
		if r.ID == "" {
			t.Fatalf("quality.rules[%d] missing server-assigned ID: %+v", i, r)
		}
		if seenIDs[r.ID] {
			t.Fatalf("duplicate quality rule ID %q", r.ID)
		}
		seenIDs[r.ID] = true
	}

	// Verify retired SQLite columns were cleared on disk.
	ctx := context.Background()
	db, err := store.Open(ctx, dir)
	if err != nil {
		t.Fatalf("reopen db for retired-field check: %v", err)
	}
	defer db.Close()
	libs, err := libraries.New(ctx, db, libraries.Options{})
	if err != nil {
		t.Fatalf("libraries.New: %v", err)
	}
	for _, libID := range []core.LibraryID{idsOut.animeMovieID, idsOut.animeShowID} {
		lib, err := libs.Get(ctx, libID)
		if err != nil {
			t.Fatalf("libraries.Get(%d): %v", libID, err)
		}
		if lib.QualityProfile != "" {
			t.Fatalf("library %d still has retired quality_profile=%q", libID, lib.QualityProfile)
		}
	}
	safe, err := safety.New(ctx, db)
	if err != nil {
		t.Fatalf("safety.New: %v", err)
	}
	for _, uid := range []int64{idsOut.tightID, idsOut.looseID} {
		if pol := safe.Get(uid); pol.MaxBitrateKbps != 0 {
			t.Fatalf("viewer %d still has retired safety maxBitrateKbps=%d", uid, pol.MaxBitrateKbps)
		}
	}
	return cfg
}

// TestV0320QualityRulesUpgradeEndToEnd verifies migrating a v0.3.20 config and
// database into the unified quality rules model through normal Ether startup
// and HTTP playback entry points (Quality API, Plex HTTP, Vela HTTP + client
// streaming cap & Original choice retention, Jellyfin viewer capabilities),
// plus a second startup with the exact same config/DataDir.
func TestV0320QualityRulesUpgradeEndToEnd(t *testing.T) {
	dir := t.TempDir()
	idsOut := seedV0320Fixture(t, dir)
	runTwoStartupUpgradeVerification(t, dir, idsOut)
}

// TestRealV0320CapturedFixtureUpgrade optionally runs the same two-startup
// upgrade check against a copy of a fixture directory generated by an actual
// v0.3.20 checkout when ETHER_V0320_FIXTURE_DIR is set.
func TestRealV0320CapturedFixtureUpgrade(t *testing.T) {
	src := os.Getenv("ETHER_V0320_FIXTURE_DIR")
	if src == "" {
		t.Skip("ETHER_V0320_FIXTURE_DIR not set")
	}
	dir := t.TempDir()
	if err := copyDir(src, dir); err != nil {
		t.Fatalf("copy fixture %s: %v", src, err)
	}
	idsOut := inspectAndAugmentCopiedFixture(t, dir)
	runTwoStartupUpgradeVerification(t, dir, idsOut)
}

func runTwoStartupUpgradeVerification(t *testing.T, dir string, idsOut fixtureIDs) {
	t.Helper()
	media := &fixtureMedia{body: []byte("fixture-media-payload")}

	// Startup 1: runs config normalization + one-time App.migrateQualityRules + normal Run.
	tc1 := &fixtureTranscoder{media: media, max: 64}
	a1, stop1 := startEtherApp(t, dir, media, tc1, idsOut)
	verifyAllEndpoints(t, "startup1", a1, tc1, idsOut)
	stop1()

	cfgAfterFirst := assertMigratedPersistence(t, dir, idsOut)
	rulesAfterFirst := append([]config.QualityRule(nil), cfgAfterFirst.Quality.Rules...)
	t.Logf("startup1 migrated %d rules", len(rulesAfterFirst))
	for i, r := range rulesAfterFirst {
		t.Logf("  rule[%d]: id=%s where=%s kind=%s libs=%v viewers=%v devices=%+v profile=%q maxH=%d maxKbps=%d stereo=%v",
			i, r.ID, r.Where, r.Kind, r.Libraries, r.Viewers, r.Devices, r.Profile, r.MaxHeight, r.MaxBitrateKbps, r.StereoAudio)
	}

	// Startup 2: reopen the exact same DataDir; migration must not repeat or duplicate rules.
	tc2 := &fixtureTranscoder{media: media, max: 64}
	a2, stop2 := startEtherApp(t, dir, media, tc2, idsOut)
	verifyAllEndpoints(t, "startup2", a2, tc2, idsOut)
	stop2()

	cfgAfterSecond := assertMigratedPersistence(t, dir, idsOut)
	rulesAfterSecond := cfgAfterSecond.Quality.Rules
	if !reflect.DeepEqual(rulesAfterFirst, rulesAfterSecond) {
		t.Fatalf("second startup altered rules:\nfirst:  %+v\nsecond: %+v", rulesAfterFirst, rulesAfterSecond)
	}
}

func verifyAllEndpoints(t *testing.T, label string, a *app.App, tc *fixtureTranscoder, idsOut fixtureIDs) {
	t.Helper()
	h := a.Handler()

	// 1. Quality Summary & Explain API.
	sumReq := httptest.NewRequest(http.MethodGet, "http://localhost/ether/api/quality/summary", nil)
	sumReq.RemoteAddr = "127.0.0.1:41000"
	sumReq.Header.Set("X-Plex-Token", ownerPlexToken)
	sumW := httptest.NewRecorder()
	h.ServeHTTP(sumW, sumReq)
	if sumW.Code != http.StatusOK || !strings.Contains(sumW.Body.String(), "Anime Films") || !strings.Contains(sumW.Body.String(), "Cinema (TV)") {
		t.Fatalf("%s: GET /ether/api/quality/summary status=%d body=%s", label, sumW.Code, sumW.Body.String())
	}

	checkExplain := func(name, body string) resolver.QualityAnswer {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "http://localhost/ether/api/quality/explain", strings.NewReader(body))
		req.RemoteAddr = "127.0.0.1:41000"
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Plex-Token", ownerPlexToken)
		req.Header.Set("X-Ether-Request", "1")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("%s/%s: POST /ether/api/quality/explain status %d: %s", label, name, w.Code, w.Body.String())
		}
		var ans resolver.QualityAnswer
		if err := json.Unmarshal(w.Body.Bytes(), &ans); err != nil {
			t.Fatalf("%s/%s: decode explain: %v", label, name, err)
		}
		return ans
	}

	// Default (media-neutral) vs Film vs TV at home & away.
	defHome := checkExplain("default-home", fmt.Sprintf(`{"userId":%d,"clientId":"plex-apple-tv","remote":false}`, idsOut.ownerID))
	if defHome.Profile != "Cinema" || defHome.MaxHeight != 2160 || defHome.BitrateKbps != 0 {
		t.Errorf("%s: default-home explain = %+v, want profile=Cinema maxH=2160 kbps=0", label, defHome)
	}
	filmHome := checkExplain("kind-movie-home", fmt.Sprintf(`{"userId":%d,"clientId":"plex-apple-tv","remote":false,"kind":"movie"}`, idsOut.ownerID))
	if filmHome.Profile != "Cinema" || filmHome.MaxHeight != 2160 || !filmHome.HDR {
		t.Errorf("%s: kind-movie-home explain = %+v, want Cinema 2160p HDR", label, filmHome)
	}
	tvHome := checkExplain("kind-episode-home", fmt.Sprintf(`{"userId":%d,"clientId":"plex-apple-tv","remote":false,"kind":"episode"}`, idsOut.ownerID))
	if tvHome.Profile != "Cinema (TV)" || tvHome.MaxHeight != 1080 || tvHome.HDR {
		t.Errorf("%s: kind-episode-home explain = %+v, want Cinema (TV) 1080p SDR", label, tvHome)
	}

	type explainCase struct {
		name        string
		userID      int64
		clientID    string
		remote      bool
		key         core.RatingKey
		wantProfile string
		wantHeight  int
		wantKbps    int
		wantHDR     bool
		wantStereo  bool
	}
	explainCases := []explainCase{
		// Home vs away, default film vs TV.
		{"owner-home-regular-movie-appletv", idsOut.ownerID, "plex-apple-tv", false, core.MovieKey(5001), "Cinema", 2160, 0, true, false},
		{"owner-away-regular-movie-appletv", idsOut.ownerID, "plex-apple-tv", true, core.MovieKey(5001), "Travel 1080p", 1080, 8000, false, false},
		{"owner-home-regular-tv-appletv", idsOut.ownerID, "plex-apple-tv", false, core.EpisodeKey(6001, 1, 1), "Cinema (TV)", 1080, 0, false, false},
		{"owner-away-regular-tv-appletv", idsOut.ownerID, "plex-apple-tv", true, core.EpisodeKey(6001, 1, 1), "Travel TV 720p", 720, 8000, false, false},

		// Custom library match (movie + episode inheriting show membership) & away > library.
		{"owner-home-anime-movie-appletv", idsOut.ownerID, "plex-apple-tv", false, core.MovieKey(5002), "Anime 1080p", 1080, 0, false, false},
		{"owner-away-anime-movie-appletv", idsOut.ownerID, "plex-apple-tv", true, core.MovieKey(5002), "Travel 1080p", 1080, 8000, false, false},
		{"owner-home-anime-tv-appletv", idsOut.ownerID, "plex-apple-tv", false, core.EpisodeKey(6002, 1, 1), "Anime 1080p", 1080, 0, false, false},
		{"owner-away-anime-tv-appletv", idsOut.ownerID, "plex-apple-tv", true, core.EpisodeKey(6002, 1, 1), "Travel TV 720p", 720, 8000, false, false},

		// Device match (Roku movie rule beats away & custom library; does NOT match TV).
		{"owner-home-regular-movie-roku", idsOut.ownerID, "plex-roku", false, core.MovieKey(5001), "Roku Compact", 720, 0, false, true},
		{"owner-away-regular-movie-roku", idsOut.ownerID, "plex-roku", true, core.MovieKey(5001), "Roku Compact", 720, 8000, false, true},
		{"owner-home-anime-movie-roku", idsOut.ownerID, "plex-roku", false, core.MovieKey(5002), "Roku Compact", 720, 0, false, true},
		{"owner-home-regular-tv-roku", idsOut.ownerID, "plex-roku", false, core.EpisodeKey(6001, 1, 1), "Cinema (TV)", 1080, 0, false, false},

		// Viewer match + strictest bitrate & height cap stacking.
		{"tight-home-regular-movie-appletv", idsOut.tightID, "plex-apple-tv", false, core.MovieKey(5001), "Viewer Special", 1080, 4000, false, false},
		{"tight-away-regular-movie-appletv", idsOut.tightID, "plex-apple-tv", true, core.MovieKey(5001), "Viewer Special", 1080, 4000, false, false},
		{"tight-home-regular-movie-roku", idsOut.tightID, "plex-roku", false, core.MovieKey(5001), "Roku Compact", 720, 4000, false, true},
		{"tight-away-regular-movie-roku", idsOut.tightID, "plex-roku", true, core.MovieKey(5001), "Roku Compact", 720, 4000, false, true},
		{"tight-home-regular-tv-roku", idsOut.tightID, "plex-roku", false, core.EpisodeKey(6001, 1, 1), "Viewer Special", 1080, 4000, false, false},

		// Loose viewer cap (12000 kbps > away 8000 kbps): strictest cap wins away.
		{"loose-home-regular-movie-appletv", idsOut.looseID, "plex-apple-tv", false, core.MovieKey(5001), "Cinema", 2160, 12000, true, false},
		{"loose-away-regular-movie-appletv", idsOut.looseID, "plex-apple-tv", true, core.MovieKey(5001), "Travel 1080p", 1080, 8000, false, false},
		{"loose-home-regular-movie-roku", idsOut.looseID, "plex-roku", false, core.MovieKey(5001), "Roku Compact", 720, 12000, false, true},
		{"loose-away-regular-movie-roku", idsOut.looseID, "plex-roku", true, core.MovieKey(5001), "Roku Compact", 720, 8000, false, true},
	}
	for _, ec := range explainCases {
		t.Run(label+"/explain/"+ec.name, func(t *testing.T) {
			body := fmt.Sprintf(`{"userId":%d,"clientId":%q,"remote":%v,"titleKey":%q}`, ec.userID, ec.clientID, ec.remote, ec.key.String())
			ex := checkExplain(ec.name, body)
			if ex.Profile != ec.wantProfile || ex.MaxHeight != ec.wantHeight || ex.BitrateKbps != ec.wantKbps || ex.HDR != ec.wantHDR || ex.Stereo != ec.wantStereo {
				t.Errorf("explain = (prof=%q maxH=%d kbps=%d hdr=%v stereo=%v), want (%q %d %d %v %v)",
					ex.Profile, ex.MaxHeight, ex.BitrateKbps, ex.HDR, ex.Stereo,
					ec.wantProfile, ec.wantHeight, ec.wantKbps, ec.wantHDR, ec.wantStereo)
			}
		})
	}

	// 2. Real Plex HTTP /video/:/transcode/universal/decision through App.Handler().
	type plexClient struct {
		product  string
		platform string
		device   string
	}
	appleTV := plexClient{product: "Plex for Apple TV", platform: "tvOS", device: "Apple TV"}
	roku := plexClient{product: "Plex for Roku", platform: "Roku", device: "Roku"}

	plexCases := []struct {
		name           string
		token          string
		client         plexClient
		remoteAddr     string
		key            core.RatingKey
		wantSourceH    int
		wantMaxBitrate int
		wantStereo     bool
	}{
		{"owner-home-regular-movie-appletv", ownerPlexToken, appleTV, "127.0.0.1:42001", core.MovieKey(5001), 2160, 0, false},
		{"owner-away-regular-movie-appletv", ownerPlexToken, appleTV, "198.51.100.10:42002", core.MovieKey(5001), 1080, 8000, false},
		{"owner-home-regular-tv-appletv", ownerPlexToken, appleTV, "127.0.0.1:42003", core.EpisodeKey(6001, 1, 1), 1080, 0, false},
		{"owner-away-regular-tv-appletv", ownerPlexToken, appleTV, "198.51.100.10:42004", core.EpisodeKey(6001, 1, 1), 720, 8000, false},
		{"owner-home-anime-movie-appletv", ownerPlexToken, appleTV, "127.0.0.1:42005", core.MovieKey(5002), 1080, 0, false},
		{"owner-home-anime-tv-appletv", ownerPlexToken, appleTV, "127.0.0.1:42006", core.EpisodeKey(6002, 1, 1), 1080, 0, false},
		{"owner-home-regular-movie-roku", ownerPlexToken, roku, "127.0.0.1:42007", core.MovieKey(5001), 720, 0, true},
		{"owner-away-regular-movie-roku", ownerPlexToken, roku, "198.51.100.10:42008", core.MovieKey(5001), 720, 8000, true},
		{"owner-home-regular-tv-roku", ownerPlexToken, roku, "127.0.0.1:42009", core.EpisodeKey(6001, 1, 1), 1080, 0, false},
		{"tight-home-regular-movie-appletv", tightPlexToken, appleTV, "127.0.0.1:42010", core.MovieKey(5001), 1080, 4000, false},
		{"tight-away-regular-movie-roku", tightPlexToken, roku, "198.51.100.10:42011", core.MovieKey(5001), 720, 4000, true},
		// Loose viewer's migrated 12 Mbps cap reaches resolver (which prefers clean 1080p over 2160p at <=12 Mbps) and transcoder.
		{"loose-home-regular-movie-appletv", loosePlexToken, appleTV, "127.0.0.1:42012", core.MovieKey(5001), 1080, 12000, false},
		{"loose-away-regular-movie-appletv", loosePlexToken, appleTV, "198.51.100.10:42013", core.MovieKey(5001), 1080, 8000, false},
	}
	if idsOut.hasUHDOnly {
		// When only a 2160p 22 Mbps release exists, loose at home opens 2160p under "Cinema" and enforces the migrated 12 Mbps cap.
		plexCases = append(plexCases, struct {
			name           string
			token          string
			client         plexClient
			remoteAddr     string
			key            core.RatingKey
			wantSourceH    int
			wantMaxBitrate int
			wantStereo     bool
		}{"loose-home-uhd-only-movie-appletv", loosePlexToken, appleTV, "127.0.0.1:42014", core.MovieKey(5003), 2160, 12000, false})
	}
	for _, pc := range plexCases {
		t.Run(label+"/plex/"+pc.name, func(t *testing.T) {
			tc.resetRecords()
			q := url.Values{
				"path":         {"/library/metadata/" + pc.key.String()},
				"session":      {"sess-" + label + "-" + pc.name},
				"protocol":     {"hls"},
				"directPlay":   {"1"},
				"directStream": {"1"},
			}
			req := httptest.NewRequest(http.MethodGet, "http://localhost/video/:/transcode/universal/decision?"+q.Encode(), nil)
			req.RemoteAddr = pc.remoteAddr
			req.Header.Set("X-Plex-Token", pc.token)
			req.Header.Set("X-Plex-Client-Identifier", "cid-"+pc.name)
			req.Header.Set("X-Plex-Product", pc.client.product)
			req.Header.Set("X-Plex-Platform", pc.client.platform)
			req.Header.Set("X-Plex-Device", pc.client.device)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)
			if w.Code != http.StatusOK {
				t.Fatalf("Plex decision HTTP %d: %s", w.Code, w.Body.String())
			}
			recs := tc.getRecords()
			if len(recs) == 0 {
				t.Fatalf("Plex decision did not invoke transcoder.Decide (body=%s)", w.Body.String())
			}
			last := recs[len(recs)-1]
			gotH := 0
			if v := last.info.Video(); v != nil {
				gotH = v.Height
			}
			effKbps := effectiveKbps(last.caps, last.opts)
			if gotH != pc.wantSourceH {
				t.Errorf("Plex resolved source height = %d, want %d (caps.MaxHeight=%d source=%q prod=%q plat=%q dev=%q remote=%v dec=%s/%s)",
					gotH, pc.wantSourceH, last.caps.MaxHeight, last.caps.MaxHeightSource,
					last.caps.Product, last.caps.Platform, last.caps.Device, last.caps.Remote,
					last.dec.Kind, last.dec.Reason)
			}
			if effKbps != pc.wantMaxBitrate {
				t.Errorf("Plex effective bitrate cap = %d (caps=%d opts=%d), want %d", effKbps, last.caps.MaxBitrateKbps, last.opts.MaxBitrateKbps, pc.wantMaxBitrate)
			}
			if last.caps.ConvertSurroundAudio != pc.wantStereo {
				t.Errorf("Plex ConvertSurroundAudio = %v, want %v", last.caps.ConvertSurroundAudio, pc.wantStereo)
			}
			if pc.wantMaxBitrate > 0 && last.dec.BitrateKbps > pc.wantMaxBitrate {
				t.Errorf("Plex decision BitrateKbps = %d exceeds cap %d (kind=%s)", last.dec.BitrateKbps, pc.wantMaxBitrate, last.dec.Kind)
			}
		})
	}

	// 3. Real Vela HTTP POST /vela/v0/playback: migrated rules + client streaming
	//    cap & retained household enforcement on the Original choice (276b0c893).
	type velaPathWire struct {
		ID                string `json:"id"`
		Transport         string `json:"transport"`
		Original          bool   `json:"original"`
		Readiness         string `json:"readiness"`
		URL               string `json:"url"`
		ExceedsClientCap  bool   `json:"exceedsClientCap"`
		ExceedsMaxBitrate bool   `json:"exceedsMaxBitrate"`
		Bitrate           int    `json:"bitrate"`
	}
	type velaPlayWire struct {
		SessionID      string         `json:"sessionId"`
		SelectedPathID string         `json:"selectedPathId"`
		Paths          []velaPathWire `json:"paths"`
	}
	callVelaPlay := func(t *testing.T, uid int64, remoteAddr string, key core.RatingKey, clientMaxBitrateBps int, devicePlatform string, idem string) (velaPlayWire, []decideRecord) {
		t.Helper()
		tc.resetRecords()
		clientObj := map[string]any{
			"device": map[string]any{
				"id":       velaDeviceID,
				"platform": devicePlatform,
				"name":     devicePlatform,
			},
			"hls":         true,
			"maxWidth":    3840,
			"maxHeight":   2160,
			"videoCodecs": []string{"h264", "hevc"},
			"audioCodecs": []string{"aac", "ac3"},
			"containers":  []string{"mp4", "mkv"},
			"originalPlayback": map[string]any{
				"version":         1,
				"range":           true,
				"demuxContainers": []string{"mkv", "mp4"},
				"videoDecoders":   []string{"hevc", "h264"},
			},
		}
		if clientMaxBitrateBps > 0 {
			clientObj["maxBitrate"] = clientMaxBitrateBps
		}
		reqPayload, _ := json.Marshal(map[string]any{
			"idempotencyKey":  idem,
			"titleId":         ids.RatingKeyGUID(key),
			"positionSeconds": 0.0,
			"client":          clientObj,
		})
		req := httptest.NewRequest(http.MethodPost, "http://localhost/vela/v0/playback", strings.NewReader(string(reqPayload)))
		req.RemoteAddr = remoteAddr
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+idsOut.velaTokens[uid])
		req.Header.Set("X-Vela-Device-Id", velaDeviceID)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusOK && w.Code != http.StatusCreated {
			t.Fatalf("POST /vela/v0/playback HTTP %d: %s", w.Code, w.Body.String())
		}
		var out velaPlayWire
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode vela playback: %v (body=%s)", err, w.Body.String())
		}
		return out, tc.getRecords()
	}

	findOriginalPath := func(paths []velaPathWire) (velaPathWire, bool) {
		for _, p := range paths {
			if p.ID == "original" {
				return p, true
			}
		}
		return velaPathWire{}, false
	}

	uhdKey := core.MovieKey(5001)
	uhdSourceH := 1080
	if idsOut.hasUHDOnly {
		uhdKey = core.MovieKey(5003)
		uhdSourceH = 2160
	}
	type velaCase struct {
		name               string
		uid                int64
		remoteAddr         string
		key                core.RatingKey
		clientMaxBitrate   int // bps
		platform           string
		wantSourceH        int
		wantPrimaryKbps    int
		wantPrimaryKind    core.DecisionKind
		wantOriginalChoice bool
		wantSwitchOriginal bool
	}
	velaCases := []velaCase{
		// Owner home, client preset 8 Mbps (< source bitrate) -> converts at 8000 kbps, Original IS offered & switchable.
		{
			name:               "owner-home-client-8mbps-offers-original",
			uid:                idsOut.ownerID,
			remoteAddr:         "127.0.0.1:43001",
			key:                uhdKey,
			clientMaxBitrate:   8_000_000,
			platform:           "tvos",
			wantSourceH:        uhdSourceH,
			wantPrimaryKbps:    8000,
			wantPrimaryKind:    core.DecisionTranscode,
			wantOriginalChoice: true,
			wantSwitchOriginal: true,
		},
		// Loose viewer home watching Anime Movie (1080p 9 Mbps <= migrated 12 Mbps cap) with 4 Mbps client preset:
		// 9 Mbps fits within the migrated 12 Mbps household rule, so Original IS offered over the 4 Mbps client cap.
		{
			name:               "loose-home-anime-9mbps-fits-12mbps-rule-offers-original-under-4mbps-preset",
			uid:                idsOut.looseID,
			remoteAddr:         "127.0.0.1:43003",
			key:                core.MovieKey(5002),
			clientMaxBitrate:   4_000_000,
			platform:           "tvos",
			wantSourceH:        1080,
			wantPrimaryKbps:    4000,
			wantPrimaryKind:    core.DecisionTranscode,
			wantOriginalChoice: true,
		},
		// Owner away: migrated away cap (8 Mbps) < 9 Mbps 1080p source, client preset (6 Mbps) is stricter.
		// Commit 276b0c893: migrated 8 Mbps away cap must be retained so Original is NOT offered.
		{
			name:               "owner-away-8mbps-rule-blocks-original-under-6mbps-preset",
			uid:                idsOut.ownerID,
			remoteAddr:         "198.51.100.10:43004",
			key:                core.MovieKey(5001),
			clientMaxBitrate:   6_000_000,
			platform:           "tvos",
			wantSourceH:        1080,
			wantPrimaryKbps:    6000,
			wantPrimaryKind:    core.DecisionTranscode,
			wantOriginalChoice: false,
		},
		// Tight viewer home: migrated 4 Mbps household cap is stricter than 8 Mbps client preset.
		// Strictest cap (4 Mbps) wins and Original is NOT offered.
		{
			name:               "tight-home-4mbps-rule-beats-8mbps-preset-and-blocks-original",
			uid:                idsOut.tightID,
			remoteAddr:         "127.0.0.1:43005",
			key:                core.MovieKey(5001),
			clientMaxBitrate:   8_000_000,
			platform:           "tvos",
			wantSourceH:        1080,
			wantPrimaryKbps:    4000,
			wantPrimaryKind:    core.DecisionTranscode,
			wantOriginalChoice: false,
		},
		// Owner home watching Anime Episode (custom library show membership inherited by episode).
		{
			name:               "owner-home-anime-episode-no-client-cap",
			uid:                idsOut.ownerID,
			remoteAddr:         "127.0.0.1:43006",
			key:                core.EpisodeKey(6002, 1, 1),
			clientMaxBitrate:   0,
			platform:           "tvos",
			wantSourceH:        1080,
			wantPrimaryKbps:    0,
			wantPrimaryKind:    core.DecisionDirectPlay,
			wantOriginalChoice: false,
		},
	}

	if idsOut.hasUHDOnly {
		// Loose viewer home on 4K-only 22 Mbps movie: migrated household safety cap (12 Mbps) < 22 Mbps source,
		// client preset (8 Mbps) is stricter. Commit 276b0c893: migrated 12 Mbps household cap must be retained
		// when evaluating the Original path so Original is NOT offered.
		velaCases = append(velaCases, velaCase{
			name:               "loose-home-12mbps-rule-blocks-original-under-8mbps-preset",
			uid:                idsOut.looseID,
			remoteAddr:         "127.0.0.1:43002",
			key:                core.MovieKey(5003),
			clientMaxBitrate:   8_000_000,
			platform:           "tvos",
			wantSourceH:        2160,
			wantPrimaryKbps:    8000,
			wantPrimaryKind:    core.DecisionTranscode,
			wantOriginalChoice: false,
		})
	}

	for _, vc := range velaCases {
		t.Run(label+"/vela/"+vc.name, func(t *testing.T) {
			resp, recs := callVelaPlay(t, vc.uid, vc.remoteAddr, vc.key, vc.clientMaxBitrate, vc.platform, label+"-"+vc.name)
			if len(recs) == 0 {
				t.Fatalf("Vela playback did not invoke transcoder.Decide")
			}
			primary := recs[0]
			gotH := 0
			if v := primary.info.Video(); v != nil {
				gotH = v.Height
			}
			if gotH != vc.wantSourceH {
				t.Errorf("Vela resolved source height = %d, want %d (caps.MaxHeight=%d source=%q)", gotH, vc.wantSourceH, primary.caps.MaxHeight, primary.caps.MaxHeightSource)
			}
			gotKbps := effectiveKbps(primary.caps, primary.opts)
			if gotKbps != vc.wantPrimaryKbps {
				t.Errorf("Vela primary effective bitrate cap = %d (caps=%d opts=%d), want %d",
					gotKbps, primary.caps.MaxBitrateKbps, primary.opts.MaxBitrateKbps, vc.wantPrimaryKbps)
			}
			if primary.dec.Kind != vc.wantPrimaryKind {
				t.Errorf("Vela primary decision kind = %s (reason=%q), want %s", primary.dec.Kind, primary.dec.Reason, vc.wantPrimaryKind)
			}
			if vc.wantPrimaryKbps > 0 && primary.dec.BitrateKbps > vc.wantPrimaryKbps {
				t.Errorf("Vela primary decision BitrateKbps = %d exceeds cap %d", primary.dec.BitrateKbps, vc.wantPrimaryKbps)
			}
			orig, hasOrig := findOriginalPath(resp.Paths)
			if hasOrig != vc.wantOriginalChoice {
				t.Errorf("Vela original choice present = %v (paths=%+v), want %v", hasOrig, resp.Paths, vc.wantOriginalChoice)
			}
			if vc.wantOriginalChoice && hasOrig {
				if !orig.ExceedsClientCap || !orig.ExceedsMaxBitrate || orig.Readiness != "on-demand" || orig.URL != "" {
					t.Errorf("Vela original path wire shape unexpected: %+v", orig)
				}
			}
			if vc.wantSwitchOriginal && hasOrig {
				postCmd := func(idemKey, body string) velaPlayWire {
					cmdReq := httptest.NewRequest(http.MethodPost, "http://localhost/vela/v0/sessions/"+resp.SessionID+"/commands", strings.NewReader(body))
					cmdReq.RemoteAddr = vc.remoteAddr
					cmdReq.Header.Set("Content-Type", "application/json")
					cmdReq.Header.Set("Authorization", "Bearer "+idsOut.velaTokens[vc.uid])
					cmdReq.Header.Set("X-Vela-Device-Id", velaDeviceID)
					cmdReq.Header.Set("Idempotency-Key", idemKey)
					cmdW := httptest.NewRecorder()
					h.ServeHTTP(cmdW, cmdReq)
					if cmdW.Code != http.StatusOK {
						t.Fatalf("Vela command %s HTTP %d: %s", idemKey, cmdW.Code, cmdW.Body.String())
					}
					var out velaPlayWire
					if err := json.Unmarshal(cmdW.Body.Bytes(), &out); err != nil {
						t.Fatalf("decode Vela command response: %v", err)
					}
					return out
				}
				switched := postCmd("switch-orig-"+label+"-"+vc.name, `{"kind":"switch","pathId":"original","atSeconds":0,"sourceGeneration":1,"seekGeneration":1}`)
				if switched.SelectedPathID != "original" {
					t.Errorf("after switch selectedPathId = %q, want original", switched.SelectedPathID)
				}
				if swOrig, ok := findOriginalPath(switched.Paths); !ok || swOrig.URL == "" {
					t.Errorf("after switch original path missing active URL: %+v", swOrig)
				}
				back := postCmd("switch-prim-"+label+"-"+vc.name, `{"kind":"switch","pathId":"primary","atSeconds":10,"sourceGeneration":2,"seekGeneration":2}`)
				if back.SelectedPathID != "primary" {
					t.Errorf("after switch back selectedPathId = %q, want primary", back.SelectedPathID)
				}
			}
			if resp.SessionID != "" {
				delReq := httptest.NewRequest(http.MethodDelete, "http://localhost/vela/v0/sessions/"+resp.SessionID, nil)
				delReq.RemoteAddr = vc.remoteAddr
				delReq.Header.Set("Authorization", "Bearer "+idsOut.velaTokens[vc.uid])
				delReq.Header.Set("X-Vela-Device-Id", velaDeviceID)
				h.ServeHTTP(httptest.NewRecorder(), delReq)
			}
		})
	}

	// 4. Viewer capabilities RemoteBitrateKbps via real Jellyfin HTTP /jellyfin/Users/Me.
	for _, want := range []struct {
		name     string
		uid      int64
		wantKbps int
	}{
		{"owner", idsOut.ownerID, 8000},
		{"tight", idsOut.tightID, 4000},
		{"loose", idsOut.looseID, 8000},
	} {
		t.Run(label+"/jellyfin-caps/"+want.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "http://localhost/jellyfin/Users/Me", nil)
			req.RemoteAddr = "198.51.100.10:44001"
			req.Header.Set("Authorization", fmt.Sprintf(`MediaBrowser Client="Web", Device="Browser", DeviceId=%q, Version="1.0", Token=%q`, jfDeviceID, idsOut.jfTokens[want.uid]))
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)
			if w.Code != http.StatusOK {
				t.Fatalf("GET /jellyfin/Users/Me HTTP %d: %s", w.Code, w.Body.String())
			}
			var me struct {
				Policy struct {
					RemoteClientBitrateLimit int `json:"RemoteClientBitrateLimit"`
				} `json:"Policy"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &me); err != nil {
				t.Fatalf("decode jellyfin /Users/Me: %v", err)
			}
			wantBps := want.wantKbps * 1000
			if me.Policy.RemoteClientBitrateLimit != wantBps {
				t.Errorf("Jellyfin RemoteClientBitrateLimit = %d, want %d", me.Policy.RemoteClientBitrateLimit, wantBps)
			}
		})
	}
}

func effectiveKbps(caps core.ClientCaps, opts core.DecideOptions) int {
	eff := caps.MaxBitrateKbps
	if opts.MaxBitrateKbps > 0 && (eff == 0 || opts.MaxBitrateKbps < eff) {
		eff = opts.MaxBitrateKbps
	}
	return eff
}

func copyDir(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0600)
	})
}

func inspectAndAugmentCopiedFixture(t *testing.T, dir string) fixtureIDs {
	t.Helper()
	stubOfflineWebClients(t, dir)
	ctx := context.Background()

	// Enable Vela and Jellyfin + loopback listener on the copied v0.3.20 config
	// without disturbing any v0.3.20 quality/transcode fields.
	cfgPath := filepath.Join(dir, "config.json")
	raw, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read copied config.json: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("unmarshal copied config.json: %v", err)
	}
	doc["frontends"] = map[string]any{
		"plex":     map[string]any{"enabled": true},
		"vela":     map[string]any{"enabled": true},
		"jellyfin": map[string]any{"enabled": true, "settings": map[string]any{"port": 0, "discovery": false}},
	}
	doc["availability"] = map[string]any{"monitor": false}
	if srv, ok := doc["server"].(map[string]any); ok {
		srv["bindAddress"] = "127.0.0.1"
		srv["port"] = freeLoopbackPort(t)
		srv["portExplicit"] = true
		srv["defaultStreamLimit"] = 64
		srv["maxStreams"] = 64
	}
	updated, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		t.Fatalf("marshal augmented config.json: %v", err)
	}
	if err := os.WriteFile(cfgPath, updated, 0600); err != nil {
		t.Fatalf("write augmented config.json: %v", err)
	}

	db, err := store.Open(ctx, dir)
	if err != nil {
		t.Fatalf("open copied db: %v", err)
	}
	defer db.Close()

	users, err := auth.New(ctx, db, auth.Options{Logger: discardLog()})
	if err != nil {
		t.Fatalf("auth.New: %v", err)
	}
	// Ensure friend Plex tokens are registered for Plex HTTP requests.
	if err := users.SyncFriends(ctx, []plextv.Friend{
		{PlexID: 1002, Username: "tight", Token: tightPlexToken, AllLibs: true},
		{PlexID: 1003, Username: "loose", Token: loosePlexToken, AllLibs: true},
	}); err != nil {
		t.Fatalf("SyncFriends: %v", err)
	}
	allUsers, err := users.Users(ctx)
	if err != nil {
		t.Fatalf("Users: %v", err)
	}
	idsOut := fixtureIDs{
		velaTokens: map[int64]string{},
		jfTokens:   map[int64]string{},
	}
	for _, u := range allUsers {
		switch u.PlexID {
		case 1001:
			idsOut.ownerID = u.ID
		case 1002:
			idsOut.tightID = u.ID
		case 1003:
			idsOut.looseID = u.ID
		}
	}
	libs, err := libraries.New(ctx, db, libraries.Options{})
	if err != nil {
		t.Fatalf("libraries.New: %v", err)
	}
	allLibs, err := libs.List(ctx)
	if err != nil {
		t.Fatalf("libraries.List: %v", err)
	}
	for _, l := range allLibs {
		switch l.Name {
		case "Anime Films":
			idsOut.animeMovieID = l.ID
		case "Anime Shows":
			idsOut.animeShowID = l.ID
		}
	}
	grants, err := identity.NewGrants(db, users, 24*time.Hour, nil)
	if err != nil {
		t.Fatalf("identity.NewGrants: %v", err)
	}
	for _, uid := range []int64{idsOut.ownerID, idsOut.tightID, idsOut.looseID} {
		tok, err := grants.Issue(ctx, uid, nil, "vela", core.Device{ID: velaDeviceID})
		if err != nil {
			t.Fatalf("issue vela grant: %v", err)
		}
		if _, err := grants.IssueRefresh(ctx, "vela", tok, core.Device{ID: velaDeviceID}); err != nil {
			t.Fatalf("issue vela refresh: %v", err)
		}
		idsOut.velaTokens[uid] = tok
		jtok, err := grants.Issue(ctx, uid, nil, "jellyfin", core.Device{ID: jfDeviceID})
		if err != nil {
			t.Fatalf("issue jf grant: %v", err)
		}
		idsOut.jfTokens[uid] = jtok
	}
	for _, tbl := range []string{"resolver_cache", "release_availability", "availability_items"} {
		_, _ = db.ExecContext(ctx, "DELETE FROM "+tbl)
	}
	return idsOut
}
