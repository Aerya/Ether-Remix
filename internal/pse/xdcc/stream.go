package xdcc

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// streamServer sert les fichiers XDCC à Ether sur 127.0.0.1 seulement, par
// jetons aléatoires (comme le serveur de fichiers du client torrent). Un
// fichier en cours de transfert est servi tel qu'il grandit : chaque
// réponse partielle ne contient que des octets réellement reçus du bot.
type streamServer struct {
	svc *Service

	mu     sync.Mutex
	ln     net.Listener
	srv    *http.Server
	tokens map[string]served
	closed bool
}

type served struct {
	packet string
	until  time.Time
}

const (
	tokenTTL  = 12 * time.Hour
	maxTokens = 4096
	pollEvery = 200 * time.Millisecond
)

// URL publie un paquet et renvoie son lien et l'origine de confiance.
func (st *streamServer) URL(packet string) (string, string, error) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.closed {
		return "", "", errors.New("serveur arrêté")
	}
	if st.ln == nil {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return "", "", err
		}
		st.ln = ln
		st.srv = &http.Server{Handler: http.HandlerFunc(st.serve), ReadHeaderTimeout: 10 * time.Second}
		go func() { _ = st.srv.Serve(ln) }()
	}
	now := time.Now()
	if st.tokens == nil {
		st.tokens = map[string]served{}
	}
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
	st.tokens[tok] = served{packet: packet, until: now.Add(tokenTTL)}
	origin := "http://" + st.ln.Addr().String()
	return origin + "/x/" + tok, origin, nil
}

// Close arrête le serveur.
func (st *streamServer) Close() {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.closed = true
	if st.srv != nil {
		_ = st.srv.Close()
	}
	st.tokens = nil
}

func (st *streamServer) serve(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	tok, ok := strings.CutPrefix(r.URL.Path, "/x/")
	st.mu.Lock()
	s, found := st.tokens[tok]
	st.mu.Unlock()
	if !ok || !found || time.Now().After(s.until) {
		http.Error(w, "gone", http.StatusGone)
		return
	}
	st.svc.serveFile(w, r, s.packet)
}

// growing est un fichier XDCC ouvert pour une réponse : complet, ou partiel
// et qui grandit au fil du transfert.
type growing struct {
	f     *os.File
	size  int64 // taille finale
	done  bool
	speed func() int64
	// alive indique que le transfert peut encore apporter des octets.
	alive func() bool
}

func (g *growing) available() int64 {
	if g.done {
		return g.size
	}
	st, err := g.f.Stat()
	if err != nil {
		return 0
	}
	return min(st.Size(), g.size)
}

// open ouvre le paquet tel qu'il est maintenant.
func (s *Service) open(ctx context.Context, packet string) (*growing, string, error) {
	src, err := s.source(ctx, packet)
	if err != nil {
		return nil, "", err
	}
	if src.local == "" {
		return nil, "", errors.New("fichier pas encore visible")
	}
	if !src.done && !s.Settings.Progressive() {
		return nil, "", errors.New("lecture progressive désactivée")
	}
	f, err := os.Open(src.local)
	if err != nil {
		return nil, "", err
	}
	g := &growing{f: f, size: src.size, done: src.done}
	g.speed = func() int64 {
		s.mu.Lock()
		defer s.mu.Unlock()
		if l := s.lives[packet]; l != nil {
			return l.speed
		}
		return 0
	}
	g.alive = func() bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		t := s.tasks[packet]
		return t != nil && (t.active() || t.State == StateCompleted)
	}
	s.mu.Lock()
	name := ""
	if t := s.tasks[packet]; t != nil {
		name = t.Name
	}
	s.mu.Unlock()
	return g, name, nil
}

