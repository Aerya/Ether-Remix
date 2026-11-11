package music

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/braedonsaunders/ether/internal/pse/localmedia"
	"github.com/braedonsaunders/ether/internal/store"
)

const ibBase int64 = 6_000_000_000_000
const ibAgent = "Ether-Remix/1.0"

func init() {
	store.Register("pse_ibroadcast", 1, `CREATE TABLE pse_ibroadcast(id INTEGER PRIMARY KEY,credentials TEXT NOT NULL);CREATE TABLE pse_ibroadcast_tracks(id INTEGER PRIMARY KEY,data TEXT NOT NULL);`)
}

type ibCredentials struct {
	Client    string
	Access    string `json:"access_token"`
	Refresh   string `json:"refresh_token"`
	ExpiresIn int    `json:"expires_in"`
	ExpiresAt int64
	UserID    int64
	Server    string
}
type ibDevice struct {
	Code        string `json:"device_code"`
	UserCode    string `json:"user_code"`
	URL         string `json:"verification_uri"`
	Complete    string `json:"verification_uri_complete"`
	Interval    int    `json:"interval"`
	Expires     int    `json:"expires_in"`
	Client      string
	Until, Next time.Time
}
type ibTrack struct {
	ID         int64
	FileID     int64
	Path, MIME string
}
type IBStatus struct {
	Connected bool   `json:"connected"`
	Client    string `json:"clientId,omitempty"`
	Tracks    int    `json:"tracks"`
	Syncing   bool   `json:"syncing"`
	Error     string `json:"error,omitempty"`
	LastSync  int64  `json:"lastSync"`
	UserCode  string `json:"userCode,omitempty"`
	URL       string `json:"url,omitempty"`
}

// IBroadcast uses the documented OAuth device flow. Passwords and access tokens
// are never exposed by its owner API. Endpoints are replaceable only in tests.
type IBroadcast struct {
	DB                           *sql.DB
	Sealer                       Sealer
	HTTP                         *http.Client
	OAuthURL, LibraryURL, APIURL string
	mu                           sync.Mutex
	syncMu                       sync.Mutex
	pending                      *ibDevice
	status                       IBStatus
}

