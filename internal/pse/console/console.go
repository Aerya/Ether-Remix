// Package console sert la page « PSE » de la console d'Ether : réglages des
// ajouts du fork, enregistrés en base, et état de chaque fonction. Une valeur
// enregistrée ici l'emporte sur la variable d'environnement du même nom.
// Ajout PSE : voir docs/pse/FORK.fr.md.
package console

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/braedonsaunders/ether/internal/langtag"
	"github.com/braedonsaunders/ether/internal/pse/submaker"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/braedonsaunders/ether/internal/pse/music"
	"github.com/braedonsaunders/ether/internal/pse/pseenv"
	"github.com/braedonsaunders/ether/internal/pse/torrentclient"
	"github.com/braedonsaunders/ether/internal/store"
)

func init() {
	store.Register("pse_settings", 1, `
CREATE TABLE pse_settings(
  k TEXT PRIMARY KEY,
  v TEXT NOT NULL DEFAULT ''
);
`)
}

// LoadOverrides lit les réglages enregistrés et les rend effectifs.
func LoadOverrides(ctx context.Context, db *sql.DB) error {
	values, err := readAll(ctx, db)
	if err != nil {
		return err
	}
	pseenv.Replace(values)
	return nil
}

func readAll(ctx context.Context, db *sql.DB) (map[string]string, error) {
	rows, err := db.QueryContext(ctx, `SELECT k, v FROM pse_settings`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		if _, known := fieldByKey[k]; known {
			out[k] = v
		}
	}
	return out, rows.Err()
}

func writeAll(ctx context.Context, db *sql.DB, values map[string]string) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `DELETE FROM pse_settings`); err != nil {
		return err
	}
	for k, v := range values {
		if _, err := tx.ExecContext(ctx, `INSERT INTO pse_settings(k, v) VALUES(?, ?)`, k, v); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Option est un choix d'une liste.
type Option struct {
	Value string `json:"value"`
	Label Text   `json:"label"`
}

// Deps relie la console au reste de l'application.
type Deps struct {
	DB *sql.DB
	// Apply rend les nouveaux réglages effectifs (après enregistrement).
	Apply func(ctx context.Context)
	// Status renvoie l'état d'une fonction (clé : identifiant de fonction).
	Status func(ctx context.Context) map[string]any
	// Sources liste les indexeurs proposés pour PSE_INDEXER_SOURCES.
	Sources func() []Option
	// TestMusic vérifie qu'une source musicale (clé PSE_MUSIC_ECLIPSE_URL
	// ou PSE_MUSIC_HIFI_URL) répond.
	// Le message décrit le résultat (par exemple l'album lu en test).
	TestMusic func(ctx context.Context, key, value string) (string, error)
}

type fieldView struct {
	Field
	Value   string   `json:"value"`
	Source  string   `json:"source"` // console, env ou ""
	Env     *string  `json:"env,omitempty"`
	Options []Option `json:"options,omitempty"`
}

type featureView struct {
	ID          string         `json:"id"`
	Title       Text           `json:"title"`
	Description Text           `json:"description"`
	Fields      []fieldView    `json:"fields"`
	Status      map[string]any `json:"status,omitempty"`
}

type handler struct {
	d  Deps
	mu sync.Mutex
}

// Handler sert /ether/api/pse/ (déjà authentifié par la console).
func Handler(d Deps) http.Handler {
	h := &handler{d: d}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", h.get)
	mux.HandleFunc("PUT /{$}", h.put)
	mux.HandleFunc("POST /music/test", h.testMusic)
	mux.HandleFunc("POST /seerrcatalog/test", h.testSeerrCatalog)
	return mux
}

func (h *handler) view(ctx context.Context) map[string]any {
	var status map[string]any
	if h.d.Status != nil {
		status = h.d.Status(ctx)
	}
	features := make([]featureView, 0, len(Features))
	for _, f := range Features {
		fv := featureView{ID: f.ID, Title: f.Title, Description: f.Description}
		if s, ok := status[f.ID].(map[string]any); ok {
			fv.Status = s
		}
		for _, fd := range f.Fields {
			v := fieldView{Field: fd, Source: pseenv.Source(fd.Key), Options: fd.Options}
			if val, ok := pseenv.Lookup(fd.Key); ok {
				v.Value = val
			} else {
				v.Value = fd.Default
			}
			if env, ok := pseenv.Env(fd.Key); ok {
				e := display(fd, env)
				v.Env = &e
			}
			v.Value = display(fd, v.Value)
			if fd.Key == "PSE_INDEXER_SOURCES" && h.d.Sources != nil {
				v.Options = h.d.Sources()
			}
			fv.Fields = append(fv.Fields, v)
		}
		features = append(features, fv)
	}
	return map[string]any{"features": features}
}

func (h *handler) get(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, h.view(r.Context()))
}

