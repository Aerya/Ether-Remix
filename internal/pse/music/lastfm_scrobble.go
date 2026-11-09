package music

import (
	"context"
	"crypto/md5"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/pse/pseenv"
	"github.com/braedonsaunders/ether/internal/store"
)

// Envoi des écoutes à Last.fm (« scrobbling »). Chaque spectateur autorise
// PSE une fois sur last.fm (application de bureau : jeton puis session) ;
// la clé de session est gardée chiffrée par Ether. Les écoutes complètes
// sont envoyées par lots de 50 toutes les deux minutes ; celles d'avant la
// liaison ou de plus de 14 jours (refusées par Last.fm) ne le sont pas.
func init() {
	store.Register("pse_music_scrobble", 1, `
CREATE TABLE pse_music_scrobble(
  viewer_id INTEGER PRIMARY KEY,
  username TEXT NOT NULL DEFAULT '',
  session TEXT NOT NULL DEFAULT '',
  linked_at INTEGER NOT NULL DEFAULT 0,
  sent_at INTEGER NOT NULL DEFAULT 0,
  error TEXT NOT NULL DEFAULT ''
);`)
	store.RegisterViewerTable("pse_music_scrobble", "pse_music_scrobble", "viewer_id", store.ViewerMergeMove)
}

// LastFMSecret renvoie le secret partagé de l'application Last.fm
// (PSE_LASTFM_SECRET), nécessaire à l'envoi des écoutes.
func LastFMSecret() string { return strings.TrimSpace(pseenv.Get("PSE_LASTFM_SECRET")) }

const (
	scrobbleBatch  = 50
	scrobbleMaxAge = 14 * 24 * time.Hour
	scrobbleEvery  = 2 * time.Minute
)

// Sealer chiffre la clé de session (config.FieldSealer d'Ether).
type Sealer interface {
	SealField(plain string, context ...string) (string, error)
	OpenField(stored string, context ...string) (plain string, legacy bool, err error)
}

// Scrobbler relie les spectateurs à Last.fm et envoie leurs écoutes.
type Scrobbler struct {
	DB      *sql.DB
	Library *Library
	Sealer  Sealer
	Key     func() string // clé API
	Secret  func() string // secret partagé
	API     string        // LastFMAPI par défaut
	HTTP    *http.Client
	Now     func() time.Time
	Log     *slog.Logger
}

// ScrobbleLink est l'état de la liaison d'un spectateur.
type ScrobbleLink struct {
	Username string `json:"username"`
	LinkedAt int64  `json:"linkedAt"`
	SentAt   int64  `json:"sentAt,omitempty"`
	Sent     int    `json:"sent"`
	Error    string `json:"error,omitempty"`
}

func (s *Scrobbler) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// Ready indique si la clé et le secret de l'application sont renseignés.
func (s *Scrobbler) Ready() bool {
	return s.Key != nil && s.Secret != nil && s.Key() != "" && s.Secret() != ""
}

