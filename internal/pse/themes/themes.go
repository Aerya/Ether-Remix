// Package themes gère les bibliothèques à thème de PSE : des bibliothèques
// Plex supplémentaires (Services, Studios, Animes, Concerts…) dont chaque
// rangée est une collection d'Ether. L'onglet Bibliothèque montre tous les
// titres des rangées, l'onglet Recommandé une rangée par collection. Rien
// n'est téléchargé : les titres restent ceux du catalogue d'Ether, lus en
// streaming. Voir docs/pse/FORK.fr.md.
package themes

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/braedonsaunders/ether/internal/pse/mediaaccess"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/store"
)

func init() {
	store.Register("pse_themes", 1, `
CREATE TABLE pse_theme_libraries (
  id INTEGER PRIMARY KEY,
  name TEXT NOT NULL,
  type TEXT NOT NULL,
  collections TEXT NOT NULL DEFAULT '[]',
  position INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL DEFAULT 0
);`)
	// Exceptions de langue : bibliothèques « toutes langues » (concerts…)
	// et collections dont les titres échappent au filtre de langue.
	store.Register("pse_themes", 2, `
ALTER TABLE pse_theme_libraries ADD COLUMN all_languages INTEGER NOT NULL DEFAULT 0;
CREATE TABLE pse_theme_langfree (
  collection_id TEXT PRIMARY KEY,
  position INTEGER NOT NULL DEFAULT 0
);`)
}

func init() {
	store.Register("pse_themes", 4, `ALTER TABLE pse_theme_libraries ADD COLUMN preset_id TEXT NOT NULL DEFAULT '';
UPDATE pse_theme_libraries SET preset_id='anime' WHERE lower(trim(name)) IN ('anime','animes');`)
}

// MaxLibraries borne le nombre de bibliothèques (sections 5101 à 5199).
const MaxLibraries = frontend.PSEThemeShowOffset - 1

// Types de bibliothèque.
const (
	TypeMovie = "movie"
	TypeShow  = "show"
	TypeMixed = "mixed" // Films & séries : deux bibliothèques dans Plex
)

// Library est une bibliothèque à thème.
type Library struct {
	Preset      string   `json:"preset,omitempty"` // modèle d’origine, conservé après renommage
	ID          int64    `json:"id"`
	Name        string   `json:"name"`
	Type        string   `json:"type"`        // movie, show ou mixed
	Collections []string `json:"collections"` // collections d'Ether, dans l'ordre des rangées
	Position    int      `json:"position"`
	// AllLanguages : ses titres échappent au filtre de langue du catalogue
	// (concerts, spectacles…), dans cette bibliothèque seulement : sans VF,
	// ils n'apparaissent ni dans Films, ni dans Séries, ni sur l'accueil.
	AllLanguages bool `json:"allLanguages"`
}

// Section renvoie le numéro de section Plex de la bibliothèque.
func (l Library) Section() int { return frontend.PSEThemeSectionBase + int(l.ID) }

// ShowSection renvoie la section Plex de la moitié Séries d'une
// bibliothèque « Films & séries ».
func (l Library) ShowSection() int { return l.Section() + frontend.PSEThemeShowOffset }

// Plex classe chaque bibliothèque comme Films ou Séries : une bibliothèque
// « Films & séries » y apparaît en deux parties.
const (
	movieSuffix = " · Films"
	showSuffix  = " · Séries"
)

// ErrInvalid signale une saisie refusée.
var ErrInvalid = errors.New("bibliothèque invalide")

func invalid(format string, a ...any) error {
	return fmt.Errorf("%w : %s", ErrInvalid, fmt.Sprintf(format, a...))
}

// Collections donne accès aux collections d'Ether.
type Collections interface {
	ListSpecs(ctx context.Context) ([]core.CollectionSpec, error)
	PSEKeys(ctx context.Context, id string, user *core.User) []core.RatingKey
}

// Service garde les bibliothèques en mémoire et sert le serveur Plex.
type Service struct {
	db   *sql.DB
	cols Collections

	mu      sync.RWMutex
	libs    []Library
	free    []string                  // collections exemptées du filtre de langue
	titles  map[string]string         // nom des collections
	types   map[string]string         // type des collections (movie, show, mixed)
	keys    map[string]core.RatingKey // clé Plex des collections
	hooks   PriorityHooks
	private map[string]string
}

// New charge les bibliothèques enregistrées.
func New(ctx context.Context, db *sql.DB, cols Collections) (*Service, error) {
	s := &Service{db: db, cols: cols, titles: map[string]string{}, types: map[string]string{}}
	return s, s.Reload(ctx)
}

