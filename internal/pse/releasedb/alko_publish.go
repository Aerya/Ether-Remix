package releasedb

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/pse/pseenv"
	"github.com/braedonsaunders/ether/internal/releaseparse"
)

// Publication des fichiers de la source « Alkodiques (API StreamFusion) »
// (internal/pse/alko) dans pse_releases : seulement les titres du catalogue,
// tous les épisodes. Le miroir complet reste dans pse_alko_files.

const (
	// AlkoSourceID identifie les releases de la source Alkodiques.
	AlkoSourceID = "remix-alko"
	// AlkoIndexerName est le nom affiché de la source.
	AlkoIndexerName = "Alkodiques (API StreamFusion)"
)

// AlkoDefaultURL est l'adresse interne conseillée par SFR (page /admin/alko-lecture).
const AlkoDefaultURL = "http://stream-fusion:8080"

// AlkoValidateURL vérifie l'adresse de l'API : http ou https, un hôte, aucun
// identifiant, paramètre ni fragment. L'hôte peut être privé (réseau Docker).
func AlkoValidateURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" ||
		u.User != nil || u.RawQuery != "" || u.Fragment != "" || strings.ContainsAny(raw, " \r\n\t") {
		return "", errors.New("adresse http(s)://hôte[:port] attendue, sans identifiants, paramètres ni fragment")
	}
	return strings.TrimRight(raw, "/"), nil
}

// AlkoAPIEnabled est l'unique définition de « source active » : activée
// (PSE_ALKO), adresse valide (celle par défaut si vide) et clé renseignée.
func AlkoAPIEnabled() bool {
	if !isOn("PSE_ALKO") || strings.TrimSpace(pseenv.Get("PSE_ALKO_KEY")) == "" {
		return false
	}
	raw := strings.TrimSpace(pseenv.Get("PSE_ALKO_URL"))
	if raw == "" {
		raw = AlkoDefaultURL
	}
	_, err := AlkoValidateURL(raw)
	return err == nil
}

// alkoMirrorReady : le miroir local a connu au moins une lecture complète
// réussie. Tant que ce n'est pas le cas (SFR trop ancien, clé fausse, première
// synchronisation en cours), le chemin PostgreSQL des Alkodiques continue.
var alkoMirrorReady atomic.Bool

// SetAlkoMirrorReady est appelé par internal/pse/alko.
func SetAlkoMirrorReady(ready bool) { alkoMirrorReady.Store(ready) }

// AlkoPostgresIgnored : le chemin PostgreSQL des Alkodiques est coupé, la
// source API est active et son miroir rempli.
func AlkoPostgresIgnored() bool { return AlkoAPIEnabled() && alkoMirrorReady.Load() }

// AlkoFile est un fichier vérifié du miroir. URL est privé : jamais journalisé.
type AlkoFile struct {
	ID, Kind, Name, URL string
	Size, TMDB          int64
	Season, Episode     int
}

// États de publication d'un fichier.
const (
	AlkoPublished = 1
	AlkoSkipped   = 2
)

// AlkoState est l'issue de la publication d'un fichier.
type AlkoState struct {
	State     int
	ReleaseID string
	TitleKey  core.RatingKey
}

// AlkoResult : l'état des fichiers jugés (absents : titre pas encore au
// catalogue) et les titres à recalculer.
type AlkoResult struct {
	States  map[string]AlkoState
	Touched []core.RatingKey
}

// AlkoGone décrit une publication à retirer.
type AlkoGone struct {
	PlaybackID, ReleaseID string
	TitleKey              core.RatingKey
}

// AlkoLanguage : audio français, MULTi ou VOSTFR, ou sous-titres français.
func AlkoLanguage(name string) bool { return sfrAlkoLanguage(name) }