func (s *IBroadcast) client() *http.Client {
	c := s.HTTP
	if c == nil {
		c = &http.Client{Timeout: 60 * time.Second}
	}
	cp := *c
	cp.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &cp
}
func (s *IBroadcast) oauth() string {
	if s.OAuthURL != "" {
		return s.OAuthURL
	}
	return "https://oauth.ibroadcast.com"
}
func (s *IBroadcast) load(ctx context.Context) (ibCredentials, error) {
	var c ibCredentials
	var raw string
	if s.Sealer == nil {
		return c, errors.New("chiffrement indisponible")
	}
	if err := s.DB.QueryRowContext(ctx, "SELECT credentials FROM pse_ibroadcast WHERE id=1").Scan(&raw); err != nil {
		return c, err
	}
	plain, _, err := s.Sealer.OpenField(raw, "pse-ibroadcast", "server")
	if err != nil {
		return c, err
	}
	err = json.Unmarshal([]byte(plain), &c)
	return c, err
}
func (s *IBroadcast) save(ctx context.Context, c ibCredentials) error {
	if s.Sealer == nil {
		return errors.New("chiffrement indisponible")
	}
	raw, err := json.Marshal(c)
	if err != nil {
		return err
	}
	sealed, err := s.Sealer.SealField(string(raw), "pse-ibroadcast", "server")
	if err != nil {
		return err
	}
	_, err = s.DB.ExecContext(ctx, "INSERT INTO pse_ibroadcast(id,credentials) VALUES(1,?) ON CONFLICT(id) DO UPDATE SET credentials=excluded.credentials", sealed)
	return err
}
func (s *IBroadcast) Status(ctx context.Context) IBStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	v := s.status
	c, err := s.load(ctx)
	v.Connected = err == nil && c.Refresh != ""
	v.Client = c.Client
	_ = s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM pse_ibroadcast_tracks").Scan(&v.Tracks)
	if p := s.pending; p != nil && time.Now().Before(p.Until) {
		v.UserCode = p.UserCode
		v.URL = p.Complete
		if v.URL == "" {
			v.URL = p.URL
		}
	}
	return v
}
func (s *IBroadcast) Begin(ctx context.Context, client string) error {
	if s.Sealer == nil {
		return errors.New("chiffrement indisponible")
	}
	client = strings.TrimSpace(client)
	if client == "" || len(client) > 256 || strings.ContainsAny(client, "\r\n\t ") {
		return errors.New("Client ID iBroadcast requis")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	req, _ := http.NewRequestWithContext(ctx, "GET", s.oauth()+"/device/code?"+url.Values{"client_id": {client}, "scope": {"user.account:read user.library:read"}}.Encode(), nil)
	req.Close = true
	res, err := s.client().Do(req)
	if err != nil {
		return errors.New("serveur OAuth iBroadcast injoignable")
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return fmt.Errorf("connexion iBroadcast refusée (%d)", res.StatusCode)
	}
	var p ibDevice
	if json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&p) != nil {
		return errors.New("réponse OAuth iBroadcast invalide")
	}
	// The official device endpoint also returns this scheme-less URI.
	if p.URL == "ibroadcast.com/device" {
		p.URL = "https://ibroadcast.com/device"
	}
	if p.Code == "" || p.UserCode == "" || !ibHost(p.URL) {
		return errors.New("réponse OAuth iBroadcast invalide")
	}
	if p.Complete != "" && !ibHost(p.Complete) {
		return errors.New("lien OAuth iBroadcast invalide")
	}
	if p.Interval < 5 {
		p.Interval = 5
	}
	if p.Expires <= 0 || p.Expires > 3600 {
		p.Expires = 600
	}
	p.Client = client
	p.Until = time.Now().Add(time.Duration(p.Expires) * time.Second)
	p.Next = time.Now().Add(time.Duration(p.Interval) * time.Second)
	s.pending = &p
	return nil
}
func ibHost(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "https" && u.User == nil && u.Port() == "" && (u.Hostname() == "ibroadcast.com" || strings.HasSuffix(u.Hostname(), ".ibroadcast.com"))
}

type ibTokenError struct{ Code string }

