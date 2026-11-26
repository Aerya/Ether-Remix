package nuvio

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/store"
)

func init() {
	store.Register("pse_nuvio_links", 1, `
CREATE TABLE pse_nuvio_links(
  user_id INTEGER PRIMARY KEY,
  profile_index INTEGER NOT NULL DEFAULT 0,
  profile_name TEXT NOT NULL DEFAULT '',
  access_token TEXT NOT NULL DEFAULT '',
  refresh_token TEXT NOT NULL DEFAULT '',
  expires_at INTEGER NOT NULL DEFAULT 0,
  cursor INTEGER NOT NULL DEFAULT 0,
  watched_at INTEGER NOT NULL DEFAULT 0,
  pull_on INTEGER NOT NULL DEFAULT 1,
  push_on INTEGER NOT NULL DEFAULT 1,
  last_cycle INTEGER NOT NULL DEFAULT 0,
  imported INTEGER NOT NULL DEFAULT 0,
  sent INTEGER NOT NULL DEFAULT 0,
  unknown INTEGER NOT NULL DEFAULT 0,
  last_error TEXT NOT NULL DEFAULT ''
);
`)
	// v2 : curseurs des vus (relecture bornée).
	store.Register("pse_nuvio_links", 2, `
ALTER TABLE pse_nuvio_links ADD COLUMN watched_cursor INTEGER NOT NULL DEFAULT 0;
ALTER TABLE pse_nuvio_links ADD COLUMN watched_full INTEGER NOT NULL DEFAULT 0;
`)
	store.RegisterViewerTable("pse_nuvio_links", "pse_nuvio_links", "user_id", store.ViewerMergeDrop)
}

// Link est la liaison d'un spectateur à un profil Nuvio.
type Link struct {
	UserID       int64  `json:"-"`
	ProfileIndex int    `json:"profileIndex"`
	ProfileName  string `json:"profileName"`
	Pull         bool   `json:"pull"`
	Push         bool   `json:"push"`
	LastCycle    int64  `json:"lastCycle"`
	Imported     int64  `json:"imported"`
	Sent         int64  `json:"sent"`
	Unknown      int64  `json:"unknown"`
	LastError    string `json:"lastError,omitempty"`
	// Profile choisi : 0 est un index valide chez Nuvio, donc on retient
	// aussi si un choix a été fait (un profil sans nom n'existe pas).
	Chosen bool `json:"chosen"`

	tokens      Tokens
	cursor      int64
	watchedAt   int64 // dernière lecture de la liste des vus (s)
	watchedCur  int64 // plus récent watched_at appliqué (ms)
	watchedFull int64 // dernière relecture complète (s)
}

// Store garde les liaisons ; les jetons sont chiffrés par spectateur et colonne.
type Store struct {
	DB     *sql.DB
	Sealer config.FieldSealer
	Now    func() time.Time

	mu    sync.Mutex
	locks map[int64]*sync.Mutex
}

func (s *Store) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// lock renvoie le verrou de la liaison : le jeton de rafraîchissement étant à
// usage unique, une seule goroutine à la fois lit, rafraîchit et enregistre.
func (s *Store) lock(user int64) *sync.Mutex {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.locks == nil {
		s.locks = map[int64]*sync.Mutex{}
	}
	m := s.locks[user]
	if m == nil {
		m = &sync.Mutex{}
		s.locks[user] = m
	}
	return m
}

func tokenCtx(user int64, col string) []string {
	return []string{"pse_nuvio_links", col, config.FieldContext(user)}
}

const linkCols = `user_id, profile_index, profile_name, access_token, refresh_token, expires_at, cursor, watched_at, watched_cursor, watched_full, pull_on, push_on, last_cycle, imported, sent, unknown, last_error`

type scanner interface{ Scan(...any) error }

func (s *Store) scan(r scanner) (Link, error) {
	var l Link
	var acc, ref string
	var exp, pull, push int64
	if err := r.Scan(&l.UserID, &l.ProfileIndex, &l.ProfileName, &acc, &ref, &exp, &l.cursor, &l.watchedAt, &l.watchedCur, &l.watchedFull, &pull, &push, &l.LastCycle, &l.Imported, &l.Sent, &l.Unknown, &l.LastError); err != nil {
		return Link{}, err
	}
	a, _, err := config.OpenWith(s.Sealer, acc, tokenCtx(l.UserID, "access_token")...)
	if err != nil {
		return Link{}, fmt.Errorf("nuvio: jetons du spectateur %d illisibles: %w", l.UserID, err)
	}
	rf, _, err := config.OpenWith(s.Sealer, ref, tokenCtx(l.UserID, "refresh_token")...)
	if err != nil {
		return Link{}, fmt.Errorf("nuvio: jetons du spectateur %d illisibles: %w", l.UserID, err)
	}
	l.tokens = Tokens{Access: a, Refresh: rf, ExpiresAt: time.Unix(exp, 0)}
	l.Pull, l.Push = pull != 0, push != 0
	l.Chosen = l.ProfileName != ""
	return l, nil
}

// Get renvoie la liaison d'un spectateur (ok faux s'il n'en a pas).
func (s *Store) Get(ctx context.Context, user int64) (Link, bool, error) {
	l, err := s.scan(s.DB.QueryRowContext(ctx, `SELECT `+linkCols+` FROM pse_nuvio_links WHERE user_id=?`, user))
	if errors.Is(err, sql.ErrNoRows) {
		return Link{}, false, nil
	}
	return l, err == nil, err
}