// AlkoRelease construit la release d'un fichier vérifié : ID et GUID
// « sfr-alko:<playback_id> » (core.IsVerifiedFile), lien source, aucune empreinte
// de torrent ni lien magnet. false : lien invalide, taille inconnue ou, si
// langFR, ni français ni MULTi ni VOSTFR.
func AlkoRelease(f AlkoFile, langFR bool) (core.Release, bool) {
	if f.ID == "" || f.Size <= 0 || !core.ValidAllDebridFileURL(f.URL) {
		return core.Release{}, false
	}
	info := releaseparse.Parse(f.Name)
	if langFR && !alkoLanguageInfo(f.Name, info) {
		return core.Release{}, false
	}
	id := core.VerifiedFileIDPrefix + f.ID
	return core.Release{ID: id, GUID: id, Name: f.Name, Size: f.Size, Protocol: core.ProtocolHTTP, Indexer: AlkoIndexerName, IndexerID: AlkoSourceID,
		TitleIDs: core.IDs{TMDB: f.TMDB}, Info: info, StreamURL: f.URL, StreamDebrid: "alldebrid", StreamOnDemand: true,
		DebridCached: []string{"alldebrid"}}, true
}

// AlkoPublisher écrit les fichiers vérifiés dans pse_releases et la
// bibliothèque de références.
type AlkoPublisher struct {
	DB       *sql.DB
	Remember func(context.Context, core.RatingKey, []core.Release)
	Now      func() time.Time
}

func (p *AlkoPublisher) now() time.Time {
	if p.Now != nil {
		return p.Now()
	}
	return time.Now()
}

// Publish publie ceux des fichiers dont le titre est au catalogue. Un fichier
// écarté (langue, association, numéro hors limites) est rendu à l'état
// AlkoSkipped. Le recalcul des titres touchés est laissé à l'appelant (Refresh).
func (p *AlkoPublisher) Publish(ctx context.Context, files []AlkoFile, langFR bool) (AlkoResult, error) {
	res := AlkoResult{States: map[string]AlkoState{}}
	st := NewStore(p.DB)
	ids := map[core.MediaType][]int64{}
	seen := map[core.MediaType]map[int64]bool{core.TypeMovie: {}, core.TypeShow: {}}
	for _, f := range files {
		typ := alkoType(f.Kind)
		if typ == "" || seen[typ][f.TMDB] {
			continue
		}
		seen[typ][f.TMDB] = true
		ids[typ] = append(ids[typ], f.TMDB)
	}
	keys := map[core.MediaType]map[int64]core.RatingKey{}
	var lookup []core.RatingKey
	for typ, list := range ids {
		m := map[int64]core.RatingKey{}
		for i := 0; i < len(list); i += 500 {
			part, err := st.sfrKeys(ctx, typ, list[i:min(i+500, len(list))])
			if err != nil {
				return res, err
			}
			for tmdb, key := range part {
				m[tmdb] = key
				lookup = append(lookup, key)
			}
		}
		keys[typ] = m
	}
	if len(lookup) == 0 {
		return res, nil
	}
	requests, err := AssociationRequestsFor(ctx, p.DB, lookup)
	if err != nil {
		return res, err
	}
	var batch []Release
	references := map[core.RatingKey][]core.Release{}
	touched := map[core.RatingKey]bool{}
	for _, f := range files {
		typ := alkoType(f.Kind)
		key, ok := keys[typ][f.TMDB]
		if typ == "" || !ok {
			continue // le titre n'est pas (encore) au catalogue
		}
		season, episode := 0, 0
		if typ == core.TypeShow {
			season, episode = f.Season, f.Episode
		}
		skip := AlkoState{State: AlkoSkipped}
		rel, ok := AlkoRelease(f, langFR)
		// EpisodeKey tient la saison sur 3 chiffres et l'épisode sur 4.
		if !ok || season < 0 || season > 999 || episode < 0 || episode > 9999 || (typ == core.TypeShow && episode == 0) {
			res.States[f.ID] = skip
			continue
		}
		request, known := AssociationRequest(requests, key, season, episode)
		if !known || releaseparse.AssociationReason(request, rel) != "" || releaseparse.ReferenceReason(rel) != "" {
			res.States[f.ID] = skip
			continue
		}
		saved := FromCore(core.Title{Key: key, Type: typ}, season, episode, AlkoSourceID, rel, []string{"alldebrid"})
		batch = append(batch, saved)
		res.States[f.ID] = AlkoState{State: AlkoPublished, ReleaseID: saved.ID, TitleKey: key}
		referenceKey := key
		if typ == core.TypeShow {
			referenceKey = core.EpisodeKey(key.TMDB(), season, episode)
		}
		references[referenceKey] = append(references[referenceKey], rel)
		touched[key] = true
	}
	if err := publishBatch(ctx, st, p.Remember, p.now(), batch, references, nil); err != nil {
		return res, err
	}
	for k := range touched {
		res.Touched = append(res.Touched, k)
	}
	return res, nil
}

