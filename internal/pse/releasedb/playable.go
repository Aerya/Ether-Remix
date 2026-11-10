package releasedb

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/braedonsaunders/ether/internal/pse/pseenv"
	"github.com/braedonsaunders/ether/internal/pse/seerrcatalog"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

// Catalogue « lisible uniquement » : les listes du catalogue (accueil,
// bibliothèques, recommandations, épisodes) ne montrent que les titres qui
// ont au moins Min releases indexées respectant le filtre. Les listes
// demandées par clé (watchlist, collections, reprise de lecture) et la
// recherche ne sont pas filtrées : un titre déjà choisi reste accessible.

func init() {
	store.Register("pse_releases", 2, `
CREATE TABLE pse_playable(
  title_key INTEGER PRIMARY KEY,
  releases INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE pse_state(
  k TEXT PRIMARY KEY,
  v TEXT NOT NULL DEFAULT ''
);
`)
	// Les flux HTTP d'un addon Stremio déjà indexés comptent comme en cache
	// (voir cachedHints) ; le catalogue lisible est recalculé.
	store.Register("pse_releases", 3, `
UPDATE pse_releases SET debrid_cached='direct' WHERE protocol='http' AND debrid_cached='' AND source_id NOT IN (SELECT DISTINCT source_id FROM pse_releases WHERE debrid_cached<>'');
DELETE FROM pse_state WHERE k='playable_signature';
`)
	// Exceptions de langue : titres (concerts, sélection en VOSTFR…) gardés
	// au catalogue sans release dans les langues du filtre. any_lang compte
	// leurs releases en ignorant la langue.
	store.Register("pse_releases", 4, `
ALTER TABLE pse_playable ADD COLUMN any_lang INTEGER NOT NULL DEFAULT 0;
CREATE TABLE pse_lang_exempt(
  title_key INTEGER PRIMARY KEY
);
DELETE FROM pse_state WHERE k='playable_signature';
`)
	// VF, VFI et VOF comptent désormais comme français (languages.go) :
	// les releases déjà enregistrées sont corrigées, le catalogue lisible
	// est recalculé.
	store.Register("pse_releases", 5, `
UPDATE pse_releases SET languages = CASE WHEN languages='' THEN 'french' ELSE languages||',french' END
WHERE ','||languages||',' NOT LIKE '%,french,%' AND (
  ' '||UPPER(name)||' ' GLOB '*[^A-Z0-9]VF[^A-Z0-9]*' OR
  ' '||UPPER(name)||' ' GLOB '*[^A-Z0-9]VFI[^A-Z0-9]*' OR
  ' '||UPPER(name)||' ' GLOB '*[^A-Z0-9]VOF[^A-Z0-9]*');
DELETE FROM pse_state WHERE k='playable_signature';
`)
	// Marqueur "vfi" (version française internationale), sélectionnable
	// seul comme VFF et VFQ.
	store.Register("pse_releases", 6, `
UPDATE pse_releases SET languages = CASE WHEN languages='' THEN 'vfi' ELSE languages||',vfi' END
WHERE ','||languages||',' NOT LIKE '%,vfi,%' AND ' '||UPPER(name)||' ' GLOB '*[^A-Z0-9]VFI[^A-Z0-9]*';
DELETE FROM pse_state WHERE k='playable_signature';
`)
}

// PlayableSettings règle le catalogue « lisible uniquement »
// (variables PSE_PLAYABLE_*).
type PlayableSettings struct {
	// Enabled : PSE_PLAYABLE=on. Désactivé par défaut.
	Enabled bool
	// Min : PSE_PLAYABLE_MIN, releases distinctes nécessaires (défaut 2).
	Min int
	// Filter : PSE_PLAYABLE_FILTER, au format de ParseFilter
	// ("lang=fr,multi;min=1080;cached=1"). Vide : toute release compte.
	Filter Filter
	// Pending : PSE_PLAYABLE_PENDING=show garde visibles les titres pas
	// encore indexés (utile pendant la première indexation). Par défaut
	// ils sont masqués.
	Pending bool
	// Single : PSE_PLAYABLE_SINGLE=on affiche aussi les titres qui n'ont
	// qu'une partie des releases demandées (au moins une), marqués comme
	// risqués (risk.go). Sans effet si Min vaut 1.
	Single bool
	// Mark : PSE_PLAYABLE_SINGLE_MARK, où marquer ces titres : "summary"
	// (début du résumé, défaut), "poster" (badge des affiches Ether) ou
	// "both".
	Mark string
	raw  string
}

