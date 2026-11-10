package music

import (
	"crypto/sha256"
	"fmt"
	"net"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"
)

// MusicSession est une observation du client, pas une session vidéo contrôlable.
// Les URL et jetons de lecture ne sont jamais conservés.
type MusicSession struct {
	ID          string    `json:"id"`
	ViewerID    int64     `json:"-"`
	Username    string    `json:"username"`
	Title       string    `json:"title"`
	Artist      string    `json:"artist"`
	Album       string    `json:"album"`
	IsMusic     bool      `json:"isMusic"`
	CanStop     bool      `json:"canStop"`
	ViewerState string    `json:"viewerState"`
	StartedAt   time.Time `json:"startedAt"`
	ViewOffset  *int64    `json:"viewOffset,omitempty"`
	Info        struct {
		Duration time.Duration `json:"duration"`
	} `json:"info"`
	Client struct {
		Protocol string `json:"protocol"`
		Product  string `json:"product"`
		Device   string `json:"deviceName"`
	} `json:"client"`
	sequence uint64
	track    int64
	expires  time.Time
}

var musicAuthField = regexp.MustCompile(`(?i)(?:^|[, ])(DeviceId|Device|Client)="([^"\r\n]*)"`)

func musicClient(r *http.Request, protocol string) (id, product, device string) {
	get := func(k string) string {
		if v := r.Header.Get(k); v != "" {
			return v
		}
		return r.URL.Query().Get(k)
	}
	switch protocol {
	case "plex":
		id, product, device = get("X-Plex-Client-Identifier"), get("X-Plex-Product"), get("X-Plex-Device-Name")
	case "subsonic":
		id, product = r.Form.Get("c"), r.Form.Get("c")
	default:
		id, product, device = get("X-Emby-Device-Id"), get("X-Emby-Client"), get("X-Emby-Device-Name")
		for _, header := range []string{"Authorization", "X-Emby-Authorization"} {
			for _, m := range musicAuthField.FindAllStringSubmatch(r.Header.Get(header), -1) {
				switch strings.ToLower(m[1]) {
				case "deviceid":
					if id == "" {
						id = m[2]
					}
				case "client":
					if product == "" {
						product = m[2]
					}
				case "device":
					if device == "" {
						device = m[2]
					}
				}
			}
		}
	}
	if id == "" || protocol == "subsonic" {
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			host = r.RemoteAddr
		}
		id += ":" + host
	}
	return
}

// observeMusic ne dépend pas du téléchargement : un préchargement n'est pas
// une écoute. Un arrêt tardif d'un ancien titre ne ferme pas le suivant.
func (s *Server) observeMusic(r *http.Request, viewer int64, protocol string, track int64, state string, offset *int64, username string) {
	if viewer <= 0 || track <= 0 {
		return
	}
	if state != "playing" && state != "paused" && state != "stopped" {
		return
	}
	client, product, device := musicClient(r, protocol)
	id := fmt.Sprintf("music-%x", sha256.Sum256([]byte(fmt.Sprintf("%d/%s/%s", viewer, protocol, client))))
	now := s.clock()
	s.nowMu.Lock()
	if state == "stopped" {
		if old, ok := s.nowPlaying[id]; ok && old.track == track {
			delete(s.nowPlaying, id)
		}
		s.nowMu.Unlock()
		return
	}
	old, exists := s.nowPlaying[id]
	if s.nowPlaying == nil {
		s.nowPlaying = map[string]MusicSession{}
	}
	for k, v := range s.nowPlaying {
		if !now.Before(v.expires) {
			delete(s.nowPlaying, k)
		}
	}
	if _, present := s.nowPlaying[id]; !present && len(s.nowPlaying) >= 1024 {
		var oldest string
		var at time.Time
		for k, v := range s.nowPlaying {
			if oldest == "" || v.expires.Before(at) {
				oldest, at = k, v.expires
			}
		}
		delete(s.nowPlaying, oldest)
	}
	s.nowSequence++
	sequence := s.nowSequence
	pending := old
	if !exists || old.track != track {
		pending = MusicSession{}
	}
	pending.track, pending.sequence, pending.expires = track, sequence, now.Add(2*time.Minute)
	s.nowPlaying[id] = pending
	s.nowMu.Unlock()
	t, err := s.TrackFor(r.Context(), viewer, track)
	if err != nil {
		s.nowMu.Lock()
		if current, ok := s.nowPlaying[id]; ok && current.sequence == sequence {
			delete(s.nowPlaying, id)
		}
		s.nowMu.Unlock()
		return
	}
	entry := MusicSession{ID: id, ViewerID: viewer, Username: username, Title: t.Title, Artist: t.Artist.Name, Album: t.Album.Title, IsMusic: true, ViewerState: state, StartedAt: now, track: track}
	if exists && old.track == track && now.Before(old.expires) {
		entry.StartedAt = old.StartedAt
	}
	entry.Info.Duration = time.Duration(t.Duration) * time.Second
	entry.Client.Protocol, entry.Client.Product, entry.Client.Device = protocol, product, device
	if offset != nil {
		pos := max(int64(0), *offset)
		if t.Duration > 0 {
			pos = min(pos, int64(t.Duration)*1000)
		}
		entry.ViewOffset = &pos
	}
	ttl := 2 * time.Minute
	// Subsonic ne fournit normalement qu'un signal au début du morceau.
	if protocol == "subsonic" {
		ttl = 5 * time.Minute
		if t.Duration > 0 {
			ttl = min(24*time.Hour, time.Duration(t.Duration)*time.Second+30*time.Second)
		}
	}
	entry.expires = now.Add(ttl)
	s.nowMu.Lock()
	defer s.nowMu.Unlock()
	// Un arrêt ou un autre morceau peut arriver pendant la lecture des
	// métadonnées. Sa décision doit rester prioritaire.
	if current, ok := s.nowPlaying[id]; !ok || current.sequence != sequence {
		return
	}
	entry.sequence = sequence
	s.nowPlaying[id] = entry
}

func (s *Server) NowPlaying() []MusicSession {
	s.nowMu.Lock()
	defer s.nowMu.Unlock()
	now := s.clock()
	out := []MusicSession{}
	for id, v := range s.nowPlaying {
		if !now.Before(v.expires) {
			delete(s.nowPlaying, id)
			continue
		}
		if v.Title != "" {
			out = append(out, v)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
