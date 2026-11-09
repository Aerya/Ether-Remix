// Package scrobpush envoie les lectures d'Ether à Scrob en temps réel, par
// le webhook Jellyfin de Scrob (format « imbriqué » : Item, Session), comme
// le ferait un serveur Jellyfin. Chaque spectateur a son propre lien webhook
// (il porte la clé API de son compte Scrob). Ajout PSE : voir
// docs/pse/FORK.fr.md.
package scrobpush

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/pse/pseenv"
	"github.com/braedonsaunders/ether/internal/state"
	"github.com/braedonsaunders/ether/internal/store"
)

func init() {
	store.Register("pse_scrob_links", 1, `
CREATE TABLE pse_scrob_links(
  viewer_id INTEGER PRIMARY KEY,
  url TEXT NOT NULL,
  last_status TEXT NOT NULL DEFAULT '',
  last_at INTEGER NOT NULL DEFAULT 0
);
`)
	store.RegisterViewerTable("pse_scrob_links", "pse_scrob_links", "viewer_id", store.ViewerMergeDrop)
}

// Enabled : PSE_SCROB_PUSH=on.
func Enabled() bool {
	switch strings.ToLower(strings.TrimSpace(pseenv.Get("PSE_SCROB_PUSH"))) {
	case "on", "1", "true", "yes", "oui":
		return true
	}
	return false
}

// Links gère les liens webhook Scrob des spectateurs.
type Links struct{ DB *sql.DB }

// Link est le lien d'un spectateur et le résultat du dernier envoi.
type Link struct {
	ViewerID   int64
	URL        string
	LastStatus string
	LastAt     int64
}

// ValidateURL vérifie qu'un lien ressemble à un webhook Scrob.
func ValidateURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", errors.New("lien http(s)://… attendu")
	}
	if !strings.Contains(u.Path, "/webhooks/") {
		return "", errors.New("lien webhook Scrob attendu (…/api/proxy/webhooks/jellyfin…?api_key=…)")
	}
	if u.Query().Get("api_key") == "" {
		return "", errors.New("le lien doit contenir api_key=…")
	}
	return raw, nil
}

// All renvoie tous les liens.
func (l Links) All(ctx context.Context) (map[int64]Link, error) {
	rows, err := l.DB.QueryContext(ctx, `SELECT viewer_id, url, last_status, last_at FROM pse_scrob_links`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]Link{}
	for rows.Next() {
		var k Link
		if err := rows.Scan(&k.ViewerID, &k.URL, &k.LastStatus, &k.LastAt); err != nil {
			return nil, err
		}
		out[k.ViewerID] = k
	}
	return out, rows.Err()
}

// Set enregistre (ou efface, si raw est vide) le lien d'un spectateur.
func (l Links) Set(ctx context.Context, viewer int64, raw string) error {
	if strings.TrimSpace(raw) == "" {
		_, err := l.DB.ExecContext(ctx, `DELETE FROM pse_scrob_links WHERE viewer_id=?`, viewer)
		return err
	}
	v, err := ValidateURL(raw)
	if err != nil {
		return err
	}
	_, err = l.DB.ExecContext(ctx, `INSERT INTO pse_scrob_links(viewer_id, url) VALUES(?, ?)
ON CONFLICT(viewer_id) DO UPDATE SET url=excluded.url, last_status='', last_at=0`, viewer, v)
	return err
}

func (l Links) get(ctx context.Context, viewer int64) (string, error) {
	var u string
	err := l.DB.QueryRowContext(ctx, `SELECT url FROM pse_scrob_links WHERE viewer_id=?`, viewer).Scan(&u)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return u, err
}

func (l Links) report(viewer int64, status string, at time.Time) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, _ = l.DB.ExecContext(ctx, `UPDATE pse_scrob_links SET last_status=?, last_at=? WHERE viewer_id=?`, status, at.Unix(), viewer)
}

// MaskURL n'affiche que l'hôte et le chemin : la clé API reste cachée.
func MaskURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	q := u.Query()
	if q.Get("api_key") != "" {
		q.Set("api_key", "••••••")
	}
	u.RawQuery = q.Encode()
	s := u.String()
	return strings.Replace(s, url.QueryEscape("••••••"), "••••••", 1)
}

