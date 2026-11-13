package torrentclient

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// FileServer sert les fichiers téléchargés à Ether sur l'interface locale
// seulement (127.0.0.1, port choisi par le système), par jetons aléatoires
// à durée limitée : Ether lit un fichier du client comme un lien debrid,
// avec ses requêtes partielles, sans que le dossier soit exposé.
type FileServer struct {
	TTL time.Duration // validité d'un lien (12 h par défaut)
	Now func() time.Time

	mu     sync.Mutex
	ln     net.Listener
	srv    *http.Server
	tokens map[string]served
	closed bool
}

type served struct {
	path    string
	until   time.Time
	modTime time.Time
}

const maxServed = 4096

func (f *FileServer) now() time.Time {
	if f.Now != nil {
		return f.Now()
	}
	return time.Now()
}

func (f *FileServer) ttl() time.Duration {
	if f.TTL > 0 {
		return f.TTL
	}
	return 12 * time.Hour
}

// URL publie le fichier local et renvoie son lien et son expiration.
func (f *FileServer) URL(path string) (string, time.Time, error) {
	st, err := os.Stat(path)
	if err != nil || !st.Mode().IsRegular() {
		return "", time.Time{}, errors.New("torrentclient: fichier introuvable dans le dossier partagé")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return "", time.Time{}, errors.New("torrentclient: serveur de fichiers arrêté")
	}
	if f.ln == nil {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return "", time.Time{}, err
		}
		f.ln = ln
		f.srv = &http.Server{Handler: http.HandlerFunc(f.serve), ReadHeaderTimeout: 10 * time.Second}
		go func() { _ = f.srv.Serve(ln) }()
	}
	now := f.now()
	if f.tokens == nil {
		f.tokens = map[string]served{}
	}
	for k, v := range f.tokens {
		if now.After(v.until) {
			delete(f.tokens, k)
		}
	}
	if len(f.tokens) >= maxServed {
		f.tokens = map[string]served{}
	}
	var b [24]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", time.Time{}, err
	}
	tok := hex.EncodeToString(b[:])
	until := now.Add(f.ttl())
	f.tokens[tok] = served{path: path, until: until, modTime: st.ModTime()}
	return "http://" + f.ln.Addr().String() + "/f/" + tok, until, nil
}

func (f *FileServer) serve(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	tok, ok := strings.CutPrefix(r.URL.Path, "/f/")
	if !ok {
		http.NotFound(w, r)
		return
	}
	f.mu.Lock()
	s, ok := f.tokens[tok]
	f.mu.Unlock()
	if !ok || f.now().After(s.until) {
		http.Error(w, "gone", http.StatusGone)
		return
	}
	file, err := os.Open(s.path)
	if err != nil {
		http.Error(w, "gone", http.StatusGone)
		return
	}
	defer file.Close()
	w.Header().Set("Content-Type", "application/octet-stream")
	http.ServeContent(w, r, "", s.modTime, file)
}

// Close arrête le serveur.
func (f *FileServer) Close() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
	if f.srv != nil {
		_ = f.srv.Close()
	}
	f.tokens = nil
}