// LoadPlayable lit la configuration depuis l'environnement.
func LoadPlayable() PlayableSettings {
	p := PlayableSettings{Min: 2}
	switch strings.ToLower(strings.TrimSpace(pseenv.Get("PSE_PLAYABLE"))) {
	case "on", "1", "true", "yes", "oui":
		p.Enabled = true
	}
	if n, err := strconv.Atoi(strings.TrimSpace(pseenv.Get("PSE_PLAYABLE_MIN"))); err == nil && n >= 1 {
		p.Min = n
	}
	p.raw = strings.TrimSpace(pseenv.Get("PSE_PLAYABLE_FILTER"))
	p.Filter = ParseFilter(p.raw)
	switch strings.ToLower(strings.TrimSpace(pseenv.Get("PSE_PLAYABLE_PENDING"))) {
	case "show", "on", "1", "true", "oui":
		p.Pending = true
	}
	switch strings.ToLower(strings.TrimSpace(pseenv.Get("PSE_PLAYABLE_SINGLE"))) {
	case "on", "1", "true", "yes", "oui":
		p.Single = true
	}
	p.Mark = "summary"
	switch m := strings.ToLower(strings.TrimSpace(pseenv.Get("PSE_PLAYABLE_SINGLE_MARK"))); m {
	case "poster", "both":
		p.Mark = m
	}
	return p
}

// ShowMin est le nombre de releases à partir duquel un titre est affiché :
// 1 quand les titres risqués sont acceptés, sinon Min.
func (p PlayableSettings) ShowMin() int {
	if p.Single && p.Min > 1 {
		return 1
	}
	return p.Min
}

// NewPlayable construit des réglages sans passer par l'environnement.
func NewPlayable(min int, filter string, pending bool) PlayableSettings {
	return PlayableSettings{Enabled: true, Min: max(1, min), Filter: ParseFilter(filter), Pending: pending, raw: strings.TrimSpace(filter)}
}

// signature identifie le filtre : les comptes stockés en dépendent, pas
// le seuil Min.
func (p PlayableSettings) signature() string {
	f := p.Filter
	return strings.Join([]string{"v3", strings.Join(f.Languages, ","), strconv.Itoa(f.MinHeight), strconv.Itoa(f.MaxHeight), strings.Join(f.VideoCodecs, ","), strconv.FormatBool(f.CachedOnly)}, "|")
}

var (
	playableOnce sync.Once
	playableCur  atomic.Pointer[PlayableSettings]
)

// Playable renvoie les réglages actifs (lus une fois dans l'environnement).
func Playable() PlayableSettings {
	playableOnce.Do(func() {
		if playableCur.Load() == nil {
			p := LoadPlayable()
			playableCur.Store(&p)
		}
	})
	return *playableCur.Load()
}

// OverridePlayable remplace les réglages actifs (tests) et renvoie la
// fonction qui les rétablit.
func OverridePlayable(p PlayableSettings) (restore func()) {
	Playable()
	prev := playableCur.Load()
	playableCur.Store(&p)
	return func() { playableCur.Store(prev) }
}