type putBody struct {
	Values map[string]*string `json:"values"`
}

func (h *handler) put(w http.ResponseWriter, r *http.Request) {
	var body putBody
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "Requête invalide"})
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	values := pseenv.Snapshot()
	errs := map[string]string{}
	for key, raw := range body.Values {
		fd, ok := fieldByKey[key]
		if !ok {
			errs[key] = "Réglage inconnu"
			continue
		}
		if raw == nil {
			delete(values, key)
			continue
		}
		v := strings.TrimSpace(*raw)
		if prev, ok := pseenv.Lookup(key); ok {
			v = restore(fd, v, prev)
		}
		var sources []Option
		if key == "PSE_INDEXER_SOURCES" && h.d.Sources != nil {
			sources = h.d.Sources()
		}
		norm, err := validate(fd, v, sources)
		if err != nil {
			errs[key] = err.Error()
			continue
		}
		values[key] = norm
	}
	effective := func(key string) string {
		if v, ok := values[key]; ok {
			return v
		}
		v, _ := pseenv.Env(key)
		return v
	}
	switch strings.ToLower(effective("PSE_TORRENT")) {
	case "on", "1", "true", "yes", "oui":
		if effective("PSE_TORRENT_URL") == "" {
			errs["PSE_TORRENT_URL"] = "Adresse du client torrent requise"
		}
		for n := 2; n <= torrentclient.MaxClients; n++ {
			p := "PSE_TORRENT" + strconv.Itoa(n)
			switch strings.ToLower(effective(p)) {
			case "on", "1", "true", "yes", "oui":
				if effective(p+"_URL") == "" {
					errs[p+"_URL"] = "Adresse du client torrent requise"
				}
			}
		}
	}
	switch strings.ToLower(effective("PSE_SEERRCATALOG")) {
	case "on", "1", "true", "yes", "oui":
		if effective("PSE_SEERRCATALOG_URL") == "" {
			errs["PSE_SEERRCATALOG_URL"] = "Adresse de SeerrCatalog requise"
		}
		if len(effective("PSE_SEERRCATALOG_TOKEN")) < 32 {
			errs["PSE_SEERRCATALOG_TOKEN"] = "Clé partagée requise (32 caractères minimum)"
		}
	}
	switch strings.ToLower(effective("PSE_SEERRCATALOG_SEARCH")) {
	case "on", "1", "true", "yes", "oui":
		if len(effective("PSE_SEERRCATALOG_SEARCH_TOKEN")) < 32 {
			errs["PSE_SEERRCATALOG_SEARCH_TOKEN"] = "Clé dédiée aux recherches requise (32 caractères minimum)"
		}
	}
	if len(errs) > 0 {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "Certains réglages sont invalides", "fields": errs})
		return
	}
	if err := writeAll(r.Context(), h.d.DB, values); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "Enregistrement impossible"})
		return
	}
	pseenv.Replace(values)
	if h.d.Apply != nil {
		h.d.Apply(r.Context())
	}
	writeJSON(w, http.StatusOK, h.view(r.Context()))
}