// Reload relit les bibliothèques et le nom des collections.
func (s *Service) Reload(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, type, collections, position, all_languages, preset_id FROM pse_theme_libraries ORDER BY position, id`)
	if err != nil {
		return err
	}
	defer rows.Close()
	var libs []Library
	for rows.Next() {
		var l Library
		var raw string
		var all int
		if err := rows.Scan(&l.ID, &l.Name, &l.Type, &raw, &l.Position, &all, &l.Preset); err != nil {
			return err
		}
		l.AllLanguages = all != 0
		_ = json.Unmarshal([]byte(raw), &l.Collections)
		libs = append(libs, l)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	rows.Close()
	free, err := s.loadFree(ctx)
	if err != nil {
		return err
	}
	private := map[string]string{}
	titles, types, keys := map[string]string{}, map[string]string{}, map[string]core.RatingKey{}
	if s.cols != nil {
		if specs, err := s.cols.ListSpecs(ctx); err == nil {
			for _, sp := range specs {
				titles[sp.ID], types[sp.ID] = sp.Name, sp.MediaType
				if strings.HasPrefix(sp.TemplateID, "remix-local-") {
					private[sp.ID] = "local:" + strings.TrimPrefix(sp.TemplateID, "remix-local-")
				}
				if k, err := core.ParseRatingKey(sp.RatingKey); err == nil {
					keys[sp.ID] = k
				}
			}
		}
	}
	s.mu.Lock()
	s.libs, s.free, s.titles, s.types, s.keys = libs, free, titles, types, keys
	s.private = private
	s.mu.Unlock()
	return nil
}

// Libraries renvoie une copie des bibliothèques.
func (s *Service) Libraries() []Library {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Library, len(s.libs))
	for i, l := range s.libs {
		l.Collections = append([]string(nil), l.Collections...)
		out[i] = l
	}
	return out
}

// Themes implémente frontend.PSEThemes.
func (s *Service) Themes() []frontend.PSETheme {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]frontend.PSETheme, 0, len(s.libs))
	for _, l := range s.libs {
		switch l.Type {
		case TypeMixed:
			// Une partie par type, chacune avec les collections de son
			// type (ou mixtes) ; une partie sans rangée n'apparaît pas.
			for _, th := range []frontend.PSETheme{
				s.theme(l, l.Section(), l.Name+movieSuffix, core.TypeMovie, true),
				s.theme(l, l.ShowSection(), l.Name+showSuffix, core.TypeShow, true),
			} {
				if len(th.Rows) > 0 {
					out = append(out, th)
				}
			}
		case TypeShow:
			out = append(out, s.theme(l, l.Section(), l.Name, core.TypeShow, false))
		default:
			out = append(out, s.theme(l, l.Section(), l.Name, core.TypeMovie, false))
		}
	}
	return out
}

// theme décrit une bibliothèque Plex ; filter ne garde que les collections
// du type (ou mixtes).
func (s *Service) theme(l Library, section int, title string, typ core.MediaType, filter bool) frontend.PSETheme {
	th := frontend.PSETheme{Section: section, Title: title, Type: typ, AllLanguages: l.AllLanguages}
	sources := []string{}
	for _, id := range l.Collections {
		if p := s.private[id]; p != "" {
			sources = append(sources, p)
		}
	}
	if len(sources) > 0 {
		th.VisibleTo = func(u *core.User) bool {
			if u == nil {
				return false
			}
			for _, p := range sources {
				if !mediaaccess.Allowed(context.Background(), s.db, p, u.ID) {
					return false
				}
			}
			return true
		}
	}
	for _, c := range l.Collections {
		if ct := s.types[c]; filter && ct != "" && ct != "mixed" && ct != string(typ) {
			continue
		}
		name := s.titles[c]
		if name == "" {
			name = l.Name
		}
		th.Rows = append(th.Rows, frontend.PSEThemeRow{ID: c, Title: name, Key: s.keys[c], LangFree: l.AllLanguages || s.isFree(c)})
	}
	return th
}

// RowKeys implémente frontend.PSEThemes.
func (s *Service) RowKeys(ctx context.Context, row string, user *core.User) []core.RatingKey {
	if s.cols == nil {
		return nil
	}
	return s.cols.PSEKeys(ctx, row, user)
}

// AllKeys renvoie les titres de toutes les bibliothèques d'un type, pour
// que l'indexeur cherche leurs sources en priorité.
func (s *Service) AllKeys(ctx context.Context, typ core.MediaType) []core.RatingKey {
	seen := map[core.RatingKey]bool{}
	var out []core.RatingKey
	for _, th := range s.Themes() {
		if th.Type != typ {
			continue
		}
		for _, k := range frontend.PSEThemeKeys(ctx, s, th, nil) {
			if !seen[k] {
				seen[k] = true
				out = append(out, k)
			}
		}
	}
	return out
}

func (s *Service) clean(ctx context.Context, l Library) (Library, error) {
	l.Name = strings.TrimSpace(l.Name)
	if l.Preset != "" {
		if _, ok := presetByID(l.Preset); !ok {
			return l, invalid("modèle inconnu")
		}
	}
	if l.Name == "" || utf8.RuneCountInString(l.Name) > 60 {
		return l, invalid("nom vide ou trop long (60 caractères au plus)")
	}
	if l.Type != TypeMovie && l.Type != TypeShow && l.Type != TypeMixed {
		return l, invalid("type « movie », « show » ou « mixed » attendu")
	}
	seen := map[string]bool{}
	var cols []string
	for _, c := range l.Collections {
		c = strings.TrimSpace(c)
		if c != "" && !seen[c] {
			seen[c] = true
			cols = append(cols, c)
		}
	}
	if len(cols) == 0 {
		return l, invalid("choisissez au moins une collection")
	}
	if len(cols) > 60 {
		return l, invalid("60 collections au plus par bibliothèque")
	}
	if s.cols != nil {
		specs, err := s.cols.ListSpecs(ctx)
		if err != nil {
			return l, err
		}
		known := map[string]bool{}
		for _, sp := range specs {
			known[sp.ID] = true
		}
		for _, c := range cols {
			if !known[c] {
				return l, invalid("collection %s introuvable", c)
			}
		}
	}
	l.Collections = cols
	return l, nil
}

// Create ajoute une bibliothèque en fin de liste.
func (s *Service) Create(ctx context.Context, l Library) (Library, error) {
	l, err := s.clean(ctx, l)
	if err != nil {
		return l, err
	}
	used := map[int64]bool{}
	pos := 0
	for _, x := range s.Libraries() {
		used[x.ID] = true
		pos = max(pos, x.Position+1)
	}
	l.ID = 0
	for id := int64(1); id <= MaxLibraries; id++ {
		if !used[id] {
			l.ID = id
			break
		}
	}
	if l.ID == 0 {
		return l, invalid("%d bibliothèques au plus", MaxLibraries)
	}
	l.Position = pos
	raw, _ := json.Marshal(l.Collections)
	if _, err := s.db.ExecContext(ctx, `INSERT INTO pse_theme_libraries(id, name, type, collections, position, created_at, all_languages, preset_id) VALUES(?, ?, ?, ?, ?, ?, ?, ?)`,
		l.ID, l.Name, l.Type, string(raw), l.Position, time.Now().Unix(), boolInt(l.AllLanguages), l.Preset); err != nil {
		return l, err
	}
	return l, s.Reload(ctx)
}

// Update remplace le nom, le type et les rangées d'une bibliothèque.
func (s *Service) Update(ctx context.Context, l Library) (Library, error) {
	l, err := s.clean(ctx, l)
	if err != nil {
		return l, err
	}
	raw, _ := json.Marshal(l.Collections)
	res, err := s.db.ExecContext(ctx, `UPDATE pse_theme_libraries SET name=?, type=?, collections=?, all_languages=? WHERE id=?`, l.Name, l.Type, string(raw), boolInt(l.AllLanguages), l.ID)
	if err != nil {
		return l, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return l, core.ErrNotFound
	}
	return l, s.Reload(ctx)
}

// Delete retire une bibliothèque ; ses collections restent.
func (s *Service) Delete(ctx context.Context, id int64) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM pse_theme_libraries WHERE id=?`, id); err != nil {
		return err
	}
	if err := forgetLibraryPriority(ctx, s.db, id); err != nil {
		return err
	}
	return s.Reload(ctx)
}