// Item décrit le titre lu, tel que Scrob l'attend.
type Item struct {
	Name        string
	Year        int
	RuntimeSecs int64
}

// Lookup renvoie le nom, l'année et la durée d'un titre (facultatif).
type Lookup func(ctx context.Context, key core.RatingKey) (Item, bool)

// Pusher relaie les événements de lecture vers Scrob.
type Pusher struct {
	Links   Links
	Enabled func() bool
	Lookup  Lookup
	Users   func(ctx context.Context, id int64) string // nom affiché
	HTTP    *http.Client
	Log     *slog.Logger
	Now     func() time.Time

	mu       sync.Mutex
	sessions map[sessionKey]*session
	queue    chan job
	once     sync.Once
}

type sessionKey struct {
	viewer int64
	key    core.RatingKey
}

type session struct {
	id       string
	started  bool
	lastSent time.Time
	paused   bool
}

type job struct {
	viewer  int64
	payload map[string]any
}

// progressEvery espace les mises à jour de position pendant la lecture.
const progressEvery = 60 * time.Second

func (p *Pusher) init() {
	p.once.Do(func() {
		if p.HTTP == nil {
			p.HTTP = &http.Client{Timeout: 15 * time.Second}
		}
		if p.Log == nil {
			p.Log = slog.New(slog.NewTextHandler(io.Discard, nil))
		}
		if p.Now == nil {
			p.Now = time.Now
		}
		p.sessions = map[sessionKey]*session{}
		p.queue = make(chan job, 256)
	})
}

// Run envoie les webhooks en file jusqu'à l'annulation de ctx.
func (p *Pusher) Run(ctx context.Context) {
	p.init()
	for {
		select {
		case <-ctx.Done():
			return
		case j := <-p.queue:
			p.send(ctx, j)
		}
	}
}

