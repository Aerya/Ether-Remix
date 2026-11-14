package ftp

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"
)

// streamServer sert les fichiers à Ether sur 127.0.0.1 seulement, par
// jetons aléatoires. Chaque fichier lu a son propre port : le moteur
// d'Ether limite les connexions par adresse, deux spectateurs ne se
// partagent donc pas les mêmes.
type streamServer struct {
	svc *Service

	mu     sync.Mutex
	files  map[string]*fileServer // id → serveur
	tokens map[string]served
	closed bool
}

type fileServer struct {
	id     string
	ln     net.Listener
	srv    *http.Server
	used   time.Time
	active int
}

type served struct {
	id    string
	until time.Time
}

const (
	tokenTTL     = 12 * time.Hour
	maxTokens    = 4096
	maxListeners = 64
	listenerIdle = 30 * time.Minute
	// headerWait : au-delà, les en-têtes partent avant le premier octet du
	// serveur distant (le moteur d'Ether double une requête lente à
	// répondre au bout de 400 ms).
	headerWait = 250 * time.Millisecond
	copyBuffer = 1 << 20
)

// URL publie un fichier et renvoie son lien et l'origine de confiance.
func (st *streamServer) URL(id string) (string, string, error) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.closed {
		return "", "", errors.New("serveur arrêté")
	}
	if st.files == nil {
		st.files, st.tokens = map[string]*fileServer{}, map[string]served{}
	}
	now := time.Now()
	fsrv := st.files[id]
	if fsrv == nil {
		st.evictLocked(now)
		if len(st.files) >= maxListeners {
			return "", "", errors.New("trop de lectures FTP simultanées")
		}
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return "", "", err
		}
		fsrv = &fileServer{id: id, ln: ln}
		fsrv.srv = &http.Server{Handler: st.handler(fsrv), ReadHeaderTimeout: 10 * time.Second}
		st.files[id] = fsrv
		go func() { _ = fsrv.srv.Serve(ln) }()
	}
	fsrv.used = now
	for k, v := range st.tokens {
		if now.After(v.until) {
			delete(st.tokens, k)
		}
	}
	if len(st.tokens) >= maxTokens {
		st.tokens = map[string]served{}
	}
	var b [24]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", "", err
	}
	tok := hex.EncodeToString(b[:])
	st.tokens[tok] = served{id: id, until: now.Add(tokenTTL)}
	origin := "http://" + fsrv.ln.Addr().String()
	return origin + "/f/" + tok, origin, nil
}

// evictLocked ferme les serveurs inactifs (et le plus ancien au-delà du
// plafond).
func (st *streamServer) evictLocked(now time.Time) {
	var oldest *fileServer
	for id, f := range st.files {
		if f.active == 0 && now.Sub(f.used) > listenerIdle {
			_ = f.srv.Close()
			delete(st.files, id)
			continue
		}
		if f.active == 0 && (oldest == nil || f.used.Before(oldest.used)) {
			oldest = f
		}
	}
	if len(st.files) >= maxListeners && oldest != nil {
		_ = oldest.srv.Close()
		delete(st.files, oldest.id)
	}
}

// Close arrête le serveur.
func (st *streamServer) Close() {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.closed = true
	for _, f := range st.files {
		_ = f.srv.Close()
	}
	st.files, st.tokens = nil, nil
}

func (st *streamServer) handler(fsrv *fileServer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		tok, ok := strings.CutPrefix(r.URL.Path, "/f/")
		st.mu.Lock()
		s, found := st.tokens[tok]
		if ok && found && s.id == fsrv.id && !time.Now().After(s.until) {
			fsrv.active++
			fsrv.used = time.Now()
		}
		st.mu.Unlock()
		if !ok || !found || s.id != fsrv.id || time.Now().After(s.until) {
			http.Error(w, "gone", http.StatusGone)
			return
		}
		defer func() {
			st.mu.Lock()
			fsrv.active--
			fsrv.used = time.Now()
			st.mu.Unlock()
		}()
		st.svc.serve(w, r, fsrv.id)
	}
}

// serve répond à une requête du moteur d'Ether : la copie locale si elle
// est complète, sinon le fichier distant.
func (s *Service) serve(w http.ResponseWriter, r *http.Request, id string) {
	f, ok := s.lookup(id)
	if !ok {
		http.Error(w, "gone", http.StatusGone)
		return
	}
	s.reading(id, +1)
	defer s.reading(id, -1)
	s.touch(id)
	if local, ok := s.localCopy(id); ok {
		fh, err := os.Open(local)
		if err == nil {
			defer fh.Close()
			s.reading(id, +1)
			defer s.reading(id, -1)
			w.Header().Set("Content-Type", contentType(f.Name))
			http.ServeContent(w, r, "", f.MTime, fh)
			return
		}
	}
	s.serveRemote(w, r, f)
}