func (h *handler) testMusic(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Key string `json:"key"`
		URL string `json:"url"`
	}
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&body)
	key := body.Key
	if key == "" {
		key = "PSE_MUSIC_HIFI_URL"
	}
	if key == "PSE_MUSIC_DMM" || key == "PSE_MUSIC_ALLDEBRID" || key == "PSE_MUSIC_REALDEBRID" || key == "PSE_MUSIC_TORBOX" {
		h.runMusicTest(w, r, key, "", 75*time.Second)
		return
	}
	if key != "PSE_MUSIC_HIFI_URL" && key != "PSE_MUSIC_ECLIPSE_URL" && !music.IsCommunityKey(key) {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "message": "Réglage non testable"})
		return
	}
	fd := fieldByKey[key]
	base := strings.TrimSpace(body.URL)
	if prev, ok := pseenv.Lookup(key); ok {
		if base == "" {
			base = prev
		} else {
			base = restore(fd, base, prev)
		}
	}
	base, err := validate(fd, base, nil)
	if err != nil || base == "" {
		msg := "Adresse invalide : http(s)://… attendu"
		if err != nil {
			msg = "Adresse invalide : " + err.Error()
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "message": msg})
		return
	}
	h.runMusicTest(w, r, key, base, 15*time.Second)
}

func (h *handler) runMusicTest(w http.ResponseWriter, r *http.Request, key, value string, limit time.Duration) {
	if h.d.TestMusic == nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "message": "Test indisponible"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), limit)
	defer cancel()
	msg, err := h.d.TestMusic(ctx, key, value)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "message": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": msg})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// --- validation ---

