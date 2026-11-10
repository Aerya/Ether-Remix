// Package quality applies Remix's optional release gates to indexing and playback.
package quality

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/releasepolicy"
	"github.com/braedonsaunders/ether/internal/store"
)

func init() {
	store.Register("remix_quality", 1, `CREATE TABLE remix_quality_config(id INTEGER PRIMARY KEY CHECK(id=1), value TEXT NOT NULL);`)
}

type Profile struct {
	ID      string             `json:"id"`
	Name    string             `json:"name"`
	Scope   string             `json:"scope"` // global, movie, show, library, collection
	Target  string             `json:"target,omitempty"`
	Replace bool               `json:"replace,omitempty"`
	Rules   []core.ReleaseRule `json:"rules"`
}
type Config struct {
	Mode string `json:"mode,omitempty"` // simple or profiles; empty migrates existing profiles

	Profiles []Profile `json:"profiles"`
}
type compiled struct {
	profile Profile
	policy  *releasepolicy.Policy
}
type snapshot struct {
	signature string
	config    Config
	profiles  []compiled
	members   map[string]map[core.RatingKey]bool
}
type Service struct {
	db      *sql.DB
	mu      sync.Mutex
	current atomic.Pointer[snapshot]
}

var services sync.Map // keyed by the owning DB; never share one application's rules with another
func New(ctx context.Context, db *sql.DB) (*Service, error) {
	s := &Service{db: db}
	c := Config{Profiles: []Profile{}}
	var raw string
	err := db.QueryRowContext(ctx, "SELECT value FROM remix_quality_config WHERE id=1").Scan(&raw)
	if err != nil && err != sql.ErrNoRows {
		return nil, err
	}
	if raw != "" {
		if err = json.Unmarshal([]byte(raw), &c); err != nil {
			return nil, err
		}
	}
	ps, err := compile(c)
	if err != nil {
		return nil, err
	}
	s.publish(&snapshot{config: c, profiles: ps, members: map[string]map[core.RatingKey]bool{}})
	services.Store(db, s)
	return s, nil
}
func (s *Service) Close() { services.CompareAndDelete(s.db, s) }
func compile(c Config) ([]compiled, error) {
	if c.Mode != "" && c.Mode != "simple" && c.Mode != "profiles" {
		return nil, fmt.Errorf("mode de filtrage inconnu")
	}
	if len(c.Profiles) > 64 {
		return nil, fmt.Errorf("64 profils au maximum")
	}
	seen := map[string]bool{}
	out := []compiled{}
	for _, p := range c.Profiles {
		if p.ID == "" || len(p.ID) > 100 || seen[p.ID] || len(p.Name) > 160 || p.Name == "" {
			return nil, fmt.Errorf("identifiant ou nom de profil invalide")
		}
		seen[p.ID] = true
		switch p.Scope {
		case "global", "movie", "show":
		case "library", "collection":
			if p.Target == "" {
				return nil, fmt.Errorf("sélectionnez une bibliothèque ou collection")
			}
		default:
			return nil, fmt.Errorf("portée inconnue")
		}
		for _, r := range p.Rules {
			if r.Action != "require" && r.Action != "avoid" {
				return nil, fmt.Errorf("seules les règles imposer/exclure sont acceptées")
			}
			if len(r.Fallback) > 0 {
				return nil, fmt.Errorf("les replis ne sont pas acceptés dans un filtre strict")
			}
			for _, cs := range [][]core.ReleaseCondition{r.Conditions, r.Unless} {
				for _, cond := range cs {
					if _, ok := lookupField(cond.Field); !ok || cond.Field == "audio_matches_original" {
						return nil, fmt.Errorf("champ non pris en charge : %s", cond.Field)
					}
				}
			}
		}
		pol, err := compilePolicy(p.Rules)
		if err != nil {
			return nil, err
		}
		out = append(out, compiled{p, pol})
	}
	return out, nil
}
func (s *Service) Config() Config {
	b, _ := json.Marshal(s.current.Load().config)
	var c Config
	_ = json.Unmarshal(b, &c)
	return c
}
func (s *Service) Save(ctx context.Context, c Config) error {
	if c.Profiles == nil {
		c.Profiles = []Profile{}
	}
	b, err := json.Marshal(c)
	if err != nil {
		return err
	}
	// Own the slices: callers cannot mutate a published policy.
	if err = json.Unmarshal(b, &c); err != nil {
		return err
	}
	ps, err := compile(c)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err = s.db.ExecContext(ctx, "INSERT INTO remix_quality_config(id,value) VALUES(1,?) ON CONFLICT(id) DO UPDATE SET value=excluded.value", string(b)); err != nil {
		return err
	}
	s.publish(&snapshot{config: c, profiles: ps, members: s.current.Load().members})
	return nil
}