func (e ibTokenError) Error() string { return "iBroadcast OAuth : " + e.Code }
func (s *IBroadcast) tokenRequest(ctx context.Context, q url.Values) (ibCredentials, error) {
	var c ibCredentials
	req, _ := http.NewRequestWithContext(ctx, "POST", s.oauth()+"/token", strings.NewReader(q.Encode()))
	// The OAuth server can close idle keep-alive connections without a response.
	req.Close = true
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", ibAgent)
	res, err := s.client().Do(req)
	if err != nil {
		return c, errors.New("serveur OAuth iBroadcast injoignable")
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return c, err
	}
	if res.StatusCode != 200 {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(raw, &e)
		switch e.Error {
		case "authorization_pending", "slow_down", "expired_token", "access_denied", "invalid_grant":
			return c, ibTokenError{e.Error}
		}
		return c, fmt.Errorf("connexion iBroadcast refusée (%d)", res.StatusCode)
	}
	if json.Unmarshal(raw, &c) != nil || c.Access == "" || c.ExpiresIn <= 0 {
		return c, errors.New("jeton iBroadcast invalide")
	}
	c.ExpiresAt = time.Now().Add(time.Duration(c.ExpiresIn) * time.Second).Unix()
	return c, nil
}
func (s *IBroadcast) Poll(ctx context.Context) (bool, error) {
	// Never change accounts halfway through publication of a library.
	if !s.syncMu.TryLock() {
		return false, nil
	}
	defer s.syncMu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.pending
	if p == nil {
		return false, nil
	}
	if time.Now().After(p.Until) {
		s.pending = nil
		return false, errors.New("code expiré : relancez la connexion")
	}
	if time.Now().Before(p.Next) {
		return false, nil
	}
	p.Next = time.Now().Add(time.Duration(p.Interval) * time.Second)
	c, err := s.tokenRequest(ctx, url.Values{"grant_type": {"device_code"}, "device_code": {p.Code}, "client_id": {p.Client}})
	var te ibTokenError
	if errors.As(err, &te) && (te.Code == "authorization_pending" || te.Code == "slow_down") {
		if te.Code == "slow_down" {
			p.Interval += 5
			p.Next = time.Now().Add(time.Duration(p.Interval) * time.Second)
		}
		return false, nil
	}
	if err != nil {
		s.pending = nil
		return false, err
	}
	if c.Refresh == "" {
		return false, errors.New("jeton de renouvellement absent")
	}
	c.Client = p.Client
	if err = s.save(ctx, c); err != nil {
		return false, err
	}
	s.pending = nil
	return true, nil
}
func (s *IBroadcast) credentials(ctx context.Context, force bool) (ibCredentials, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, err := s.load(ctx)
	if err != nil {
		return c, errors.New("connectez le compte iBroadcast")
	}
	if !force && c.ExpiresAt > time.Now().Add(time.Minute).Unix() {
		return c, nil
	}
	n, err := s.tokenRequest(ctx, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {c.Refresh}, "client_id": {c.Client}})
	if err != nil {
		return c, err
	}
	n.Client, n.UserID, n.Server = c.Client, c.UserID, c.Server
	if n.Refresh == "" {
		n.Refresh = c.Refresh
	}
	return n, s.save(ctx, n)
}
func (s *IBroadcast) Disconnect(ctx context.Context) error {
	s.syncMu.Lock()
	defer s.syncMu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pending = nil
	// Local disconnect always succeeds independently of provider reachability.
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, q := range []string{"DELETE FROM pse_ibroadcast", "DELETE FROM pse_ibroadcast_tracks", "DELETE FROM pse_music_owned WHERE provider='ibroadcast'"} {
		if _, err = tx.ExecContext(ctx, q); err != nil {
			return err
		}
	}
	return tx.Commit()
}

type ibResponse struct {
	Result        bool
	Authenticated *bool
	User          struct{ ID json.Number }
	Settings      struct {
		Streaming string `json:"streaming_server"`
	}
	Library map[string]json.RawMessage
}

func (s *IBroadcast) api(ctx context.Context, endpoint, mode string) (ibResponse, error) {
	var out ibResponse
	for attempt := 0; attempt < 2; attempt++ {
		c, err := s.credentials(ctx, attempt > 0)
		if err != nil {
			return out, err
		}
		body, _ := json.Marshal(map[string]string{"mode": mode, "client": "ether-remix", "version": "1.0", "device_name": "Ether-Remix", "user_agent": ibAgent})
		req, _ := http.NewRequestWithContext(ctx, "POST", endpoint, strings.NewReader(string(body)))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", ibAgent)
		req.Header.Set("Authorization", "Bearer "+c.Access)
		req.Close = true
		res, err := s.client().Do(req)
		if err != nil {
			return out, errors.New("API iBroadcast injoignable")
		}
		if res.StatusCode == 401 {
			res.Body.Close()
			continue
		}
		if res.StatusCode != 200 {
			res.Body.Close()
			return out, fmt.Errorf("API iBroadcast : HTTP %d", res.StatusCode)
		}
		err = json.NewDecoder(io.LimitReader(res.Body, 128<<20)).Decode(&out)
		res.Body.Close()
		if err != nil {
			return out, errors.New("réponse iBroadcast illisible")
		}
		if out.Authenticated != nil && !*out.Authenticated {
			continue
		}
		if !out.Result {
			return out, errors.New("requête refusée par iBroadcast")
		}
		return out, nil
	}
	return out, errors.New("reconnectez le compte iBroadcast")
}

type ibTable struct {
	Rows map[string][]json.RawMessage
	Map  map[string]int
}