// serveFile répond à une requête du moteur d'Ether. Règles :
//   - la plage demandée commence dans la partie reçue : réponse 206 tout
//     de suite, octets envoyés au fil de leur arrivée ;
//   - elle commence au-delà : attente seulement si l'arrivée estimée tient
//     dans le délai maximal, sinon 503 immédiat. Sans Retry-After : le
//     moteur d'Ether mettrait alors toute la source en pause, y compris la
//     lecture des octets déjà reçus ; sans lui, seul ce bloc échoue ;
//   - le bot se tait plus longtemps que le délai maximal : la réponse est
//     coupée, le moteur réessaie depuis l'octet manquant.
func (s *Service) serveFile(w http.ResponseWriter, r *http.Request, packet string) {
	ctx := r.Context()
	s.reading(packet, +1)
	defer s.reading(packet, -1)
	g, name, err := s.open(ctx, packet)
	if err != nil {
		http.Error(w, "not available yet", http.StatusServiceUnavailable)
		return
	}
	defer g.f.Close()
	start, end, partial, ok := parseRange(r.Header.Get("Range"), g.size)
	if !ok {
		w.Header().Set("Content-Range", "bytes */"+strconv.FormatInt(g.size, 10))
		http.Error(w, "range not satisfiable", http.StatusRequestedRangeNotSatisfiable)
		return
	}
	wait := s.Settings.Wait
	if avail := g.available(); start >= avail {
		need := start - avail + 1
		sp := g.speed()
		if sp <= 0 || time.Duration(need/sp)*time.Second > wait || !g.alive() {
			http.Error(w, "not received from the XDCC bot yet", http.StatusServiceUnavailable)
			return
		}
		if !waitFor(ctx, func() bool { return g.available() > start }, wait) {
			http.Error(w, "not received from the XDCC bot yet", http.StatusServiceUnavailable)
			return
		}
	}
	h := w.Header()
	h.Set("Accept-Ranges", "bytes")
	h.Set("Content-Type", contentType(name))
	h.Set("Content-Length", strconv.FormatInt(end-start+1, 10))
	h.Set("Cache-Control", "no-store")
	if partial {
		h.Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, g.size))
		w.WriteHeader(http.StatusPartialContent)
	} else {
		w.WriteHeader(http.StatusOK)
	}
	if r.Method == http.MethodHead {
		return
	}
	fl, _ := w.(http.Flusher)
	buf := make([]byte, 256<<10)
	pos := start
	for pos <= end {
		avail := g.available()
		if pos >= avail {
			if !waitFor(ctx, func() bool { return g.available() > pos }, wait) {
				return // bot muet : le moteur réessaie depuis pos
			}
			continue
		}
		n := min(int64(len(buf)), avail-pos, end+1-pos)
		got, err := g.f.ReadAt(buf[:n], pos)
		if got > 0 {
			if _, werr := w.Write(buf[:got]); werr != nil {
				return
			}
			if fl != nil {
				fl.Flush()
			}
			pos += int64(got)
		}
		if err != nil && !errors.Is(err, io.EOF) {
			return
		}
	}
}

// reading compte les lectures en cours d'un paquet (aucun effacement pendant
// une lecture) et retient la dernière.
func (s *Service) reading(packet string, d int) {
	s.fileMu.Lock()
	defer s.fileMu.Unlock()
	s.mu.Lock()
	s.readers[packet] += d
	if s.readers[packet] <= 0 {
		delete(s.readers, packet)
	}
	t := s.tasks[packet]
	var cp Task
	save := false
	if t != nil && (t.LastRead.IsZero() || time.Since(t.LastRead) > time.Minute) {
		t.LastRead = s.now()
		cp, save = *t, true
	}
	s.mu.Unlock()
	if save {
		go s.save(context.Background(), cp)
	}
}

// waitFor attend cond au plus d, en relisant toutes les 200 ms.
func waitFor(ctx context.Context, cond func() bool, d time.Duration) bool {
	deadline := time.Now().Add(d)
	for {
		if cond() {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(pollEvery):
		}
	}
}

// parseRange lit un en-tête Range à une seule plage. Sans en-tête : tout le
// fichier (200). ok est faux pour une plage hors du fichier.
func parseRange(v string, size int64) (start, end int64, partial, ok bool) {
	if size <= 0 {
		return 0, 0, false, false
	}
	v = strings.TrimSpace(v)
	if v == "" {
		return 0, size - 1, false, true
	}
	spec, found := strings.CutPrefix(v, "bytes=")
	if !found || strings.Contains(spec, ",") {
		return 0, 0, false, false
	}
	a, b, found := strings.Cut(spec, "-")
	if !found {
		return 0, 0, false, false
	}
	a, b = strings.TrimSpace(a), strings.TrimSpace(b)
	switch {
	case a == "" && b != "": // les n derniers octets
		n, err := strconv.ParseInt(b, 10, 64)
		if err != nil || n <= 0 {
			return 0, 0, false, false
		}
		start, end = max(size-n, 0), size-1
	default:
		s, err := strconv.ParseInt(a, 10, 64)
		if err != nil || s < 0 || s >= size {
			return 0, 0, false, false
		}
		start, end = s, size-1
		if b != "" {
			e, err := strconv.ParseInt(b, 10, 64)
			if err != nil || e < s {
				return 0, 0, false, false
			}
			end = min(e, size-1)
		}
	}
	return start, end, true, true
}