// SetMembers publishes immutable collection/library membership, refreshed when collections change.
func (s *Service) SetMembers(m map[string][]core.RatingKey) {
	members := map[string]map[core.RatingKey]bool{}
	for id, keys := range m {
		set := map[core.RatingKey]bool{}
		for _, k := range keys {
			set[k] = true
		}
		members[id] = set
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	old := s.current.Load()
	s.publish(&snapshot{config: old.config, profiles: old.profiles, members: members})
}
func (s *Service) Allow(key core.RatingKey, typ core.MediaType, rel core.Release, original ...string) bool {
	snap := s.current.Load()
	if !active(snap.config) {
		return true
	}
	var globals []compiled
	var chosen *compiled
	// More specific scopes win. Order resolves ties explicitly (first wins).
	priority := 0
	for i := range snap.profiles {
		p := &snap.profiles[i]
		rank := 0
		switch p.profile.Scope {
		case "global":
			globals = append(globals, *p)
		case "movie":
			if typ == core.TypeMovie {
				rank = 1
			}
		case "show":
			if typ == core.TypeShow || typ == core.TypeEpisode {
				rank = 1
			}
		case "library":
			if snap.members["library:"+p.profile.Target][key] {
				rank = 2
			}
		case "collection":
			if snap.members["collection:"+p.profile.Target][key] {
				rank = 3
			}
		}
		if rank > priority {
			priority = rank
			chosen = p
		}
	}
	if chosen == nil || !chosen.profile.Replace {
		for _, p := range globals {
			if evaluate(p.policy, rel, original...).Rejected {
				return false
			}
		}
	}
	return chosen == nil || !evaluate(chosen.policy, rel, original...).Rejected
}
func Allow(db *sql.DB, key core.RatingKey, typ core.MediaType, rel core.Release, original ...string) bool {
	if v, ok := services.Load(db); ok {
		return v.(*Service).Allow(key, typ, rel, original...)
	}
	return true
}
func Signature(db *sql.DB) string {
	if v, ok := services.Load(db); ok {
		return v.(*Service).current.Load().signature
	}
	return ""
}

func (s *Service) publish(snap *snapshot) {
	b, _ := json.Marshal(struct {
		Config  Config
		Members map[string]map[core.RatingKey]bool
	}{snap.config, snap.members})
	snap.signature = fmt.Sprintf("%x", sha256.Sum256(b))
	s.current.Store(snap)
}

// A missing fact fails a requirement, but is not evidence for an exclusion.
func evaluate(p *releasepolicy.Policy, r core.Release, original ...string) releasepolicy.Result {
	lang := ""
	if len(original) > 0 {
		lang = original[0]
	}
	out := p.Evaluate(r, releasepolicy.Context{ExternalMatch: func(r core.Release, c core.ReleaseCondition) (bool, bool) { return matchFact(r, c, lang) }})
	out.Rejected = false
	for i, e := range out.Explanations {
		if e.Action == "avoid" && e.State == releasepolicy.Unknown {
			out.Explanations[i].Outcome = "skip"
			continue
		}
		if e.Outcome == "exclude" || e.Outcome == "unknown" {
			out.Rejected = true
		}
	}
	return out
}

func active(c Config) bool { return c.Mode == "profiles" || (c.Mode == "" && len(c.Profiles) > 0) }
func Active(db *sql.DB) bool {
	if v, ok := services.Load(db); ok {
		return active(v.(*Service).current.Load().config)
	}
	return false
}
