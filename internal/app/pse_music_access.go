package app

import (
	"context"
	"crypto/md5" // Subsonic token protocol, not password storage.
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/braedonsaunders/ether/internal/store"
)

func init() {
	store.Register("pse_music_access", 1, `CREATE TABLE pse_music_access(viewer_id INTEGER PRIMARY KEY, secret TEXT NOT NULL);`)
	store.RegisterViewerPurge("pse_music_access", []string{"pse_music_access"}, func(ctx context.Context, tx *sql.Tx, viewer int64) error {
		_, err := tx.ExecContext(ctx, "DELETE FROM pse_music_access WHERE viewer_id=?", viewer)
		return err
	})
	store.RegisterViewerMerge("pse_music_access", []string{"pse_music_access"}, func(ctx context.Context, tx *sql.Tx, from, to int64) error {
		// Revoke the merged identity rather than silently widening its access.
		_, err := tx.ExecContext(ctx, "DELETE FROM pse_music_access WHERE viewer_id=?", from)
		return err
	})
}

var errMusicAccess = errors.New("invalid music access")

func musicUsername(id int64) string { return fmt.Sprintf("music-%d", id) }

// A separate random secret permits legacy MD5 token clients without retaining
// the account's login password. Every request checks revocation and membership.
func (a *App) pseMusicTokenAuth(ctx context.Context, user, token, salt string) (int64, error) {
	if len(user) > 64 || len(token) != 32 || len(salt) == 0 || len(salt) > 256 {
		return 0, errMusicAccess
	}
	id, err := strconv.ParseInt(strings.TrimPrefix(user, "music-"), 10, 64)
	if err != nil || id <= 0 || user != musicUsername(id) {
		return 0, errMusicAccess
	}
	got, err := hex.DecodeString(token)
	if err != nil {
		return 0, errMusicAccess
	}
	if a.users == nil {
		return 0, errMusicAccess
	}
	if _, err := a.users.PolicyUser(ctx, id); err != nil {
		return 0, errMusicAccess
	}
	var sealed string
	err = a.db.QueryRowContext(ctx, "SELECT secret FROM pse_music_access WHERE viewer_id=?", id).Scan(&sealed)
	if err != nil || a.cfg == nil {
		return 0, errMusicAccess
	}
	secret, legacy, err := a.cfg.OpenField(sealed, "pse_music_access", "secret", strconv.FormatInt(id, 10))
	if err != nil || legacy || secret == "" {
		return 0, errMusicAccess
	}
	want := md5.Sum([]byte(secret + salt))
	if subtle.ConstantTimeCompare(got, want[:]) != 1 {
		return 0, errMusicAccess
	}
	return id, nil
}

// Mounted only under the owner-authenticated, CSRF-protected console API.
// The owner provisions a separate music identity for each active viewer.
func (a *App) pseMusicAccess(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	if r.Method == http.MethodGet {
		if a.users == nil {
			http.Error(w, "Spectateurs indisponibles", 503)
			return
		}
		viewers, err := a.users.AllUsers(r.Context())
		if err != nil {
			http.Error(w, "Spectateurs indisponibles", 503)
			return
		}
		rows, err := a.db.QueryContext(r.Context(), "SELECT viewer_id FROM pse_music_access")
		if err != nil {
			http.Error(w, "Accès musicaux indisponibles", 503)
			return
		}
		configured := map[int64]bool{}
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				http.Error(w, "Accès musicaux indisponibles", 503)
				return
			}
			configured[id] = true
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			http.Error(w, "Accès musicaux indisponibles", 503)
			return
		}
		type access struct {
			ID       int64  `json:"id"`
			Name     string `json:"name"`
			Username string `json:"username"`
			Enabled  bool   `json:"enabled"`
		}
		result := []access{}
		for _, v := range viewers {
			if !v.Active || v.Origin == "dlna_guest" {
				continue
			}
			name := v.Title
			if name == "" {
				name = v.Username
			}
			if name == "" {
				name = "Spectateur"
			}
			result = append(result, access{v.ID, name, musicUsername(v.ID), configured[v.ID]})
		}
		_ = json.NewEncoder(w).Encode(result)
		return
	}
	if r.Method != http.MethodPost && r.Method != http.MethodDelete {
		w.Header().Set("Allow", "GET, POST, DELETE")
		http.Error(w, "Méthode interdite", 405)
		return
	}
	var body struct {
		Viewer int64 `json:"viewer"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&body) != nil || body.Viewer <= 0 {
		http.Error(w, "Spectateur invalide", 400)
		return
	}
	if a.users == nil {
		http.Error(w, "Spectateur indisponible", 404)
		return
	}
	viewers, err := a.users.AllUsers(r.Context())
	eligible := false
	for _, v := range viewers {
		if v.ID == body.Viewer && v.Active && v.Origin != "dlna_guest" {
			eligible = true
		}
	}
	if err != nil || !eligible {
		http.Error(w, "Spectateur indisponible", 404)
		return
	}
	if r.Method == http.MethodDelete {
		if _, err := a.db.ExecContext(r.Context(), "DELETE FROM pse_music_access WHERE viewer_id=?", body.Viewer); err != nil {
			http.Error(w, "Révocation impossible", 503)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})
		return
	}
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		http.Error(w, "Création impossible", 503)
		return
	}
	secret := hex.EncodeToString(raw[:])
	if a.cfg == nil {
		http.Error(w, "Chiffrement indisponible", 503)
		return
	}
	sealed, err := a.cfg.SealField(secret, "pse_music_access", "secret", strconv.FormatInt(body.Viewer, 10))
	if err != nil {
		http.Error(w, "Chiffrement indisponible", 503)
		return
	}
	_, err = a.db.ExecContext(r.Context(), `INSERT INTO pse_music_access(viewer_id,secret) VALUES(?,?) ON CONFLICT(viewer_id) DO UPDATE SET secret=excluded.secret`, body.Viewer, sealed)
	if err != nil {
		http.Error(w, "Création impossible", 503)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]string{"username": musicUsername(body.Viewer), "password": secret})
}
