// Package layouts stores named, reusable presentation snapshots. Participants
// own their tables; all parts and the undo snapshot commit in one transaction.
package layouts

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/braedonsaunders/ether/internal/store"
)

func init() {
	store.Register("pse_layouts", 1, `CREATE TABLE pse_layouts_templates (id TEXT PRIMARY KEY, name TEXT NOT NULL, document TEXT NOT NULL, created_at BIGINT NOT NULL);`)
}

type Part struct {
	Name    string
	Capture func(context.Context, *sql.Tx) (json.RawMessage, error)
	Restore func(context.Context, *sql.Tx, json.RawMessage) error
	Changed func(context.Context)
}
type Service struct {
	db    *sql.DB
	parts []Part
	mu    sync.Mutex
}

func New(db *sql.DB, parts ...Part) *Service { return &Service{db: db, parts: parts} }

type Entry struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	CreatedAt int64  `json:"createdAt"`
}
type document map[string]json.RawMessage

func (s *Service) capture(ctx context.Context, tx *sql.Tx) (document, error) {
	d := document{}
	for _, p := range s.parts {
		v, e := p.Capture(ctx, tx)
		if e != nil {
			return nil, e
		}
		d[p.Name] = v
	}
	return d, nil
}
func write(ctx context.Context, tx *sql.Tx, id, name string, d document) error {
	b, e := json.Marshal(d)
	if e != nil {
		return e
	}
	_, e = tx.ExecContext(ctx, `INSERT INTO pse_layouts_templates(id,name,document,created_at) VALUES(?,?,?,?) ON CONFLICT(id) DO UPDATE SET name=excluded.name,document=excluded.document,created_at=excluded.created_at`, id, name, string(b), time.Now().Unix())
	return e
}
func read(ctx context.Context, tx *sql.Tx, id string) (document, error) {
	var raw string
	err := tx.QueryRowContext(ctx, `SELECT document FROM pse_layouts_templates WHERE id=?`, id).Scan(&raw)
	if err != nil {
		return nil, err
	}
	var d document
	err = json.Unmarshal([]byte(raw), &d)
	return d, err
}
func (s *Service) Save(ctx context.Context, name, from string) error {
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > 80 {
		return errors.New("nom attendu (1 à 80 caractères)")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, e := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var count int
	if e = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM pse_layouts_templates WHERE id <> '_previous'`).Scan(&count); e != nil {
		return e
	}
	if count >= 50 {
		return errors.New("limite de 50 modèles atteinte")
	}
	var d document
	if from == "" {
		d, e = s.capture(ctx, tx)
	} else {
		d, e = read(ctx, tx, from)
	}
	if e != nil {
		return e
	}
	var id [16]byte
	if _, e = rand.Read(id[:]); e != nil {
		return e
	}
	if e = write(ctx, tx, hex.EncodeToString(id[:]), name, d); e != nil {
		return e
	}
	return tx.Commit()
}
func (s *Service) Apply(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, e := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if e != nil {
		return e
	}
	defer tx.Rollback()
	d, e := read(ctx, tx, id)
	if e != nil {
		return e
	}
	previous, e := s.capture(ctx, tx)
	if e != nil {
		return e
	}
	if len(d) != len(s.parts) {
		return errors.New("modèle incompatible avec les composants disponibles")
	}
	for _, p := range s.parts {
		raw, ok := d[p.Name]
		if !ok {
			return fmt.Errorf("partie absente : %s", p.Name)
		}
		if e = p.Restore(ctx, tx, raw); e != nil {
			return fmt.Errorf("%s : %w", p.Name, e)
		}
	}
	if e = write(ctx, tx, "_previous", "Avant la dernière application", previous); e != nil {
		return e
	}
	if e = tx.Commit(); e != nil {
		return e
	}
	for _, p := range s.parts {
		if p.Changed != nil {
			p.Changed(ctx)
		}
	}
	return nil
}
func (s *Service) List(ctx context.Context) ([]Entry, error) {
	rows, e := s.db.QueryContext(ctx, `SELECT id,name,created_at FROM pse_layouts_templates ORDER BY created_at DESC,id`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Entry{}
	for rows.Next() {
		var x Entry
		if e = rows.Scan(&x.ID, &x.Name, &x.CreatedAt); e != nil {
			return nil, e
		}
		out = append(out, x)
	}
	return out, rows.Err()
}
func (s *Service) Handler() http.Handler {
	mux := http.NewServeMux()
	send := func(w http.ResponseWriter, r *http.Request, err error) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		if err != nil {
			w.WriteHeader(400)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		entries, e := s.List(r.Context())
		if e != nil {
			w.WriteHeader(500)
			json.NewEncoder(w).Encode(map[string]string{"error": "modèles indisponibles"})
			return
		}
		json.NewEncoder(w).Encode(entries)
	}
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) { send(w, r, nil) })
	mux.HandleFunc("POST /{$}", func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			Name string `json:"name"`
			From string `json:"from"`
		}
		e := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&b)
		if e == nil {
			e = s.Save(r.Context(), b.Name, b.From)
		}
		send(w, r, e)
	})
	mux.HandleFunc("POST /{id}/apply", func(w http.ResponseWriter, r *http.Request) { send(w, r, s.Apply(r.Context(), r.PathValue("id"))) })
	mux.HandleFunc("DELETE /{id}", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		_, e := s.db.ExecContext(r.Context(), `DELETE FROM pse_layouts_templates WHERE id=?`, r.PathValue("id"))
		s.mu.Unlock()
		send(w, r, e)
	})
	return mux
}