// sign ajoute api_key et api_sig aux paramètres d'un appel authentifié.
func (s *Scrobbler) sign(q url.Values) url.Values {
	q.Set("api_key", s.Key())
	keys := make([]string, 0, len(q))
	for k := range q {
		if k != "format" && k != "callback" {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		b.WriteString(k)
		b.WriteString(q.Get(k))
	}
	b.WriteString(s.Secret())
	sum := md5.Sum([]byte(b.String()))
	q.Set("api_sig", hex.EncodeToString(sum[:]))
	q.Set("format", "json")
	return q
}

type lastfmError struct {
	Code    int    `json:"error"`
	Message string `json:"message"`
}

func (e *lastfmError) Error() string { return fmt.Sprintf("Last.fm %d : %s", e.Code, e.Message) }

// call envoie un appel signé (POST) et décode la réponse.
func (s *Scrobbler) call(ctx context.Context, q url.Values, into any) error {
	if !s.Ready() {
		return errors.New("clé API et secret Last.fm manquants (Ether-Remix › Musique)")
	}
	base := s.API
	if base == "" {
		base = LastFMAPI
	}
	client := s.HTTP
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base, strings.NewReader(s.sign(q).Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", "Ether-Remix")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	var le lastfmError
	if json.Unmarshal(body, &le) == nil && le.Code != 0 {
		return &le
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("Last.fm : HTTP %d", resp.StatusCode)
	}
	return json.Unmarshal(body, into)
}

// AuthURL demande un jeton et renvoie la page d'autorisation Last.fm.
func (s *Scrobbler) AuthURL(ctx context.Context) (token, link string, err error) {
	var out struct {
		Token string `json:"token"`
	}
	if err := s.call(ctx, url.Values{"method": {"auth.getToken"}}, &out); err != nil {
		return "", "", err
	}
	if out.Token == "" {
		return "", "", errors.New("Last.fm n'a pas renvoyé de jeton")
	}
	return out.Token, "https://www.last.fm/api/auth/?" + url.Values{"api_key": {s.Key()}, "token": {out.Token}}.Encode(), nil
}

func sealContext(viewer int64) []string {
	return []string{"pse-lastfm-session", strconv.FormatInt(viewer, 10)}
}

// Confirm échange le jeton autorisé contre une session et lie le spectateur.
func (s *Scrobbler) Confirm(ctx context.Context, viewer int64, token string) (ScrobbleLink, error) {
	var out struct {
		Session struct {
			Name string `json:"name"`
			Key  string `json:"key"`
		} `json:"session"`
	}
	if err := s.call(ctx, url.Values{"method": {"auth.getSession"}, "token": {token}}, &out); err != nil {
		var le *lastfmError
		if errors.As(err, &le) && (le.Code == 14 || le.Code == 15) {
			return ScrobbleLink{}, errors.New("autorisation pas encore donnée sur last.fm : cliquez sur « Oui, autoriser » puis réessayez")
		}
		return ScrobbleLink{}, err
	}
	if out.Session.Key == "" {
		return ScrobbleLink{}, errors.New("Last.fm n'a pas renvoyé de session")
	}
	stored := out.Session.Key
	if s.Sealer != nil {
		sealed, err := s.Sealer.SealField(out.Session.Key, sealContext(viewer)...)
		if err != nil {
			return ScrobbleLink{}, err
		}
		stored = sealed
	}
	now := s.now().Unix()
	if _, err := s.DB.ExecContext(ctx, `INSERT INTO pse_music_scrobble(viewer_id, username, session, linked_at) VALUES(?,?,?,?)
ON CONFLICT(viewer_id) DO UPDATE SET username=excluded.username, session=excluded.session, linked_at=excluded.linked_at, error=''`,
		viewer, out.Session.Name, stored, now); err != nil {
		return ScrobbleLink{}, err
	}
	return ScrobbleLink{Username: out.Session.Name, LinkedAt: now}, nil
}

// Unlink retire la liaison d'un spectateur.
func (s *Scrobbler) Unlink(ctx context.Context, viewer int64) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM pse_music_scrobble WHERE viewer_id=?`, viewer)
	return err
}

// Links renvoie la liaison de chaque spectateur lié.
func (s *Scrobbler) Links(ctx context.Context) (map[int64]ScrobbleLink, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT viewer_id, username, linked_at, sent_at, error FROM pse_music_scrobble`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]ScrobbleLink{}
	for rows.Next() {
		var v int64
		var l ScrobbleLink
		if err := rows.Scan(&v, &l.Username, &l.LinkedAt, &l.SentAt, &l.Error); err != nil {
			return nil, err
		}
		out[v] = l
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if s.Library != nil {
		for v, l := range out {
			l.Sent = s.Library.sentPlays(ctx, v)
			out[v] = l
		}
	}
	return out, nil
}

type scrobbleTarget struct {
	viewer   int64
	session  string
	linkedAt int64
}

func (s *Scrobbler) targets(ctx context.Context) ([]scrobbleTarget, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT viewer_id, session, linked_at FROM pse_music_scrobble WHERE session<>''`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []scrobbleTarget
	for rows.Next() {
		var t scrobbleTarget
		var stored string
		if err := rows.Scan(&t.viewer, &stored, &t.linkedAt); err != nil {
			return nil, err
		}
		t.session = stored
		if s.Sealer != nil {
			plain, _, err := s.Sealer.OpenField(stored, sealContext(t.viewer)...)
			if err != nil {
				s.setError(ctx, t.viewer, "clé de session illisible : reliez le compte Last.fm")
				continue
			}
			t.session = plain
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Scrobbler) setError(ctx context.Context, viewer int64, msg string) {
	_, _ = s.DB.ExecContext(ctx, `UPDATE pse_music_scrobble SET error=? WHERE viewer_id=?`, msg, viewer)
}

// Flush envoie les écoutes en attente de chaque spectateur lié.
func (s *Scrobbler) Flush(ctx context.Context) error {
	if !s.Ready() || s.Library == nil {
		return nil
	}
	targets, err := s.targets(ctx)
	if err != nil {
		return err
	}
	since := s.now().Add(-scrobbleMaxAge).Unix()
	for _, t := range targets {
		from := max(since, t.linkedAt)
		for {
			plays, err := s.Library.pendingPlays(ctx, t.viewer, from, scrobbleBatch)
			if err != nil {
				return err
			}
			if len(plays) == 0 {
				break
			}
			if err := s.send(ctx, t, plays); err != nil {
				s.setError(ctx, t.viewer, err.Error())
				var le *lastfmError
				if errors.As(err, &le) && le.Code == 9 { // session invalide : compte délié
					_, _ = s.DB.ExecContext(ctx, `UPDATE pse_music_scrobble SET session='' WHERE viewer_id=?`, t.viewer)
				}
				break
			}
			if len(plays) < scrobbleBatch {
				break
			}
		}
	}
	return nil
}

func (s *Scrobbler) send(ctx context.Context, t scrobbleTarget, plays []play) error {
	q := url.Values{"method": {"track.scrobble"}, "sk": {t.session}}
	var ids, skipped []int64
	n := 0
	for _, p := range plays {
		if p.artist == "" || p.title == "" {
			skipped = append(skipped, p.id)
			continue
		}
		i := "[" + strconv.Itoa(n) + "]"
		q.Set("artist"+i, p.artist)
		q.Set("track"+i, p.title)
		q.Set("timestamp"+i, strconv.FormatInt(p.at, 10))
		if p.album != "" {
			q.Set("album"+i, p.album)
		}
		if p.duration > 0 {
			q.Set("duration"+i, strconv.Itoa(p.duration))
		}
		ids = append(ids, p.id)
		n++
	}
	if len(skipped) > 0 {
		if err := s.Library.markPlays(ctx, skipped, scrobbleSkipped); err != nil {
			return err
		}
	}
	if n == 0 {
		return nil
	}
	var out struct {
		Scrobbles struct {
			Attr struct {
				Accepted int `json:"accepted"`
				Ignored  int `json:"ignored"`
			} `json:"@attr"`
		} `json:"scrobbles"`
	}
	if err := s.call(ctx, q, &out); err != nil {
		return err
	}
	if err := s.Library.markPlays(ctx, ids, scrobbleSent); err != nil {
		return err
	}
	_, _ = s.DB.ExecContext(ctx, `UPDATE pse_music_scrobble SET sent_at=?, error='' WHERE viewer_id=?`, s.now().Unix(), t.viewer)
	if s.Log != nil {
		s.Log.Debug("écoutes envoyées à Last.fm", "viewer", t.viewer, "accepted", out.Scrobbles.Attr.Accepted, "ignored", out.Scrobbles.Attr.Ignored)
	}
	return nil
}

// Run envoie les écoutes toutes les deux minutes jusqu'à l'arrêt de ctx.
func (s *Scrobbler) Run(ctx context.Context) {
	t := time.NewTicker(scrobbleEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		if err := s.Flush(ctx); err != nil && ctx.Err() == nil && s.Log != nil {
			s.Log.Warn("envoi des écoutes à Last.fm impossible", "err", err)
		}
	}
}

// Handler renvoie l'API de la console (/ether/api/pse/music/lastfm/).
// known vérifie qu'un spectateur existe.
func (s *Scrobbler) Handler(known func(context.Context, int64) bool) http.Handler {
	mux := http.NewServeMux()
	reply := func(w http.ResponseWriter, r *http.Request) {
		links, err := s.Links(r.Context())
		if err != nil {
			apiJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		out := map[string]ScrobbleLink{}
		for v, l := range links {
			out[strconv.FormatInt(v, 10)] = l
		}
		apiJSON(w, http.StatusOK, map[string]any{"ready": s.Ready(), "links": out})
	}
	body := func(w http.ResponseWriter, r *http.Request) (int64, string, bool) {
		var b struct {
			Viewer int64  `json:"viewer"`
			Token  string `json:"token"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&b); err != nil || !known(r.Context(), b.Viewer) {
			apiJSON(w, http.StatusBadRequest, map[string]string{"error": "spectateur inconnu"})
			return 0, "", false
		}
		return b.Viewer, strings.TrimSpace(b.Token), true
	}
	mux.HandleFunc("GET /{$}", reply)
	mux.HandleFunc("POST /connect", func(w http.ResponseWriter, r *http.Request) {
		if _, _, ok := body(w, r); !ok {
			return
		}
		token, link, err := s.AuthURL(r.Context())
		if err != nil {
			apiJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
			return
		}
		apiJSON(w, http.StatusOK, map[string]string{"token": token, "url": link})
	})
	mux.HandleFunc("POST /confirm", func(w http.ResponseWriter, r *http.Request) {
		viewer, token, ok := body(w, r)
		if !ok {
			return
		}
		if token == "" {
			apiJSON(w, http.StatusBadRequest, map[string]string{"error": "jeton manquant"})
			return
		}
		if _, err := s.Confirm(r.Context(), viewer, token); err != nil {
			apiJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		reply(w, r)
	})
	mux.HandleFunc("POST /unlink", func(w http.ResponseWriter, r *http.Request) {
		viewer, _, ok := body(w, r)
		if !ok {
			return
		}
		if err := s.Unlink(r.Context(), viewer); err != nil {
			apiJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		reply(w, r)
	})
	return mux
}

// StatsHandler renvoie l'API des statistiques (/ether/api/pse/music/stats).
func StatsHandler(l *Library) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "méthode non autorisée", http.StatusMethodNotAllowed)
			return
		}
		days, _ := strconv.Atoi(r.URL.Query().Get("days"))
		if days == 0 {
			days = 30
		}
		st, err := l.Stats(r.Context(), min(max(days, 1), 365))
		if err != nil {
			apiJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		apiJSON(w, http.StatusOK, st)
	})
}