func alkoType(kind string) core.MediaType {
	switch kind {
	case "movie":
		return core.TypeMovie
	case "episode":
		return core.TypeShow
	}
	return ""
}

// Refresh recalcule les titres touchés (catalogue « lisible uniquement »).
func (p *AlkoPublisher) Refresh(ctx context.Context, keys []core.RatingKey) error {
	return refreshPlayable(ctx, NewStore(p.DB), keys)
}

// Forget retire des publications (fichier supprimé, changé de titre ou écarté)
// des releases et de la bibliothèque de références.
func (p *AlkoPublisher) Forget(ctx context.Context, gone []AlkoGone) error {
	if len(gone) == 0 {
		return nil
	}
	keys := map[core.RatingKey]bool{}
	for i := 0; i < len(gone); i += alkoChunk {
		part := gone[i:min(i+alkoChunk, len(gone))]
		var rels, refs []any
		for _, g := range part {
			if g.ReleaseID != "" {
				rels = append(rels, g.ReleaseID)
				if g.TitleKey != 0 {
					keys[g.TitleKey] = true
				}
			}
			refs = append(refs, core.VerifiedFileIDPrefix+g.PlaybackID)
		}
		if len(rels) > 0 {
			args := append([]any{AlkoSourceID}, rels...)
			if _, err := p.DB.ExecContext(ctx, `DELETE FROM pse_releases WHERE source_id=? AND id IN (`+qmarks(len(rels))+`)`, args...); err != nil {
				return err
			}
		}
		args := append([]any{AlkoSourceID}, refs...)
		if _, err := p.DB.ExecContext(ctx, `DELETE FROM pse_release_library WHERE source_id=? AND release_id IN (`+qmarks(len(refs))+`)`, args...); err != nil {
			return err
		}
	}
	list := make([]core.RatingKey, 0, len(keys))
	for k := range keys {
		list = append(list, k)
	}
	return refreshPlayable(ctx, NewStore(p.DB), list)
}

