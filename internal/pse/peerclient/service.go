package peerclient

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/pse/xdcc"
	"github.com/braedonsaunders/ether/internal/store"
)

func init() {
	store.Register("pse_peer", 1, `CREATE TABLE pse_peer_items (source TEXT NOT NULL, id TEXT NOT NULL, data TEXT NOT NULL, started INTEGER NOT NULL, seen INTEGER NOT NULL, PRIMARY KEY(source,id)); CREATE INDEX pse_peer_history ON pse_peer_items(source,started,seen);`)
}

type Item struct {
	Hit      Hit    `json:"hit"`
	Started  int64  `json:"started"`
	State    string `json:"state"`
	Received int64  `json:"received"`
}
type servedFile struct {
	Hit      Hit
	Relative string
	Expires  time.Time
}

type Service struct {
	Client   *Client
	DB       *sql.DB
	mu       sync.Mutex
	server   *http.Server
	listener net.Listener
	tokens   map[string]servedFile
	closed   bool
}

func (s *Service) key() string { return s.Client.Settings.Identity() }
func (s *Service) save(ctx context.Context, i Item) error {
	raw, e := json.Marshal(i)
	if e != nil {
		return e
	}
	_, e = s.DB.ExecContext(ctx, "INSERT INTO pse_peer_items(source,id,data,started,seen) VALUES(?,?,?,?,?) ON CONFLICT(source,id) DO UPDATE SET data=excluded.data,started=excluded.started,seen=excluded.seen", s.key(), i.Hit.ID, string(raw), i.Started, time.Now().Unix())
	return e
}
func (s *Service) Item(ctx context.Context, id string) (Item, error) {
	var i Item
	var raw string
	err := s.DB.QueryRowContext(ctx, "SELECT data FROM pse_peer_items WHERE source=? AND id=?", s.key(), id).Scan(&raw)
	if err != nil {
		return i, err
	}
	err = json.Unmarshal([]byte(raw), &i)
	return i, err
}
func (s *Service) Items(ctx context.Context) ([]Item, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT data FROM pse_peer_items WHERE source=? AND started>0 ORDER BY started DESC", s.key())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Item{}
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var i Item
		if json.Unmarshal([]byte(raw), &i) == nil && i.Started > 0 {
			out = append(out, i)
		}
	}
	return out, rows.Err()
}
func (s *Service) Search(ctx context.Context, q string) ([]Hit, error) {
	hits, err := s.Client.Search(ctx, q)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// Expire search-only observations; retain every download intent across restarts.
	if _, err := s.DB.ExecContext(ctx, "DELETE FROM pse_peer_items WHERE started=0 AND seen<?", time.Now().Add(-7*24*time.Hour).Unix()); err != nil {
		return nil, err
	}
	var count int
	if err := s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM pse_peer_items WHERE source=? AND started=0", s.key()).Scan(&count); err != nil {
		return nil, err
	}
	if count >= 50000 {
		if _, err := s.DB.ExecContext(ctx, "DELETE FROM pse_peer_items WHERE source=? AND started=0 AND id IN (SELECT id FROM pse_peer_items WHERE source=? AND started=0 ORDER BY seen LIMIT 200)", s.key(), s.key()); err != nil {
			return nil, err
		}
	}
	for _, h := range hits {
		if i, err := s.Item(ctx, h.ID); errors.Is(err, sql.ErrNoRows) {
			if err = s.save(ctx, Item{Hit: h}); err != nil {
				return nil, err
			}
		} else if err != nil {
			return nil, err
		} else if err = s.save(ctx, i); err != nil {
			return nil, err
		}
	}
	return hits, nil
}
func (s *Service) Ready(ctx context.Context, id string, allow bool) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return "", core.ErrSourceUnavailable
	}
	i, err := s.Item(ctx, id)
	if err != nil {
		return "", core.ErrSourceUnavailable
	}
	t, err := s.Client.Transfer(ctx, i.Hit)
	if errors.Is(err, ErrMissing) && i.Started == 0 {
		if !allow {
			return "", core.ErrForegroundOnly
		}
		items, e := s.Items(ctx)
		if e != nil {
			return "", e
		}
		active, daily := 0, 0
		for _, x := range items {
			if x.State != "completed" && x.State != "failed" {
				if tr, e := s.Client.Transfer(ctx, x.Hit); e == nil {
					x.State, x.Received = tr.State, tr.Received
				} else if errors.Is(e, ErrMissing) && time.Since(time.Unix(x.Started, 0)) > 2*time.Minute {
					x.State = "failed"
				}
				if e = s.save(ctx, x); e != nil {
					return "", e
				}
			}
			if x.State != "completed" && x.State != "failed" {
				active++
			}
			if time.Since(time.Unix(x.Started, 0)) < 24*time.Hour {
				daily++
			}
		}
		if active >= 2 || daily >= 20 {
			return "", core.ErrDownloadLimit
		}
		// Persist intent before the external mutation: timeouts/restarts never duplicate downloads.
		i.Started = time.Now().Unix()
		i.State = "queued"
		if e = s.save(ctx, i); e != nil {
			return "", e
		}
		if e = s.Client.Enqueue(ctx, i.Hit); e != nil {
			// The remote service may have accepted the request before its response
			// was lost. Retain the intent and active slot until Transfer confirms
			// completion/failure or sustained absence; never retry this POST.
			return "", e
		}
		return "", queued()
	}
	if errors.Is(err, ErrMissing) && i.Started > 0 {
		if time.Since(time.Unix(i.Started, 0)) > 2*time.Minute {
			i.State = "failed"
			if e := s.save(ctx, i); e != nil {
				return "", e
			}
		}
		if i.State == "failed" {
			return "", core.ErrSourceUnavailable
		}
		return "", queued()
	}
	if err != nil {
		return "", err
	}
	i.State, i.Received = t.State, t.Received
	if err = s.save(ctx, i); err != nil {
		return "", err
	}
	if t.State == "failed" || t.State == "erroneous" || t.State == "insufficient_disk" {
		return "", core.ErrSourceUnavailable
	}
	if t.State != "completed" {
		return "", queued()
	}
	f, err := s.Open(t.Relative, i.Hit.Size)
	if err != nil {
		return "", err
	}
	_ = f.Close()
	return t.Relative, nil
}
func queued() error {
	return &core.LocalNoticeError{FR: "Téléchargement P2P en cours ; réessayez après sa fin.", EN: "P2P download in progress; retry when complete.", Err: fmt.Errorf("%w: %w", core.ErrDownloadQueued, core.ErrPrepareSlow)}
}

