package xdcc

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeXG imite les deux API de XDCC Grabscher utilisées par Remix (REST
// /api/1.0 et compatibilité SABnzbd) et écrit ses fichiers comme lui :
// partiel dans tmp/<nom court>.<taille>, puis déplacé dans dl/<catégorie>/.
// Aucun réseau IRC : les octets « reçus du bot » sont ajoutés par le test.
type fakeXG struct {
	t    *testing.T
	key  string
	root string // contient dl/ et tmp/

	mu      sync.Mutex
	packets map[string]*fakePacket
	bots    map[string]*Bot
	jobs    map[string]*fakeJob
	urls    []string
	bodies  []string
	addurls int
	deleted []string // tâches retirées (file ou historique)
	srv     *httptest.Server
}

type fakePacket struct {
	Packet
	content []byte
	got     int
}

type fakeJob struct {
	id, packet, cat, status, fail, storage string
	done                                   time.Time
}

func newFakeXG(t *testing.T) *fakeXG {
	t.Helper()
	f := &fakeXG{t: t, key: "test-key-0000", root: t.TempDir(), packets: map[string]*fakePacket{}, bots: map[string]*Bot{}, jobs: map[string]*fakeJob{}}
	for _, d := range []string{"dl", "tmp"} {
		if err := os.MkdirAll(filepath.Join(f.root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func guid(n int) string { return fmt.Sprintf("00000000-0000-4000-8000-%012d", n) }

func (f *fakeXG) addBot(n int, name string, online bool) string {
	g := guid(1000 + n)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.bots[g] = &Bot{GUID: g, Name: name, Connected: online}
	return g
}

func (f *fakeXG) addPacket(n int, bot, name string, content []byte) string {
	g := guid(n)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.packets[g] = &fakePacket{Packet: Packet{GUID: g, BotGUID: bot, ID: n, Name: name, Size: int64(len(content)), LastUpdated: time.Now()}, content: content}
	return g
}

// tmpPath : Grabscher nomme le fichier partiel avec la taille exacte de
// l'offre DCC, pas celle, arrondie, de l'annonce du bot.
func (f *fakeXG) tmpPath(p *fakePacket) string {
	return filepath.Join(f.root, "tmp", TmpName(p.Name, int64(len(p.content))))
}

// announce remplace la taille annoncée par un arrondi, comme un vrai bot.
func (f *fakeXG) announce(packet string, size int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.packets[packet].Size = size
}

// receive ajoute n octets reçus du bot au fichier partiel du paquet.
func (f *fakeXG) receive(packet string, n int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p := f.packets[packet]
	end := min(p.got+n, len(p.content))
	fh, err := os.OpenFile(f.tmpPath(p), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		f.t.Fatal(err)
	}
	_, _ = fh.Write(p.content[p.got:end])
	_ = fh.Close()
	p.got = end
	p.CurrentSize, p.Connected, p.Speed = int64(end), true, 4<<20
	p.Size = int64(len(p.content)) // taille exacte connue dès l'offre DCC
	for _, j := range f.jobs {
		if j.packet == packet && j.status == "Queued" {
			j.status = "Downloading"
		}
	}
}

// complete termine le transfert : déplacement dans dl/<catégorie>/.
func (f *fakeXG) complete(packet string) string {
	f.receive(packet, 1<<40)
	f.mu.Lock()
	defer f.mu.Unlock()
	p := f.packets[packet]
	for _, j := range f.jobs {
		if j.packet != packet || j.status == "Completed" {
			continue
		}
		dir := filepath.Join(f.root, "dl", j.cat)
		_ = os.MkdirAll(dir, 0o755)
		dst := filepath.Join(dir, p.Name)
		if err := os.Rename(f.tmpPath(p), dst); err != nil {
			f.t.Fatal(err)
		}
		j.status, j.storage, j.done = "Completed", dst, time.Now()
		p.Connected, p.Enabled = false, false
		return dst
	}
	f.t.Fatalf("aucune tâche pour %s", packet)
	return ""
}

func (f *fakeXG) fail(packet, reason string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, j := range f.jobs {
		if j.packet == packet {
			j.status, j.fail = "Failed", reason
		}
	}
}

func (f *fakeXG) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.urls = append(f.urls, r.URL.String())
	if strings.HasPrefix(r.URL.Path, "/api/1.0/") {
		f.rest(w, r)
		return
	}
	if r.URL.Path == "/sabnzbd/api" && r.Method == http.MethodPost {
		_ = r.ParseForm()
		f.bodies = append(f.bodies, r.PostForm.Encode())
		f.sab(w, r)
		return
	}
	http.NotFound(w, r)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func (f *fakeXG) rest(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != f.key {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	p := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/1.0/"), ".json")
	switch {
	case p == "packets":
		term := strings.ToLower(r.URL.Query().Get("searchTerm"))
		offline := r.URL.Query().Get("showOfflineBots") == "true"
		var res []Packet
		for _, pk := range f.packets {
			b := f.bots[pk.BotGUID]
			if b == nil || (!b.Connected && !offline) {
				continue
			}
			name := strings.ToLower(searchText(pk.Name))
			ok := true
			for _, w := range strings.Fields(term) {
				ok = ok && strings.Contains(name, w)
			}
			if ok {
				res = append(res, pk.Packet)
			}
		}
		writeJSON(w, map[string]any{"Results": res, "ResultCount": len(res)})
	case strings.HasPrefix(p, "packets/"):
		pk := f.packets[strings.TrimPrefix(p, "packets/")]
		if pk == nil {
			writeJSON(w, map[string]any{"ReturnValue": 0, "Message": "object not found"})
			return
		}
		writeJSON(w, pk.Packet)
	case strings.HasPrefix(p, "bots/"):
		b := f.bots[strings.TrimPrefix(p, "bots/")]
		if b == nil {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		writeJSON(w, b)
	case p == "servers":
		writeJSON(w, map[string]any{"Results": []Node{{GUID: guid(9000), Name: "irc.lab", Connected: true}}, "ResultCount": 1})
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func (f *fakeXG) sab(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("apikey") != "" {
		f.t.Errorf("clé d'API dans l'adresse : %s", r.URL)
	}
	if r.PostForm.Get("apikey") != f.key {
		writeJSON(w, map[string]any{"status": false, "error": "API Key Incorrect"})
		return
	}
	switch mode := r.PostForm.Get("mode"); mode {
	case "version":
		writeJSON(w, map[string]any{"version": "3.3.3"})
	case "get_config":
		writeJSON(w, map[string]any{"config": map[string]any{"misc": map[string]any{"complete_dir": filepath.Join(f.root, "dl") + "/"}}})
	case "addurl":
		link := r.PostForm.Get("name")
		i := strings.Index(link, "id=")
		packet := link[i+3:]
		pk := f.packets[packet]
		if pk == nil {
			writeJSON(w, map[string]any{"status": false, "error": "Unknown packet"})
			return
		}
		for _, j := range f.jobs {
			if j.packet == packet && (j.status == "Queued" || j.status == "Downloading") {
				writeJSON(w, map[string]any{"status": true, "nzo_ids": []string{j.id}})
				return
			}
		}
		f.addurls++
		id := fmt.Sprintf("XG_%d", f.addurls)
		f.jobs[id] = &fakeJob{id: id, packet: packet, cat: r.PostForm.Get("cat"), status: "Queued"}
		pk.Enabled = true
		writeJSON(w, map[string]any{"status": true, "nzo_ids": []string{id}})
	case "queue":
		if r.PostForm.Get("name") == "delete" {
			id := r.PostForm.Get("value")
			if j := f.jobs[id]; j != nil {
				_ = os.Remove(f.tmpPath(f.packets[j.packet]))
				delete(f.jobs, id)
			}
			f.deleted = append(f.deleted, id)
			writeJSON(w, map[string]any{"status": true})
			return
		}
		var slots []map[string]any
		for _, j := range f.jobs {
			if j.status == "Queued" || j.status == "Downloading" {
				slots = append(slots, map[string]any{"nzo_id": j.id, "filename": f.packets[j.packet].Name, "cat": j.cat, "status": j.status, "mb": "1", "mbleft": "1"})
			}
		}
		writeJSON(w, map[string]any{"queue": map[string]any{"status": "Idle", "slots": slots}})
	case "history":
		if r.PostForm.Get("name") == "delete" {
			id := r.PostForm.Get("value")
			if j := f.jobs[id]; j != nil {
				if r.PostForm.Get("del_files") == "1" && j.storage != "" {
					_ = os.Remove(j.storage)
				}
				delete(f.jobs, id)
			}
			f.deleted = append(f.deleted, id)
			writeJSON(w, map[string]any{"status": true})
			return
		}
		var slots []map[string]any
		for _, j := range f.jobs {
			if j.status == "Completed" || j.status == "Failed" {
				slots = append(slots, map[string]any{"nzo_id": j.id, "name": f.packets[j.packet].Name, "category": j.cat, "bytes": len(f.packets[j.packet].content),
					"status": j.status, "fail_message": j.fail, "storage": j.storage, "completed": j.done.Unix()})
			}
		}
		writeJSON(w, map[string]any{"history": map[string]any{"slots": slots}})
	default:
		writeJSON(w, map[string]any{"status": false, "error": "not implemented: " + mode})
	}
}

func (f *fakeXG) settings(mode string) Settings {
	return Settings{Enabled: true, URL: f.srv.URL, Key: f.key, Mode: mode, MaxActive: 2, MaxPerDay: 20, CacheBytes: 10 << 30,
		Retention: 48 * time.Hour, Buffer: 1 << 20, Wait: 2 * time.Second, MaxResults: 50, Daily: 1000}
}

func (f *fakeXG) service(mode string) *Service {
	s := f.settings(mode)
	return &Service{Settings: s, Client: &Client{Base: s.URL, Key: s.Key}, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Usage: func(string) (int64, int64, error) { return 1 << 40, 1 << 40, nil }}
}

func (f *fakeXG) addurlCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.addurls
}
