// Package latestfilter décrit le réglage PSE_JELLYFIN_LATEST_FILTERS : des
// règles d'exclusion (même modèle que « Modifier la collection ») appliquées
// aux derniers ajouts Jellyfin des bibliothèques Films et Séries. Ajout PSE :
// voir docs/pse/FORK.fr.md.
package latestfilter

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/libraryfilter"
)

// Key est le réglage console qui contient le JSON des règles.
const Key = "PSE_JELLYFIN_LATEST_FILTERS"

// MaxSize borne le JSON enregistré.
const MaxSize = 32 << 10

// Settings regroupe les règles d'exclusion de chaque type. Seul Exclude de
// chaque TabFilter est utilisé : « retirer les titres qui correspondent à au
// moins une de ces règles ».
type Settings struct {
	Movie core.TabFilter `json:"movie"`
	Show  core.TabFilter `json:"show"`
}

// Compiled contient les filtres prêts à l'emploi ; un champ nil n'exclut rien.
type Compiled struct {
	Movie, Show *libraryfilter.Filter
}

// Empty indique qu'aucune règle n'est définie.
func (c *Compiled) Empty() bool { return c == nil || (c.Movie == nil && c.Show == nil) }

func compile(f core.TabFilter, mt core.MediaType, label string) (*libraryfilter.Filter, error) {
	if len(f.Rules) > 0 || len(f.Groups) > 0 {
		return nil, fmt.Errorf("%s : seules les règles d'exclusion sont acceptées", label)
	}
	if len(f.Exclude) == 0 {
		return nil, nil
	}
	policy, fallback := f.ToPolicy(mt)
	flt, err := libraryfilter.CompilePolicy(policy, fallback)
	if err != nil {
		return nil, fmt.Errorf("%s : %w", label, err)
	}
	return flt, nil
}

func decode(raw string) (Settings, error) {
	var s Settings
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return s, nil
	}
	if len(raw) > MaxSize {
		return s, errors.New("règles trop volumineuses")
	}
	dec := json.NewDecoder(bytes.NewReader([]byte(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&s); err != nil {
		return s, fmt.Errorf("règles illisibles : %w", err)
	}
	return s, nil
}

// Parse lit et compile le JSON enregistré. Vide : aucune règle.
func Parse(raw string) (*Compiled, error) {
	s, err := decode(raw)
	if err != nil {
		return nil, err
	}
	m, err := compile(s.Movie, core.TypeMovie, "Films")
	if err != nil {
		return nil, err
	}
	sh, err := compile(s.Show, core.TypeShow, "Séries")
	if err != nil {
		return nil, err
	}
	return &Compiled{Movie: m, Show: sh}, nil
}

func exclusionOnly(f core.TabFilter) core.TabFilter {
	return core.TabFilter{Exclude: f.Exclude}
}

// Normalize valide le JSON et renvoie sa forme canonique (« » si aucune
// règle), prête à être enregistrée.
func Normalize(raw string) (string, error) {
	if _, err := Parse(raw); err != nil {
		return "", err
	}
	s, _ := decode(raw)
	s = Settings{Movie: exclusionOnly(s.Movie), Show: exclusionOnly(s.Show)}
	if s.Movie.IsEmpty() && s.Show.IsEmpty() {
		return "", nil
	}
	out, err := json.Marshal(s)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func rule(field string, values ...string) core.CollectionRule {
	v, _ := json.Marshal(values)
	return core.CollectionRule{Field: field, Op: "in", Value: v}
}

// Defaults est le modèle proposé par « Appliquer le modèle par défaut » :
// animation, documentaires, émissions, Inde et langues indiennes. Il n'est
// jamais appliqué automatiquement.
func Defaults() Settings {
	f := core.TabFilter{Exclude: []core.CollectionRule{
		rule("genre", "Animation", "Documentaire", "Documentary", "News", "Talk", "Reality"),
		rule("country", "IN", "India"),
		rule("language", "hi", "ta", "te", "ml", "kn", "bn", "mr", "pa"),
	}}
	return Settings{Movie: f, Show: *f.Clone()}
}