// PlayableSQL renvoie la condition SQL qui garde les titres lisibles,
// keyCol désignant la colonne de clé du titre (ex. "t.key"). Vide si la
// fonction est désactivée.
func (p PlayableSettings) PlayableSQL(keyCol string) (string, []any) {
	if !p.Enabled {
		return "", nil
	}
	// The eligible set is independent of the outer catalogue row. SQLite can
	// drive a title-key lookup from this set instead of probing it once for
	// every title in a large catalogue. Keep the exact same language exceptions.
	clause := keyCol + " IN (SELECT pp.title_key FROM pse_playable pp WHERE pp.releases>=? OR (pp.any_lang>=? AND " + LangExemptSQL("pp.title_key") + "))"
	if p.Pending {
		clause += " OR NOT EXISTS (SELECT 1 FROM pse_index_titles pi WHERE pi.title_key=" + keyCol + ")"
	}
	trusted, args := seerrcatalog.TrustedSQL(keyCol)
	return "(" + clause + " OR " + trusted + ")", append([]any{p.ShowMin(), p.ShowMin()}, args...)
}

// LangExemptSQL renvoie la condition SQL « le titre échappe au filtre de
// langue » (voir SetLangExempt).
func LangExemptSQL(keyCol string) string {
	return "EXISTS (SELECT 1 FROM pse_lang_exempt le WHERE le.title_key=" + keyCol + ")"
}

// SetLangExempt remplace la liste des titres qui échappent au filtre de
// langue : leurs releases comptent quelle que soit leur langue (les autres
// critères du filtre restent appliqués).
func (s *Store) SetLangExempt(ctx context.Context, keys []core.RatingKey) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM pse_lang_exempt`); err != nil {
		return err
	}
	seen := map[core.RatingKey]bool{}
	for _, k := range keys {
		if seen[k] {
			continue
		}
		seen[k] = true
		if _, err := tx.ExecContext(ctx, `INSERT INTO pse_lang_exempt(title_key) VALUES(?)`, int64(k)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// RefreshPlayable recalcule le nombre de releases lisibles d'un titre.
// Deux releases de même nom venant de sources différentes comptent une
// seule fois. Les observations expirées et celles d'une source désactivée
// ne comptent pas (freshness.go).
func (s *Store) RefreshPlayable(ctx context.Context, key core.RatingKey, p PlayableSettings) error {
	anyLang := p.Filter
	anyLang.Languages = nil
	rs, err := s.Releases(ctx, key, anyLang)
	if err != nil {
		return err
	}
	names, all := map[string]bool{}, map[string]bool{}
	now := s.now()
	for _, r := range rs {
		if !counts(r, now) {
			continue
		}
		all[strings.ToLower(r.Name)] = true
		if p.Filter.Match(r) {
			names[strings.ToLower(r.Name)] = true
		}
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO pse_playable(title_key,releases,any_lang) VALUES(?,?,?)
ON CONFLICT(title_key) DO UPDATE SET releases=excluded.releases, any_lang=excluded.any_lang`, int64(key), len(names), len(all))
	return err
}

// ForgetPlayable efface la signature : le prochain SyncPlayable recalcule
// tout. Appelé quand la fonction est désactivée, la table cessant alors
// d'être tenue à jour.
func (s *Store) ForgetPlayable(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM pse_state WHERE k='playable_signature'`)
	return err
}

// SyncPlayable recalcule tous les titres si le filtre a changé depuis le
// dernier calcul. Renvoie le nombre de titres recalculés.
func (s *Store) SyncPlayable(ctx context.Context, p PlayableSettings) (int, error) {
	sig := p.signature()
	var cur string
	err := s.db.QueryRowContext(ctx, `SELECT v FROM pse_state WHERE k='playable_signature'`).Scan(&cur)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	if cur == sig {
		return 0, nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT title_key FROM pse_releases`)
	if err != nil {
		return 0, err
	}
	var keys []core.RatingKey
	for rows.Next() {
		var k int64
		if rows.Scan(&k) == nil {
			keys = append(keys, core.RatingKey(k))
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM pse_playable`); err != nil {
		return 0, err
	}
	for _, k := range keys {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		if err := s.RefreshPlayable(ctx, k, p); err != nil {
			return 0, err
		}
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO pse_state(k,v) VALUES('playable_signature',?)
ON CONFLICT(k) DO UPDATE SET v=excluded.v`, sig)
	return len(keys), err
}