// Handle convertit un événement d'Ether en webhook Scrob.
func (p *Pusher) Handle(ev state.Event) {
	p.init()
	if p.Enabled != nil && !p.Enabled() {
		return
	}
	if ev.Imported || ev.Account || ev.UserID <= 0 || !ev.Key.Valid() || ev.Key.TMDB() <= 0 {
		return
	}
	kind := ev.Key.Kind()
	if kind != core.KindMovie && kind != core.KindEpisode {
		return
	}
	var notification string
	var position time.Duration
	paused := false
	sk := sessionKey{ev.UserID, ev.Key}
	now := p.Now()

	p.mu.Lock()
	s := p.sessions[sk]
	switch ev.Kind {
	case state.EventProgress:
		if s == nil {
			s = &session{id: fmt.Sprintf("pse-%d-%d-%d", ev.UserID, int64(ev.Key), now.Unix())}
			p.sessions[sk] = s
			if len(p.sessions) > 4096 {
				p.sessions = map[sessionKey]*session{sk: s}
			}
		}
		position = ev.Offset
		switch ev.State {
		case "stopped":
			notification = "PlaybackStop"
			delete(p.sessions, sk)
		case "paused":
			paused = true
			if !s.started {
				notification, s.started = "PlaybackStart", true
			} else if !s.paused || now.Sub(s.lastSent) >= progressEvery {
				notification = "PlaybackProgress"
			}
		default:
			if !s.started {
				notification, s.started = "PlaybackStart", true
			} else if s.paused || now.Sub(s.lastSent) >= progressEvery {
				notification = "PlaybackProgress"
			}
		}
		s.paused = paused
		if notification != "" {
			s.lastSent = now
		}
	case state.EventScrobble:
		notification = "MarkPlayed"
		delete(p.sessions, sk)
	case state.EventUnscrobble:
		notification = "UserDataSaved"
		delete(p.sessions, sk)
	}
	sid := fmt.Sprintf("pse-%d-%d", ev.UserID, int64(ev.Key))
	if s != nil {
		sid = s.id
	}
	p.mu.Unlock()
	if notification == "" {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	item := Item{}
	if p.Lookup != nil {
		item, _ = p.Lookup(ctx, ev.Key)
	}
	runtime := item.RuntimeSecs * 10_000_000
	if ev.Duration > 0 {
		runtime = ev.Duration.Nanoseconds() / 100
	}
	ticks := position.Nanoseconds() / 100
	if ev.Kind == state.EventScrobble {
		ticks = runtime
	}
	it := map[string]any{
		"Id":           "pse-" + strconv.FormatInt(int64(ev.Key), 10),
		"RunTimeTicks": runtime,
	}
	if item.Name != "" {
		it["Name"] = item.Name
	}
	if item.Year > 0 {
		it["ProductionYear"] = item.Year
	}
	tmdb := strconv.FormatInt(ev.Key.TMDB(), 10)
	if kind == core.KindMovie {
		it["Type"] = "Movie"
		it["ProviderIds"] = map[string]string{"Tmdb": tmdb}
	} else {
		it["Type"] = "Episode"
		it["ProviderIds"] = map[string]string{}
		it["SeriesProviderIds"] = map[string]string{"Tmdb": tmdb}
		it["ParentIndexNumber"] = ev.Key.Season()
		it["IndexNumber"] = ev.Key.Episode()
	}
	user := ""
	if p.Users != nil {
		user = p.Users(ctx, ev.UserID)
	}
	payload := map[string]any{
		"NotificationType": notification,
		"Item":             it,
		"Session": map[string]any{
			"Id":       sid,
			"UserName": user,
			"PlayState": map[string]any{
				"PositionTicks": ticks,
				"IsPaused":      paused,
			},
		},
	}
	if notification == "UserDataSaved" {
		// Format « à plat » du plugin Webhook : seul porteur de SaveReason.
		payload = map[string]any{
			"NotificationType": "UserDataSaved", "ItemType": it["Type"], "ItemId": it["Id"],
			"Name": item.Name, "SaveReason": "TogglePlayed", "Played": false, "UserName": user,
		}
		if kind == core.KindMovie {
			payload["Provider_tmdb"] = tmdb
		} else {
			payload["SeasonNumber"] = ev.Key.Season()
			payload["EpisodeNumber"] = ev.Key.Episode()
			if item.Name != "" {
				payload["SeriesName"] = item.Name
			}
		}
	}
	select {
	case p.queue <- job{viewer: ev.UserID, payload: payload}:
	default:
		p.Log.Warn("scrob: file d'envoi pleine, événement ignoré")
	}
}

func (p *Pusher) send(ctx context.Context, j job) {
	target, err := p.Links.get(ctx, j.viewer)
	if err != nil || target == "" {
		return
	}
	body, _ := json.Marshal(j.payload)
	status := "ok"
	if err := p.post(ctx, target, body); err != nil {
		status = err.Error()
		p.Log.Warn("scrob: envoi refusé", "viewer", j.viewer, "err", err)
	}
	p.Links.report(j.viewer, status, p.Now())
}

func (p *Pusher) post(ctx context.Context, target string, body []byte) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return errors.New("lien invalide")
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := p.HTTP.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			return fmt.Errorf("Scrob injoignable : %v", ue.Err)
		}
		return errors.New("Scrob injoignable")
	}
	defer res.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 64<<10))
	switch {
	case res.StatusCode == http.StatusUnauthorized:
		return errors.New("clé API Scrob refusée")
	case res.StatusCode == http.StatusNotFound:
		return errors.New("connexion Scrob introuvable (vérifiez le lien)")
	case res.StatusCode >= 300:
		return fmt.Errorf("Scrob a répondu %d", res.StatusCode)
	}
	return nil
}

// Test envoie un webhook neutre (ignoré par Scrob) pour vérifier le lien et
// la clé API.
func (p *Pusher) Test(ctx context.Context, target string) error {
	p.init()
	if _, err := ValidateURL(target); err != nil {
		return err
	}
	return p.post(ctx, target, []byte(`{"NotificationType":"PSETest","ItemType":"Other"}`))
}