var (
	countryRe = regexp.MustCompile(`^[A-Z]{2}$`)
	sourceRe  = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`)
)

func splitList(v string) []string {
	var out []string
	seen := map[string]bool{}
	for _, p := range strings.Split(v, ",") {
		p = strings.TrimSpace(p)
		if p != "" && !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	return out
}

func validate(fd Field, v string, sources []Option) (string, error) {
	if fd.Key == "PSE_SUBMAKER_MANIFEST" && v != "" {
		if _, _, err := submaker.Endpoint(v); err != nil {
			return "", err
		}
	}
	if (fd.Key == "PSE_SUBMAKER_SOURCE" || fd.Key == "PSE_SUBMAKER_TARGET") && langtag.Normalize(v) == "" {
		return "", errors.New("code langue invalide")
	}

	if fd.Key == "PSE_SEERRCATALOG_URL" && v != "" {
		u, e := url.Parse(v)
		if e != nil || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return "", errors.New("adresse de base sans identifiants, paramètres ni fragment attendue")
		}
	}
	if (fd.Key == "PSE_SEERRCATALOG_TOKEN" || fd.Key == "PSE_SEERRCATALOG_SEARCH_TOKEN") && v != "" && len(v) < 32 {
		return "", errors.New("clé de 32 caractères minimum")
	}
	if out, ok, err := validateXDCC(fd.Key, v); ok {
		return out, err
	}
	key := fd.Key
	if strings.HasPrefix(key, "PSE_TORRENT2_") || strings.HasPrefix(key, "PSE_TORRENT3_") {
		key = "PSE_TORRENT_" + key[len("PSE_TORRENT2_"):] // mêmes règles que le client principal
	}
	switch key {
	case "PSE_TORRENT_TRACKERS":
		if _, err := torrentclient.ParseTrackerRules(v); err != nil {
			return "", err
		}
		return v, nil
	case "PSE_TORRENT_RATIO":
		r, err := torrentclient.ParseRatio(v)
		if err != nil {
			return "", err
		}
		if r == 0 {
			return "", nil
		}
		return strconv.FormatFloat(r, 'f', -1, 64), nil
	case "PSE_TORRENT_SEED_TIME":
		if v == "" {
			return "", nil
		}
		d, err := time.ParseDuration(v)
		if err != nil || d < time.Minute {
			return "", errors.New("durée attendue, ex. 72h (minimum 1m)")
		}
		return d.String(), nil
	case "PSE_TORRENT_PATH_MAP":
		if _, err := torrentclient.ParsePathMap(v); err != nil {
			return "", err
		}
		return v, nil
	case "PSE_TORRENT_SAVE_PATH":
		if v != "" && !strings.HasPrefix(v, "/") && !(len(v) > 2 && v[1] == ':') {
			return "", errors.New("chemin absolu attendu")
		}
		return v, nil
	case "PSE_TORRENT_CATEGORY":
		if v == "" || len(v) > 64 || strings.ContainsAny(v, "/\\\n\r\t") {
			return "", errors.New("nom de catégorie simple attendu")
		}
		return v, nil
	}
	switch fd.Kind {
	case KindToggle:
		switch strings.ToLower(v) {
		case "on", "1", "true", "yes", "oui":
			return "on", nil
		case "off", "0", "false", "no", "non", "":
			return "off", nil
		}
		return "", errors.New("on ou off attendu")
	case KindNumber:
		n, err := strconv.Atoi(v)
		if err != nil {
			return "", errors.New("nombre entier attendu")
		}
		if fd.Min != nil && n < *fd.Min {
			return "", fmt.Errorf("minimum %d", *fd.Min)
		}
		if fd.Max != nil && n > *fd.Max {
			return "", fmt.Errorf("maximum %d", *fd.Max)
		}
		return strconv.Itoa(n), nil
	case KindDuration:
		d, err := time.ParseDuration(v)
		if err != nil {
			return "", errors.New("durée attendue, ex. 10s ou 1m")
		}
		if d < 2*time.Second {
			return "", errors.New("minimum 2s")
		}
		return d.String(), nil
	case KindSelect:
		for _, o := range fd.Options {
			if strings.EqualFold(o.Value, v) {
				return o.Value, nil
			}
		}
		return "", errors.New("valeur non proposée")
	case KindMulti:
		items := splitList(v)
		opts := fd.Options
		if fd.Key == "PSE_INDEXER_SOURCES" {
			for _, s := range items {
				if !sourceRe.MatchString(s) {
					return "", fmt.Errorf("identifiant invalide : %s", s)
				}
			}
			_ = sources // un indexeur absent de la configuration est conservé (il peut revenir)
			return strings.Join(items, ","), nil
		}
		for i, s := range items {
			ok := false
			for _, o := range opts {
				if strings.EqualFold(o.Value, s) {
					items[i], ok = o.Value, true
					break
				}
			}
			if !ok {
				return "", fmt.Errorf("valeur non proposée : %s", s)
			}
		}
		return strings.Join(items, ","), nil
	case KindCountries:
		items := splitList(strings.ToUpper(v))
		for _, c := range items {
			if !countryRe.MatchString(c) {
				return "", fmt.Errorf("code pays ISO à deux lettres attendu : %s", c)
			}
		}
		return strings.Join(items, ","), nil
	case KindFilter:
		if len(v) > 500 {
			return "", errors.New("filtre trop long")
		}
		return v, nil
	case KindKey:
		if v == "" {
			return "", nil
		}
		if strings.Contains(v, mask) {
			return "", errors.New("ressaisissez la clé complète")
		}
		if len(v) < 16 || len(v) > 128 || strings.IndexFunc(v, func(r rune) bool {
			return !(r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r == '-' || r == '_')
		}) >= 0 {
			return "", errors.New("clé d'API invalide")
		}
		return v, nil
	case KindURL:
		if fd.Key == "PSE_MUSIC_DMM_URL" && v != "" {
			u, err := url.Parse(v)
			if err != nil || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
				return "", errors.New("adresse DMM sans identifiants, paramètres ni fragment attendue")
			}
		}
		if music.IsCommunityKey(fd.Key) && v != "" {
			u, err := url.Parse(v)
			if err != nil || !strings.HasSuffix(u.Path, "/manifest.json") || len(strings.Fields(v)) != 1 {
				return "", errors.New("un lien manifest.json unique est requis")
			}
		}
		if v == "" {
			return "", nil
		}
		if fd.List {
			parts := strings.FieldsFunc(v, func(r rune) bool { return r == ',' || r == ';' || unicode.IsSpace(r) })
			out := make([]string, 0, len(parts))
			for _, p := range parts {
				one, err := validate(Field{Kind: KindURL}, p, nil)
				if err != nil {
					if strings.Contains(p, mask) {
						return "", errors.New("ressaisissez le lien complet")
					}
					return "", err
				}
				if !slices.Contains(out, one) {
					out = append(out, one)
				}
			}
			return strings.Join(out, " "), nil
		}
		if strings.Contains(v, mask) || strings.Contains(v, url.PathEscape(mask)) {
			return "", errors.New("ressaisissez le mot de passe de l'adresse")
		}
		u, err := url.Parse(v)
		if fd.Key == "PSE_SFR_URL" {
			if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") || u.Host == "" {
				return "", errors.New("adresse postgres://… attendue")
			}
			return v, nil
		}
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return "", errors.New("adresse http(s)://… attendue")
		}
		return strings.TrimRight(v, "/"), nil
	}
	return "", errors.New("type de réglage inconnu")
}

// --- masquage des identifiants dans les URL ---

// display masque ce qui peut être secret avant d'envoyer une valeur à la
// page : le mot de passe d'une adresse, ou tout le chemin d'un lien dont le
// jeton fait partie (Secret).
func display(fd Field, v string) string {
	if fd.Kind == KindKey {
		if len(v) <= 4 {
			return v
		}
		return v[:4] + mask
	}
	if fd.Kind != KindURL {
		return v
	}
	if !fd.Secret {
		return maskURL(v)
	}
	parts := strings.Fields(v)
	for i, p := range parts {
		parts[i] = maskSecret(p)
	}
	return strings.Join(parts, " ")
}

// restore remet les parties masquées à partir de la valeur enregistrée.
func restore(fd Field, v, prev string) string {
	if fd.Kind == KindKey {
		if v != "" && v == display(fd, prev) {
			return prev
		}
		return v
	}
	if fd.Kind != KindURL {
		return v
	}
	if !fd.Secret {
		return unmaskURL(v, prev)
	}
	old := strings.Fields(prev)
	parts := strings.FieldsFunc(v, func(r rune) bool { return r == ',' || r == ';' || unicode.IsSpace(r) })
	used := map[int]bool{}
	for i, p := range parts {
		if !strings.Contains(p, mask) && !strings.Contains(p, url.PathEscape(mask)) {
			continue
		}
		for j, o := range old {
			if !used[j] && maskSecret(o) == strings.Replace(p, url.PathEscape(mask), mask, 1) {
				parts[i], used[j] = o, true
				break
			}
		}
	}
	return strings.Join(parts, " ")
}

// maskSecret ne garde que le schéma et l'hôte d'un lien.
func maskSecret(v string) string {
	u, err := url.Parse(v)
	if err != nil || u.Host == "" {
		return mask
	}
	if (u.Path == "" || u.Path == "/") && u.RawQuery == "" && u.User == nil {
		return v
	}
	return u.Scheme + "://" + u.Host + "/" + mask
}

const mask = "••••••"

func maskURL(v string) string {
	u, err := url.Parse(v)
	if err != nil || u.User == nil {
		return v
	}
	if _, has := u.User.Password(); has {
		u.User = url.UserPassword(u.User.Username(), "MASK")
		return strings.Replace(u.String(), "MASK", mask, 1)
	}
	return v
}

// unmaskURL remet le mot de passe enregistré si l'adresse renvoyée par la
// page contient encore le masque et vise le même hôte et le même compte.
func unmaskURL(v, prev string) string {
	if !strings.Contains(v, mask) && !strings.Contains(v, url.PathEscape(mask)) {
		return v
	}
	u, err := url.Parse(strings.Replace(v, mask, "MASK", 1))
	if err != nil || u.User == nil {
		return v
	}
	p, err := url.Parse(prev)
	if err != nil || p.User == nil || p.Host != u.Host || p.User.Username() != u.User.Username() {
		return v
	}
	pw, _ := p.User.Password()
	u.User = url.UserPassword(u.User.Username(), pw)
	return u.String()
}