// All renvoie les liaisons (identifiants de spectateurs).
func (s *Store) All(ctx context.Context) ([]Link, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+linkCols+` FROM pse_nuvio_links ORDER BY user_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Link
	for rows.Next() {
		l, err := s.scan(rows)
		if err != nil {
			continue // une liaison illisible n'arrête pas les autres
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// Create enregistre une nouvelle liaison avec ses jetons (sans profil choisi).
func (s *Store) Create(ctx context.Context, user int64, t Tokens) error {
	a, r, err := s.seal(user, t)
	if err != nil {
		return err
	}
	_, err = s.DB.ExecContext(ctx, `INSERT INTO pse_nuvio_links(user_id, access_token, refresh_token, expires_at) VALUES(?,?,?,?)
ON CONFLICT(user_id) DO UPDATE SET access_token=excluded.access_token, refresh_token=excluded.refresh_token, expires_at=excluded.expires_at,
 profile_index=0, profile_name='', cursor=0, watched_at=0, watched_cursor=0, watched_full=0, last_error='', imported=0, sent=0, unknown=0, last_cycle=0`,
		user, a, r, t.ExpiresAt.Unix())
	return err
}

func (s *Store) seal(user int64, t Tokens) (string, string, error) {
	a, err := config.SealWith(s.Sealer, t.Access, tokenCtx(user, "access_token")...)
	if err != nil {
		return "", "", err
	}
	r, err := config.SealWith(s.Sealer, t.Refresh, tokenCtx(user, "refresh_token")...)
	return a, r, err
}

// saveTokens enregistre le nouveau couple de jetons.
func (s *Store) saveTokens(ctx context.Context, user int64, t Tokens) error {
	a, r, err := s.seal(user, t)
	if err != nil {
		return err
	}
	_, err = s.DB.ExecContext(ctx, `UPDATE pse_nuvio_links SET access_token=?, refresh_token=?, expires_at=? WHERE user_id=?`, a, r, t.ExpiresAt.Unix(), user)
	return err
}

// SetProfile choisit le profil et les sens ; changer de profil repart d'un
// import complet.
func (s *Store) SetProfile(ctx context.Context, user int64, index int, name string, pull, push bool) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE pse_nuvio_links SET
 cursor=CASE WHEN profile_index=? AND profile_name<>'' THEN cursor ELSE 0 END,
 watched_at=CASE WHEN profile_index=? AND profile_name<>'' THEN watched_at ELSE 0 END,
 watched_cursor=CASE WHEN profile_index=? AND profile_name<>'' THEN watched_cursor ELSE 0 END,
 watched_full=CASE WHEN profile_index=? AND profile_name<>'' THEN watched_full ELSE 0 END,
 profile_index=?, profile_name=?, pull_on=?, push_on=? WHERE user_id=?`,
		index, index, index, index, index, name, b2i(pull), b2i(push), user)
	return err
}

// SetDirections règle les sens sans changer de profil.
func (s *Store) SetDirections(ctx context.Context, user int64, pull, push bool) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE pse_nuvio_links SET pull_on=?, push_on=? WHERE user_id=?`, b2i(pull), b2i(push), user)
	return err
}

// Delete supprime la liaison et ses jetons.
func (s *Store) Delete(ctx context.Context, user int64) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM pse_nuvio_links WHERE user_id=?`, user)
	return err
}

func (s *Store) report(ctx context.Context, user int64, cycle Summary, st cursors, errText string) {
	_, _ = s.DB.ExecContext(ctx, `UPDATE pse_nuvio_links SET last_cycle=?, cursor=?, watched_at=?, watched_cursor=?, watched_full=?, imported=imported+?, unknown=unknown+?, last_error=? WHERE user_id=?`,
		s.now().Unix(), st.progress, st.watchedAt, st.watchedCur, st.watchedFull, cycle.Imported, cycle.Unknown, errText, user)
}

func (s *Store) addSent(ctx context.Context, user int64, n int, errText string) {
	_, _ = s.DB.ExecContext(ctx, `UPDATE pse_nuvio_links SET sent=sent+?, last_error=CASE WHEN ?<>'' THEN ? ELSE last_error END WHERE user_id=?`, n, errText, errText, user)
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// tokens renvoie un jeton d'accès valable. Le verrou de la liaison est pris :
// un jeton de rafraîchissement à usage unique ne doit jamais partir deux fois.
func (s *Store) accessToken(ctx context.Context, c *Client, user int64, force string) (string, error) {
	m := s.lock(user)
	m.Lock()
	defer m.Unlock()
	l, ok, err := s.Get(ctx, user)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", userError{"Ce spectateur n'est pas relié à Nuvio"}
	}
	now := s.now()
	stillValid := l.tokens.Access != "" && now.Add(time.Minute).Before(l.tokens.ExpiresAt)
	// force : le jeton refusé par Nuvio ; s'il a changé depuis, un autre appel
	// l'a déjà rafraîchi.
	if stillValid && (force == "" || l.tokens.Access != force) {
		return l.tokens.Access, nil
	}
	nt, err := c.Refresh(ctx, l.tokens.Refresh, now)
	if err != nil {
		var se *StatusError
		if errors.Is(err, ErrUnauthorized) || (errors.As(err, &se) && se.Code >= 400 && se.Code < 500) {
			_, _ = s.DB.ExecContext(ctx, `UPDATE pse_nuvio_links SET last_error=? WHERE user_id=?`, "Reconnexion à Nuvio nécessaire", user)
		}
		return "", err
	}
	// Enregistré tout de suite : l'ancien jeton de rafraîchissement est consommé.
	if err := s.saveTokens(ctx, user, nt); err != nil {
		return "", err
	}
	return nt.Access, nil
}

// cursors sont les repères de lecture mémorisés par liaison.
type cursors struct{ progress, watchedAt, watchedCur, watchedFull int64 }