func parseIBTable(raw json.RawMessage) (ibTable, error) {
	t := ibTable{Rows: map[string][]json.RawMessage{}, Map: map[string]int{}}
	var all map[string]json.RawMessage
	if json.Unmarshal(raw, &all) != nil {
		return t, errors.New("table iBroadcast invalide")
	}
	var columns map[string]json.RawMessage
	if json.Unmarshal(all["map"], &columns) != nil || len(columns) == 0 {
		return t, errors.New("colonnes iBroadcast manquantes")
	}
	for name, raw := range columns {
		var index int
		if json.Unmarshal(raw, &index) == nil && index >= 0 {
			t.Map[name] = index
		}
	}
	if len(t.Map) == 0 {
		return t, errors.New("colonnes iBroadcast manquantes")
	}
	for id, v := range all {
		if id == "map" {
			continue
		}
		if _, err := strconv.ParseInt(id, 10, 64); err != nil {
			continue
		}
		var row []json.RawMessage
		if json.Unmarshal(v, &row) != nil {
			return t, errors.New("ligne iBroadcast invalide")
		}
		t.Rows[id] = row
	}
	return t, nil
}
func (t ibTable) value(id, key string) json.RawMessage {
	idx, ok := t.Map[key]
	row := t.Rows[id]
	if !ok || idx < 0 || idx >= len(row) {
		return nil
	}
	return row[idx]
}
func (t ibTable) text(id, key string) string {
	var s string
	if json.Unmarshal(t.value(id, key), &s) == nil {
		return s
	}
	return string(t.value(id, key))
}
func (t ibTable) num(id, key string) int64 {
	n, _ := strconv.ParseInt(t.text(id, key), 10, 64)
	return n
}
func (t ibTable) trashed(id string) bool {
	return t.text(id, "trashed") == "true" || t.text(id, "trashed") == "1"
}
func (s *IBroadcast) Sync(ctx context.Context) error {
	s.syncMu.Lock()
	defer s.syncMu.Unlock()
	s.mu.Lock()
	s.status.Syncing = true
	s.status.Error = ""
	s.mu.Unlock()
	err := s.sync(ctx)
	s.mu.Lock()
	s.status.Syncing = false
	if err != nil {
		s.status.Error = err.Error()
	} else {
		s.status.LastSync = time.Now().Unix()
	}
	s.mu.Unlock()
	return err
}
func (s *IBroadcast) sync(ctx context.Context) error {
	endpoint := s.APIURL
	if endpoint == "" {
		endpoint = "https://api.ibroadcast.com/"
	}
	status, err := s.api(ctx, endpoint, "status")
	if err != nil {
		return err
	}
	userID, _ := status.User.ID.Int64()
	if userID <= 0 || !ibHost(status.Settings.Streaming) {
		return errors.New("compte ou serveur de lecture iBroadcast invalide")
	}
	endpoint = s.LibraryURL
	if endpoint == "" {
		endpoint = "https://library.ibroadcast.com/"
	}
	lib, err := s.api(ctx, endpoint, "library")
	if err != nil {
		return err
	}
	tracks, err := parseIBTable(lib.Library["tracks"])
	if err != nil {
		return err
	}
	albums, err := parseIBTable(lib.Library["albums"])
	if err != nil {
		return err
	}
	artists, err := parseIBTable(lib.Library["artists"])
	if err != nil {
		return err
	}
	tt := []Track{}
	links := map[int64]ibTrack{}
	user := strconv.FormatInt(userID, 10)
	for id := range tracks.Rows {
		if tracks.trashed(id) {
			continue
		}
		aid := tracks.text(id, "artist_id")
		alid := tracks.text(id, "album_id")
		if albums.trashed(alid) || artists.trashed(aid) {
			continue
		}
		trackID := localmedia.ID("ib-track:"+user, id, ibBase)
		artist := Artist{ID: localmedia.ID("ib-artist:"+user, aid, ibBase), Name: artists.text(aid, "name")}
		albumArtist := artist
		if albumAID := albums.text(alid, "artist_id"); albumAID != "" {
			albumArtist = Artist{ID: localmedia.ID("ib-artist:"+user, albumAID, ibBase), Name: artists.text(albumAID, "name")}
		}
		album := Album{ID: localmedia.ID("ib-album:"+user, alid, ibBase), Title: albums.text(alid, "name"), Artist: albumArtist}
		if art := tracks.num(id, "artwork_id"); art > 0 {
			album.Cover = "ib-" + strconv.FormatInt(art, 10)
		}
		path := tracks.text(id, "file")
		if path == "" {
			path = tracks.text(id, "url")
		}
		u, e := url.Parse(path)
		if e != nil || u.IsAbs() || u.Host != "" || !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") || u.RawQuery != "" || u.Fragment != "" {
			return errors.New("chemin de lecture iBroadcast invalide")
		}
		fileID := tracks.num(id, "file_id")
		if fileID == 0 {
			fileID, _ = strconv.ParseInt(id, 10, 64)
		}
		links[trackID] = ibTrack{ID: trackID, FileID: fileID, Path: path, MIME: tracks.text(id, "type")}
		tt = append(tt, Track{MIME: "audio/mpeg", Container: "mp3", Codec: "mp3", ID: trackID, Title: tracks.text(id, "title"), Duration: int(tracks.num(id, "length")), Number: int(tracks.num(id, "track")), Volume: int(albums.num(alid, "disc")), Artist: artist, Album: album})
	}
	// Publish links first; both snapshots are serialized against disconnect.
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "DELETE FROM pse_ibroadcast_tracks"); err != nil {
		return err
	}
	for id, link := range links {
		raw, _ := json.Marshal(link)
		if _, err = tx.ExecContext(ctx, "INSERT INTO pse_ibroadcast_tracks(id,data) VALUES(?,?)", id, string(raw)); err != nil {
			return err
		}
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	s.mu.Lock()
	c, err := s.load(ctx)
	if err == nil {
		c.UserID = userID
		c.Server = status.Settings.Streaming
		err = s.save(ctx, c)
	}
	s.mu.Unlock()
	if err != nil {
		return err
	}
	return ReplaceOwned(ctx, s.DB, "ibroadcast", tt)
}
func (s *IBroadcast) Stream(ctx context.Context, id int64, quality string) (Stream, error) {
	var raw string
	if s.DB.QueryRowContext(ctx, "SELECT data FROM pse_ibroadcast_tracks WHERE id=?", id).Scan(&raw) != nil {
		return Stream{}, ErrNotFound
	}
	var t ibTrack
	if json.Unmarshal([]byte(raw), &t) != nil {
		return Stream{}, ErrNotFound
	}
	c, err := s.credentials(ctx, false)
	if err != nil {
		return Stream{}, err
	}
	if !ibHost(c.Server) || c.UserID == 0 {
		return Stream{}, ErrNotFound
	}
	// Standard 128 kbps is available to every account; original quality depends
	// on the account's iBroadcast plan and is deliberately not assumed.
	u, err := url.Parse(strings.TrimRight(c.Server, "/") + t.Path)
	if err != nil || !ibHost(u.String()) {
		return Stream{}, ErrNotFound
	}
	u.RawQuery = url.Values{"Expires": {strconv.FormatInt(time.Now().UnixMilli(), 10)}, "Signature": {c.Access}, "file_id": {strconv.FormatInt(t.FileID, 10)}, "user_id": {strconv.FormatInt(c.UserID, 10)}, "platform": {"ether-remix"}, "version": {"1.0"}}.Encode()
	return Stream{Provider: "ibroadcast", URL: u.String(), Mime: "audio/mpeg", Quality: "128", ExpiresAt: c.ExpiresAt}, nil
}
func (s *IBroadcast) ImageURL(ref string, size int) string {
	id, err := strconv.ParseInt(strings.TrimPrefix(ref, "ib-"), 10, 64)
	if err != nil || id <= 0 {
		return ""
	}
	return "https://artwork.ibroadcast.com/artwork/" + strconv.FormatInt(id, 10) + "-" + strconv.Itoa(pickSize([]int{150, 300, 1000}, size))
}
