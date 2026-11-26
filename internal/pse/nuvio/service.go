package nuvio

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
)

// Catalog convertit les identifiants de Nuvio (IMDb) en clés d'Ether, et
// inversement.
type Catalog interface {
	// KeyFor : film par IMDb ; épisode par IMDb de la série, saison, épisode.
	KeyFor(ctx context.Context, t Target) (core.RatingKey, bool)
	// TargetOf : l'inverse, avec le titre à afficher.
	TargetOf(ctx context.Context, key core.RatingKey) (t Target, title string, ok bool)
}

// Applier écrit une progression ou un vu dans l'état d'Ether.
type Applier interface {
	ApplyOfflineProgress(ctx context.Context, user int64, key core.RatingKey, p frontend.OfflineProgress) (frontend.TimelinePersistenceResult, error)
}

// Summary résume un cycle.
type Summary struct {
	Imported int    `json:"imported"`
	Unknown  int    `json:"unknown"`
	Skipped  int    `json:"skipped"`
	Error    string `json:"error,omitempty"`
}

// Service regroupe la liaison, la synchronisation et l'API d'administration.
type Service struct {
	Store   *Store
	Client  *Client
	Catalog Catalog
	Applier Applier
	Log     *slog.Logger
	Now     func() time.Time

	mu     sync.Mutex
	flows  map[int64]*flow
	cycles sync.Map // int64 -> *sync.Mutex
	queue  chan pushItem
	qOnce  sync.Once
	inMu   sync.Mutex
	in     map[incomingKey]time.Time
}

type flow struct {
	login    TVLogin
	nextPoll time.Time
	busy     bool
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Service) log() *slog.Logger {
	if s.Log != nil {
		return s.Log
	}
	return slog.Default()
}

// ---- connexion TV ---------------------------------------------------------

// LoginView est la partie d'une connexion TV montrée au navigateur.
type LoginView struct {
	Code      string    `json:"code"`
	WebURL    string    `json:"webUrl"`
	ExpiresAt time.Time `json:"expiresAt"`
	Interval  int       `json:"interval"`
}

// Status est l'état d'un spectateur pour la console.
type Status struct {
	Linked   bool       `json:"linked"`
	Link     *Link      `json:"link,omitempty"`
	Profiles []Profile  `json:"profiles,omitempty"`
	Login    *LoginView `json:"login,omitempty"`
	Status   string     `json:"status,omitempty"`
	Error    string     `json:"error,omitempty"`
}

const maxFlows = 128

// Start ouvre une connexion TV pour le spectateur.
func (s *Service) Start(ctx context.Context, user int64) (Status, error) {
	l, err := s.Client.StartTVLogin(ctx, s.now())
	if err != nil {
		return Status{}, err
	}
	now := s.now()
	s.mu.Lock()
	if s.flows == nil {
		s.flows = map[int64]*flow{}
	}
	for id, f := range s.flows {
		if !now.Before(f.login.ExpiresAt) {
			delete(s.flows, id)
		}
	}
	if len(s.flows) >= maxFlows {
		s.mu.Unlock()
		return Status{}, errors.New("nuvio: trop de connexions en cours")
	}
	s.flows[user] = &flow{login: l, nextPoll: now.Add(time.Duration(l.PollInterval) * time.Second)}
	s.mu.Unlock()
	st, err := s.status(ctx, user, false)
	st.Login = viewOf(l)
	st.Status = "pending"
	return st, err
}

func viewOf(l TVLogin) *LoginView {
	return &LoginView{Code: l.Code, WebURL: l.WebURL, ExpiresAt: l.ExpiresAt, Interval: l.PollInterval}
}

// ErrNoLogin : aucune connexion TV en cours.
var ErrNoLogin = errors.New("nuvio: aucune connexion en cours")

// Poll interroge la connexion TV (au rythme demandé par Nuvio) et, une fois
// approuvée, enregistre les jetons.
func (s *Service) Poll(ctx context.Context, user int64) (Status, error) {
	s.mu.Lock()
	f := s.flows[user]
	if f == nil {
		s.mu.Unlock()
		st, err := s.status(ctx, user, true)
		if err == nil && !st.Linked {
			err = ErrNoLogin
		}
		return st, err
	}
	now := s.now()
	if !now.Before(f.login.ExpiresAt) {
		delete(s.flows, user)
		s.mu.Unlock()
		st, err := s.status(ctx, user, true)
		st.Status = "expired"
		return st, err
	}
	if f.busy || now.Before(f.nextPoll) {
		v := viewOf(f.login)
		s.mu.Unlock()
		st, err := s.status(ctx, user, false)
		st.Login, st.Status = v, "pending"
		return st, err
	}
	f.busy = true
	f.nextPoll = now.Add(time.Duration(f.login.PollInterval) * time.Second)
	login := f.login
	s.mu.Unlock()

	status, err := s.Client.PollTVLogin(ctx, login)
	s.mu.Lock()
	f.busy = false
	cur := s.flows[user] == f
	s.mu.Unlock()
	if err != nil {
		return Status{}, err
	}
	if !cur { // annulé (Delete) pendant l'appel
		return s.status(ctx, user, true)
	}
	switch status {
	case "approved":
		toks, err := s.Client.ExchangeTVLogin(ctx, login, s.now())
		if err != nil {
			return Status{}, err
		}
		s.mu.Lock()
		still := s.flows[user] == f
		delete(s.flows, user)
		s.mu.Unlock()
		if !still {
			return s.status(ctx, user, true)
		}
		if err := s.Store.Create(ctx, user, toks); err != nil {
			return Status{}, err
		}
		st, err := s.status(ctx, user, true)
		st.Status = "approved"
		return st, err
	case "expired", "used", "cancelled":
		s.mu.Lock()
		delete(s.flows, user)
		s.mu.Unlock()
		st, err := s.status(ctx, user, true)
		st.Status = status
		return st, err
	}
	st, err := s.status(ctx, user, false)
	st.Login, st.Status = viewOf(login), "pending"
	return st, err
}