// ForgetAll retire toutes les publications de la source (autre serveur).
func (p *AlkoPublisher) ForgetAll(ctx context.Context) error {
	rows, err := p.DB.QueryContext(ctx, `SELECT DISTINCT title_key FROM pse_releases WHERE source_id=?`, AlkoSourceID)
	if err != nil {
		return err
	}
	var keys []core.RatingKey
	for rows.Next() {
		var k int64
		if err := rows.Scan(&k); err != nil {
			rows.Close()
			return err
		}
		keys = append(keys, core.RatingKey(k))
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if _, err := p.DB.ExecContext(ctx, `DELETE FROM pse_releases WHERE source_id=?`, AlkoSourceID); err != nil {
		return err
	}
	if _, err := p.DB.ExecContext(ctx, `DELETE FROM pse_release_library WHERE source_id=?`, AlkoSourceID); err != nil {
		return err
	}
	return refreshPlayable(ctx, NewStore(p.DB), keys)
}

// alkoChunk borne le nombre de lignes d'une même instruction d'écriture : un
// écrivain SQLite tient la base pour tous les autres tant qu'elle dure.
const alkoChunk = 100

// Touch garde les observations de la source « vues » : un fichier toujours
// présent ne doit pas expirer (MaxAge) entre deux synchronisations. Par
// tranches de rowid : une seule instruction sur des centaines de milliers de
// lignes tiendrait l'écrivain plusieurs secondes.
func (p *AlkoPublisher) Touch(ctx context.Context) error {
	now := p.now().Unix()
	after := int64(0)
	for {
		var last sql.NullInt64
		if err := p.DB.QueryRowContext(ctx, `SELECT MAX(rowid) FROM (SELECT rowid FROM pse_releases WHERE source_id=? AND rowid>? ORDER BY rowid LIMIT ?)`,
			AlkoSourceID, after, 5*alkoChunk).Scan(&last); err != nil {
			return err
		}
		if !last.Valid {
			return nil
		}
		if _, err := p.DB.ExecContext(ctx, `UPDATE pse_releases SET last_seen=? WHERE source_id=? AND rowid>? AND rowid<=?`, now, AlkoSourceID, after, last.Int64); err != nil {
			return err
		}
		after = last.Int64
	}
}

// column lit la première colonne d'une requête (lecture seule).
func (p *AlkoPublisher) column(ctx context.Context, query string, args ...any) ([]any, error) {
	rows, err := p.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []any
	for rows.Next() {
		var v any
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func qmarks(n int) string { return "?" + strings.Repeat(",?", n-1) }

// Identifiants des anciennes lignes des Alkodiques lues par PostgreSQL : le nom
// de source des releases (sfr-db) et la source de leurs références chiffrées
// (ligne d'indexeur remix-sfr-search, ID « sfr-alko:… »).
const (
	alkoPostgresName    = "StreamFusion Reborn (Alkodiques)"
	alkoPostgresLibrary = "remix-sfr-search"
)

// DropPostgresDuplicates supprime les anciennes lignes sfr-db des Alkodiques et
// leurs références, devenues des doublons du miroir. Les torrents SFR ne sont
// pas touchés. Renvoie le nombre de releases retirées.
func (p *AlkoPublisher) DropPostgresDuplicates(ctx context.Context) (int, error) {
	rows, err := p.DB.QueryContext(ctx, `SELECT DISTINCT title_key FROM pse_releases WHERE source_id=? AND source_name=?`, SFRSourceID, alkoPostgresName)
	if err != nil {
		return 0, err
	}
	var keys []core.RatingKey
	for rows.Next() {
		var k int64
		if err := rows.Scan(&k); err != nil {
			rows.Close()
			return 0, err
		}
		keys = append(keys, core.RatingKey(k))
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return 0, err
	}
	// Lecture d'abord (sans écrivain), puis suppression par tranches de clés :
	// un DELETE filtré sur le nom parcourt toute la table pendant qu'il tient
	// l'écrivain.
	ids, err := p.column(ctx, `SELECT id FROM pse_releases WHERE source_id=? AND source_name=?`, SFRSourceID, alkoPostgresName)
	if err != nil {
		return 0, err
	}
	n := 0
	for part := range slices.Chunk(ids, alkoChunk) {
		args := append([]any{SFRSourceID, alkoPostgresName}, part...)
		res, err := p.DB.ExecContext(ctx, `DELETE FROM pse_releases WHERE source_id=? AND source_name=? AND id IN (`+qmarks(len(part))+`)`, args...)
		if err != nil {
			return n, err
		}
		c, _ := res.RowsAffected()
		n += int(c)
	}
	refs, err := p.column(ctx, `SELECT rowid FROM pse_release_library WHERE source_id=? AND release_id LIKE ?`, alkoPostgresLibrary, core.VerifiedFileIDPrefix+"%")
	if err != nil {
		return n, err
	}
	for part := range slices.Chunk(refs, alkoChunk) {
		// Condition revérifiée : un rowid libéré entre la lecture et la
		// suppression peut avoir été repris par la référence d'un autre titre.
		args := append([]any{alkoPostgresLibrary, core.VerifiedFileIDPrefix + "%"}, part...)
		if _, err := p.DB.ExecContext(ctx, `DELETE FROM pse_release_library WHERE source_id=? AND release_id LIKE ? AND rowid IN (`+qmarks(len(part))+`)`, args...); err != nil {
			return n, err
		}
	}
	return n, refreshPlayable(ctx, NewStore(p.DB), keys)
}