// Move déplace une bibliothèque d'un cran (dir -1 ou +1).
func (s *Service) Move(ctx context.Context, id int64, dir int) error {
	libs := s.Libraries()
	sort.SliceStable(libs, func(i, j int) bool { return libs[i].Position < libs[j].Position })
	i := -1
	for k, l := range libs {
		if l.ID == id {
			i = k
		}
	}
	j := i + dir
	if i < 0 || j < 0 || j >= len(libs) {
		return nil
	}
	libs[i], libs[j] = libs[j], libs[i]
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for k, l := range libs {
		if _, err := tx.ExecContext(ctx, `UPDATE pse_theme_libraries SET position=? WHERE id=?`, k, l.ID); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return s.Reload(ctx)
}

// ShowPresetCollections affiche dans l'onglet Collections de Plex les
// collections créées par les modèles avant qu'elles n'y apparaissent par
// défaut.
func (s *Service) ShowPresetCollections(ctx context.Context, ed Editor) error {
	if s.cols == nil || ed == nil {
		return nil
	}
	specs, err := s.cols.ListSpecs(ctx)
	if err != nil {
		return err
	}
	changed := false
	for _, sp := range specs {
		if sp.Description != presetDescription || sp.Targets.CollectionsTab {
			continue
		}
		sp.Targets.CollectionsTab = true
		if _, err := ed.UpdateSpec(ctx, sp.ID, sp); err != nil {
			return err
		}
		changed = true
	}
	if changed {
		return s.Reload(ctx)
	}
	return nil
}