// State renvoie l'état du spectateur, profils compris.
func (s *Service) State(ctx context.Context, user int64) (Status, error) {
	st, err := s.status(ctx, user, true)
	s.mu.Lock()
	if f := s.flows[user]; f != nil && s.now().Before(f.login.ExpiresAt) && !st.Linked {
		st.Login, st.Status = viewOf(f.login), "pending"
	}
	s.mu.Unlock()
	return st, err
}

func (s *Service) status(ctx context.Context, user int64, profiles bool) (Status, error) {
	l, ok, err := s.Store.Get(ctx, user)
	if err != nil || !ok {
		return Status{}, err
	}
	st := Status{Linked: true, Link: &l}
	if profiles {
		var ps []Profile
		err := s.call(ctx, user, func(tok string) (e error) { ps, e = s.Client.Profiles(ctx, tok); return })
		if err != nil {
			st.Error = "Profils Nuvio indisponibles"
		}
		st.Profiles = ps
	}
	return st, nil
}

// Choose enregistre le profil et les sens de synchronisation.
func (s *Service) Choose(ctx context.Context, user int64, index int, pull, push bool) (Status, error) {
	l, ok, err := s.Store.Get(ctx, user)
	if err != nil {
		return Status{}, err
	}
	if !ok {
		return Status{}, userError{"Ce spectateur n'est pas relié à Nuvio"}
	}
	var ps []Profile
	if err := s.call(ctx, user, func(tok string) (e error) { ps, e = s.Client.Profiles(ctx, tok); return }); err != nil {
		return Status{}, err
	}
	name, found := "", false
	for _, p := range ps {
		if p.Index == index {
			name, found = p.Name, true
			if name == "" {
				name = fmt.Sprintf("Profil %d", index)
			}
		}
	}
	if !found {
		return Status{}, userError{"Profil Nuvio inconnu"}
	}
	if err := s.Store.SetProfile(ctx, user, index, name, pull, push); err != nil {
		return Status{}, err
	}
	_ = l
	return s.State(ctx, user)
}

// Unlink supprime la liaison, ses jetons et toute connexion en cours.
func (s *Service) Unlink(ctx context.Context, user int64) error {
	s.mu.Lock()
	delete(s.flows, user)
	s.mu.Unlock()
	return s.Store.Delete(ctx, user)
}

// call exécute fn avec un jeton valable ; sur 401, rafraîchit une fois.
func (s *Service) call(ctx context.Context, user int64, fn func(token string) error) error {
	tok, err := s.Store.accessToken(ctx, s.Client, user, "")
	if err != nil {
		return err
	}
	err = fn(tok)
	if errors.Is(err, ErrUnauthorized) {
		if tok, err = s.Store.accessToken(ctx, s.Client, user, tok); err != nil {
			return err
		}
		err = fn(tok)
	}
	return err
}

// ---- installation de l'addon ---------------------------------------------

const addonName = "Ether Remix"

// ValidateAddonURL accepte un lien de manifeste Stremio en https.
func ValidateAddonURL(raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || !strings.HasSuffix(u.Path, "/manifest.json") {
		return nil, userError{"Lien de manifeste Stremio https://…/manifest.json attendu"}
	}
	return u, nil
}

// InstallAddon installe (ou met à jour) l'addon Ether dans le profil Nuvio
// relié, repéré par l'hôte du serveur ; les autres addons restent tels quels.
func (s *Service) InstallAddon(ctx context.Context, user int64, manifest string) error {
	u, err := ValidateAddonURL(manifest)
	if err != nil {
		return err
	}
	l, ok, err := s.Store.Get(ctx, user)
	if err != nil {
		return err
	}
	if !ok || !l.Chosen {
		return userError{"Choisissez d'abord un profil Nuvio"}
	}
	return s.call(ctx, user, func(tok string) error {
		list, err := s.Client.Addons(ctx, tok, l.ProfileIndex)
		if err != nil {
			return err
		}
		out, replaced, order := make([]map[string]any, 0, len(list)+1), false, -1
		for _, a := range list {
			if n, ok := a["sort_order"].(float64); ok && int(n) > order {
				order = int(n)
			}
			if au, _ := a["url"].(string); sameHost(au, u) {
				if replaced {
					continue // un seul addon Ether par profil
				}
				a["url"], a["name"], a["enabled"] = u.String(), addonName, true
				replaced = true
			}
			out = append(out, a)
		}
		if !replaced {
			out = append(out, map[string]any{"url": u.String(), "name": addonName, "enabled": true, "sort_order": order + 1})
		}
		return s.Client.PushAddons(ctx, tok, l.ProfileIndex, out)
	})
}

func sameHost(raw string, u *url.URL) bool {
	p, err := url.Parse(raw)
	return err == nil && strings.EqualFold(p.Host, u.Host)
}
