package xdcc

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"
)

// TaskView est une tâche telle que la console l'affiche. State reprend les
// états lisibles : waiting (en attente du bot), offline (bot hors ligne),
// downloading (en cours), playable (prêt à lire en progressif), ready
// (complet), failed (échec), cancelled, removed.
type TaskView struct {
	Packet   string `json:"packet"`
	Name     string `json:"name"`
	Size     int64  `json:"size"`
	Bot      string `json:"bot"`
	State    string `json:"state"`
	Current  int64  `json:"current"`
	Speed    int64  `json:"speed"`
	ETA      int64  `json:"eta,omitempty"` // secondes
	Queue    int    `json:"queue,omitempty"`
	Reason   string `json:"reason,omitempty"`
	Keep     bool   `json:"keep"`
	Manual   bool   `json:"manual,omitempty"`
	Reading  bool   `json:"reading,omitempty"`
	Created  string `json:"created"`
	Done     string `json:"done,omitempty"`
	LastRead string `json:"lastRead,omitempty"`
}

// Status est l'état de la source pour la console.
type Status struct {
	Mode        string     `json:"mode"`
	Progressive bool       `json:"progressive"`
	Reachable   bool       `json:"reachable"`
	Error       string     `json:"error,omitempty"`
	ErrorAt     string     `json:"errorAt,omitempty"`
	CompleteDir string     `json:"completeDir,omitempty"`
	ReadyLocal  string     `json:"readyLocal,omitempty"`
	ReadyOK     bool       `json:"readyVisible"`
	TempLocal   string     `json:"tempLocal,omitempty"`
	TempOK      bool       `json:"tempVisible"`
	Active      int        `json:"active"`
	MaxActive   int        `json:"maxActive"`
	Today       int        `json:"today"`
	MaxPerDay   int        `json:"maxPerDay"`
	CacheUsed   int64      `json:"cacheUsed"`
	CacheMax    int64      `json:"cacheMax"`
	Tasks       []TaskView `json:"tasks"`
}

const statusTasks = 60

// Status lit l'état courant ; les dossiers sont vérifiés depuis Remix.
func (s *Service) Status(ctx context.Context) Status {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	err := s.refresh(ctx, false)
	out := Status{Mode: s.Settings.Mode, Progressive: s.Settings.Progressive(), Reachable: err == nil,
		MaxActive: s.Settings.MaxActive, MaxPerDay: s.Settings.MaxPerDay, CacheMax: s.Settings.CacheBytes}
	if d, err := s.completeDir(ctx); err == nil {
		out.CompleteDir = d
		if local, ok := s.Settings.Local(d); ok {
			out.ReadyLocal, out.ReadyOK = local, isDir(local)
		}
	}
	if d, ok := s.tempDir(ctx); ok {
		out.TempLocal, out.TempOK = d, isDir(d)
	}
	out.CacheUsed = s.cacheUsed()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.init()
	if s.lastErr != "" && s.now().Sub(s.lastErrT) < 30*time.Minute {
		out.Error, out.ErrorAt = clip(s.lastErr, 300), s.lastErrT.UTC().Format(time.RFC3339)
	}
	day := s.now().Format("2006-01-02")
	list := make([]*Task, 0, len(s.tasks))
	for _, t := range s.tasks {
		if t.active() {
			out.Active++
		}
		if t.Created.Format("2006-01-02") == day {
			out.Today++
		}
		list = append(list, t)
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].active() != list[j].active() {
			return list[i].active()
		}
		return list[i].Created.After(list[j].Created)
	})
	for i, t := range list {
		if i >= statusTasks {
			break
		}
		out.Tasks = append(out.Tasks, s.view(t))
	}
	return out
}

// view décrit une tâche (verrou tenu).
func (s *Service) view(t *Task) TaskView {
	v := TaskView{Packet: t.Packet, Name: t.Name, Size: t.Size, Bot: t.Bot, Keep: t.Keep, Manual: t.Manual,
		Reading: s.readers[t.Packet] > 0, Created: t.Created.UTC().Format(time.RFC3339)}
	if !t.Done.IsZero() {
		v.Done = t.Done.UTC().Format(time.RFC3339)
	}
	if !t.LastRead.IsZero() {
		v.LastRead = t.LastRead.UTC().Format(time.RFC3339)
	}
	l := s.lives[t.Packet]
	switch t.State {
	case StateCompleted:
		v.State, v.Current = "ready", t.Size
	case StateFailed:
		v.State, v.Reason = "failed", clip(t.Fail, 200)
	case StateCancelled, StateRemoved:
		v.State = t.State
	default:
		v.State = "waiting"
		if l != nil {
			v.Current, v.Speed, v.Queue = l.current, l.speed, l.queuePos
			if l.eta > 0 {
				v.ETA = int64(l.eta / time.Second)
			}
			switch {
			case !l.botOnline && l.current == 0:
				v.State = "offline"
			case l.current > 0 || l.transfer:
				v.State = "downloading"
				if s.Settings.Progressive() && l.current >= min(s.Settings.Buffer, t.Size) {
					v.State = "playable"
				}
			}
			if v.State == "waiting" && l.botMsg != "" {
				v.Reason = clip(l.botMsg, 160)
			}
		}
	}
	return v
}

func isDir(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

// Test vérifie la configuration : Grabscher joignable, clé acceptée par ses
// deux API, réseaux IRC connus, dossiers visibles depuis Remix.
func Test(ctx context.Context, s Settings, c *Client) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	v, err := c.Version(ctx)
	if err != nil {
		return "", err
	}
	servers, err := c.Servers(ctx)
	if err != nil {
		return "", err
	}
	online := 0
	for _, n := range servers {
		if n.Connected {
			online++
		}
	}
	dir, err := c.CompleteDir(ctx)
	if err != nil {
		return "", err
	}
	var notes []string
	notes = append(notes, fmt.Sprintf("Grabscher %s joignable, clé acceptée", strings.TrimSpace(v)))
	switch {
	case len(servers) == 0:
		notes = append(notes, "aucun réseau IRC configuré dans Grabscher")
	default:
		notes = append(notes, fmt.Sprintf("%d/%d réseau(x) IRC connecté(s)", online, len(servers)))
	}
	local, ok := s.Local(dir)
	if !ok || !isDir(local) {
		return strings.Join(notes, " ; "), errors.New("Grabscher répond, mais Remix ne voit pas son dossier de téléchargement « " + clip(dir, 120) +
			" » : partagez ce volume avec Remix (même chemin, ou correspondance des dossiers)")
	}
	notes = append(notes, "dossier de téléchargement visible")
	if s.Progressive() {
		tmp := s.TempDir
		if tmp == "" {
			tmp, _ = s.Local(parentTmp(dir))
		}
		if tmp == "" || !isDir(tmp) {
			notes = append(notes, "dossier temporaire invisible : lecture progressive impossible, lecture une fois le fichier complet")
		} else {
			notes = append(notes, "dossier temporaire visible : lecture progressive possible")
		}
	}
	return strings.Join(notes, " ; "), nil
}