func (s *Service) serveRemote(w http.ResponseWriter, r *http.Request, f File) {
	start, end, partial, ok := parseRange(r.Header.Get("Range"), f.Size)
	h := w.Header()
	if !ok {
		h.Set("Content-Range", "bytes */"+strconv.FormatInt(f.Size, 10))
		http.Error(w, "range not satisfiable", http.StatusRequestedRangeNotSatisfiable)
		return
	}
	writeHeaders := func() {
		h.Set("Accept-Ranges", "bytes")
		h.Set("Content-Type", contentType(f.Name))
		h.Set("Content-Length", strconv.FormatInt(end-start+1, 10))
		if partial {
			h.Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, f.Size))
			w.WriteHeader(http.StatusPartialContent)
		} else {
			w.WriteHeader(http.StatusOK)
		}
	}
	if r.Method == http.MethodHead {
		writeHeaders()
		return
	}
	type opened struct {
		rd  io.ReadCloser
		err error
	}
	ch := make(chan opened, 1)
	ctx := r.Context()
	go func() {
		rd, err := s.remote.Open(ctx, f.Path, start)
		ch <- opened{rd, err}
	}()
	var res opened
	sent := false
	select {
	case res = <-ch:
	case <-time.After(headerWait):
		writeHeaders()
		sent = true
		if fl, ok := w.(http.Flusher); ok {
			fl.Flush()
		}
		res = <-ch
	}
	if res.err != nil {
		s.log().Debug("ouverture FTP impossible", "début", start, "err", scrub(res.err, s.Settings))
		s.noteErr(res.err)
		if errors.Is(res.err, ErrNotFound) {
			s.forget(f.ID)
		}
		if sent {
			panic(http.ErrAbortHandler) // réponse coupée : le moteur réessaie
		}
		// 503 sans Retry-After : le moteur d'Ether réessaie ce bloc sans
		// mettre toute la source en pause.
		http.Error(w, "remote server unavailable", http.StatusServiceUnavailable)
		return
	}
	defer res.rd.Close()
	if !sent {
		writeHeaders()
	}
	s.reading(f.ID, +1)
	defer s.reading(f.ID, -1)
	want := end - start + 1
	buf := make([]byte, min(int64(copyBuffer), want))
	var done int64
	for done < want {
		n, err := res.rd.Read(buf[:min(int64(len(buf)), want-done)])
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return // le lecteur est parti (saut, arrêt)
			}
			done += int64(n)
		}
		if err != nil {
			if done < want {
				s.log().Debug("lecture FTP coupée", "début", start, "lus", done, "err", scrub(err, s.Settings))
				s.noteErr(err)
				panic(http.ErrAbortHandler)
			}
			return
		}
	}
}

// parseRange lit un en-tête Range à une seule plage.
func parseRange(v string, size int64) (start, end int64, partial, ok bool) {
	if v == "" {
		return 0, size - 1, false, size > 0
	}
	spec, found := strings.CutPrefix(v, "bytes=")
	if !found || strings.Contains(spec, ",") {
		return 0, 0, false, false
	}
	a, b, found := strings.Cut(strings.TrimSpace(spec), "-")
	if !found {
		return 0, 0, false, false
	}
	switch {
	case a == "":
		n, err := strconv.ParseInt(b, 10, 64)
		if err != nil || n <= 0 {
			return 0, 0, false, false
		}
		start, end = max(size-n, 0), size-1
	default:
		var err error
		start, err = strconv.ParseInt(a, 10, 64)
		if err != nil || start < 0 || start >= size {
			return 0, 0, false, false
		}
		end = size - 1
		if b != "" {
			e, err := strconv.ParseInt(b, 10, 64)
			if err != nil || e < start {
				return 0, 0, false, false
			}
			end = min(e, size-1)
		}
	}
	return start, end, true, true
}

// contentType du fichier d'après son extension.
func contentType(name string) string {
	switch strings.ToLower(path.Ext(name)) {
	case ".mkv":
		return "video/x-matroska"
	case ".mp4", ".m4v":
		return "video/mp4"
	case ".webm":
		return "video/webm"
	case ".ts", ".m2ts":
		return "video/mp2t"
	case ".avi":
		return "video/x-msvideo"
	case ".mov":
		return "video/quicktime"
	}
	return "application/octet-stream"
}