// Open confines reads to the configured mount, including symlink resolution.
func (s *Service) Open(relative string, size int64) (*os.File, error) {
	if !filepath.IsLocal(relative) {
		return nil, core.ErrSourceUnavailable
	}
	root, err := os.OpenRoot(s.Client.Settings.Root)
	if err != nil {
		return nil, core.ErrSourceUnavailable
	}
	defer root.Close()
	f, err := root.Open(relative)
	if err != nil {
		return nil, core.ErrSourceUnavailable
	}
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() || st.Size() != size {
		f.Close()
		return nil, core.ErrSourceUnavailable
	}
	return f, nil
}
func (s *Service) SearchReleases(ctx context.Context, req core.SearchRequest, reserve func(context.Context, string) error) ([]core.Release, error) {
	out := []core.Release{}
	seen := map[string]bool{}
	for _, q := range xdcc.Queries(req) {
		if reserve != nil {
			if err := reserve(ctx, "hit"); err != nil {
				return out, err
			}
		}
		hits, err := s.Search(ctx, q)
		if err != nil {
			return out, err
		}
		for _, h := range hits {
			info, ok := xdcc.Matches(h.Name, req)
			if !ok || seen[h.ID] {
				continue
			}
			seen[h.ID] = true
			cat := 2000
			if req.Type != core.TypeMovie {
				cat = 5000
			}
			out = append(out, core.Release{GUID: s.Client.Settings.Kind + ":" + s.key() + ":" + h.ID, Name: h.Name, Size: h.Size, Info: info, Category: cat, Protocol: core.ProtocolHTTP, StreamURL: "http://127.0.0.1/peer/" + h.ID, StreamOrigin: "http://127.0.0.1", StreamOnDemand: true})
		}
	}
	return out, nil
}
func (s *Service) Prepare(ctx context.Context, r core.Release) (core.Release, bool, error) {
	prefix := s.Client.Settings.Kind + ":"
	if !strings.HasPrefix(r.GUID, prefix) {
		return r, false, nil
	}
	id, ok := strings.CutPrefix(r.GUID, prefix+s.key()+":")
	if !ok {
		return r, true, core.ErrSourceUnavailable
	}
	i, err := s.Item(ctx, id)
	if err != nil || i.Hit.Name != r.Name || i.Hit.Size != r.Size {
		return r, true, core.ErrSourceUnavailable
	}
	rel, err := s.Ready(ctx, id, core.OnDemandStreamsAllowed(ctx) && !core.PSEAvailabilityCheck(ctx))
	if err != nil {
		return r, true, err
	}
	u, err := s.fileURL(i.Hit, rel)
	if err != nil {
		return r, true, err
	}
	r.StreamURL = u
	r.StreamOnDemand = false
	return r, true, nil
}
func (s *Service) fileURL(hit Hit, relative string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return "", core.ErrSourceUnavailable
	}
	if s.listener == nil {
		ln, e := net.Listen("tcp", "127.0.0.1:0")
		if e != nil {
			return "", e
		}
		s.listener = ln
		s.tokens = map[string]servedFile{}
		s.server = &http.Server{Handler: http.HandlerFunc(s.serve), ReadHeaderTimeout: 10 * time.Second}
		go s.server.Serve(ln)
	}
	var random [24]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", err
	}
	token := hex.EncodeToString(random[:])
	for k, item := range s.tokens {
		if time.Now().After(item.Expires) {
			delete(s.tokens, k)
		}
	}
	if len(s.tokens) >= 4096 {
		return "", core.ErrDownloadLimit
	}
	s.tokens[token] = servedFile{Hit: hit, Relative: relative, Expires: time.Now().Add(12 * time.Hour)}
	return "http://" + s.listener.Addr().String() + "/" + token, nil
}
func (s *Service) serve(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" && r.Method != "HEAD" {
		w.WriteHeader(405)
		return
	}
	s.mu.Lock()
	item, ok := s.tokens[strings.TrimPrefix(r.URL.Path, "/")]
	s.mu.Unlock()
	if !ok || time.Now().After(item.Expires) {
		http.NotFound(w, r)
		return
	}
	f, err := s.Open(item.Relative, item.Hit.Size)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	st, _ := f.Stat()
	http.ServeContent(w, r, item.Hit.Name, st.ModTime(), f)
}
func (s *Service) Close() {
	s.mu.Lock()
	s.closed = true
	srv := s.server
	s.mu.Unlock()
	if srv != nil {
		_ = srv.Close()
	}
}
func (s *Service) MusicOpen(ctx context.Context, id string) (func() (io.ReadSeekCloser, time.Time, error), error) {
	relative, err := s.Ready(ctx, id, core.OnDemandStreamsAllowed(ctx) && !core.PSEAvailabilityCheck(ctx))
	if err != nil {
		return nil, err
	}
	i, err := s.Item(ctx, id)
	if err != nil {
		return nil, err
	}
	return func() (io.ReadSeekCloser, time.Time, error) {
		f, e := s.Open(relative, i.Hit.Size)
		if e != nil {
			return nil, time.Time{}, e
		}
		st, e := f.Stat()
		if e != nil {
			f.Close()
			return nil, time.Time{}, e
		}
		return f, st.ModTime(), nil
	}, nil
}
