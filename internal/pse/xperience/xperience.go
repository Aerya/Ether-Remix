// Package xperience imports selected Xperience catalogues as scheduled collections.
// Private manifest links belong to the installed add-on, never to collection specs.
package xperience

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/extend"
	"github.com/braedonsaunders/ether/internal/pse/jukebox"
	"github.com/braedonsaunders/ether/internal/stremio"
)

const Site = "https://xperience-app.com/"

type Backend interface {
	Preview(context.Context, string) (stremio.Manifest, error)
	Install(context.Context, string, int) (string, stremio.Manifest, error)
}
type Service struct {
	cols    jukebox.Collections
	libs    jukebox.Libraries
	backend Backend
	mu      sync.Mutex
}

func New(cols jukebox.Collections, libs jukebox.Libraries, backend Backend) *Service {
	return &Service{cols: cols, libs: libs, backend: backend}
}

type Choice struct {
	ID     string            `json:"id"`
	Type   string            `json:"type"`
	Name   string            `json:"name"`
	Params map[string]string `json:"params"`
}
type Catalog struct {
	ID       string            `json:"id"`
	Type     string            `json:"type"`
	Name     string            `json:"name"`
	Extras   []stremio.Extra   `json:"extras"`
	Defaults map[string]string `json:"defaults"`
}
type Request struct {
	Manifest               string   `json:"manifest"`
	Catalogs               []Choice `json:"catalogs"`
	Mode                   string   `json:"mode"` // collections, libraries (hidden backing collections), both
	MaxItems               int      `json:"maxItems"`
	ManifestRefreshHours   int      `json:"manifestRefreshHours"`
	CollectionRefreshHours int      `json:"collectionRefreshHours"`
}
type Result struct {
	Collections []string         `json:"collections"`
	Libraries   []core.LibraryID `json:"libraries"`
}

// Normalize only accepts the public Xperience host; errors never contain the link.
func Normalize(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "stremio://") {
		raw = "https://" + strings.TrimPrefix(raw, "stremio://")
	}
	u, err := url.Parse(raw)
	if err != nil || len(raw) > 8192 || u.Scheme != "https" || u.Host != "xperience-app.com" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || !strings.HasSuffix(u.Path, "/manifest.json") || !strings.HasPrefix(u.Path, "/manifest/") {
		return "", fmt.Errorf("collez le lien personnel manifest.json fourni par Xperience")
	}
	return u.String(), nil
}
func catalogs(man stremio.Manifest) []Catalog {
	out := []Catalog{}
	for _, c := range man.Catalogs {
		if c.Type != "movie" && c.Type != "series" {
			continue
		}
		// Search is interactive, not a scheduled selection.
		if _, ok := c.ExtraNamed("search"); ok {
			continue
		}
		item := Catalog{ID: c.ID, Type: c.Type, Name: c.Name, Extras: []stremio.Extra{}, Defaults: map[string]string{}}
		if item.Name == "" {
			item.Name = c.ID
		}
		for _, e := range c.Extras() {
			if e.Name == "skip" {
				continue
			}
			item.Extras = append(item.Extras, e)
			for _, v := range e.Options {
				if v == "None" {
					item.Defaults[e.Name] = v
					break
				}
			}
			if item.Defaults[e.Name] == "" && e.IsRequired && len(e.Options) > 0 {
				item.Defaults[e.Name] = e.Options[0]
			}
		}
		out = append(out, item)
	}
	return out
}
func (s *Service) Preview(ctx context.Context, raw string) ([]Catalog, error) {
	link, err := Normalize(raw)
	if err != nil {
		return nil, err
	}
	man, err := s.backend.Preview(ctx, link)
	if err != nil {
		return nil, fmt.Errorf("manifeste Xperience inaccessible ou invalide ; vérifiez le lien")
	}
	out := catalogs(man)
	if len(out) == 0 {
		return nil, fmt.Errorf("aucun catalogue de films ou séries importable")
	}
	return out, nil
}
func validate(req Request, man stremio.Manifest) error {
	if req.ManifestRefreshHours < 1 || req.ManifestRefreshHours > 168 || req.CollectionRefreshHours < 1 || req.CollectionRefreshHours > 168 {
		return fmt.Errorf("choisissez des intervalles de 1 à 168 heures")
	}
	if len(req.Catalogs) < 1 || len(req.Catalogs) > 30 {
		return fmt.Errorf("choisissez entre 1 et 30 catalogues")
	}
	if req.Mode != "collections" && req.Mode != "libraries" && req.Mode != "both" {
		return fmt.Errorf("mode de création invalide")
	}
	if req.MaxItems < 1 || req.MaxItems > stremio.MaxCatalogItems {
		return fmt.Errorf("choisissez entre 1 et 2000 titres par catalogue")
	}
	for _, c := range req.Catalogs {
		decl, ok := man.CatalogNamed(c.Type, c.ID)
		if !ok || (c.Type != "movie" && c.Type != "series") {
			return fmt.Errorf("catalogue absent du manifeste")
		}
		if _, ok := decl.ExtraNamed("search"); ok {
			return fmt.Errorf("les catalogues de recherche ne sont pas importables")
		}
		if strings.TrimSpace(c.Name) == "" || len(c.Name) > 120 {
			return fmt.Errorf("nom de sélection invalide")
		}
		for k, v := range c.Params {
			if k == "skip" || len(v) > 500 {
				return fmt.Errorf("paramètre de catalogue invalide")
			}
		}
		if err := decl.ValidateExtras(c.Params); err != nil {
			return fmt.Errorf("complétez les paramètres requis avec les valeurs proposées par le catalogue")
		}
	}
	return nil
}
func (s *Service) Apply(ctx context.Context, req Request) (Result, error) {
	if req.ManifestRefreshHours == 0 {
		req.ManifestRefreshHours = 1
	}
	if req.CollectionRefreshHours == 0 {
		req.CollectionRefreshHours = 24
	}
	out := Result{Collections: []string{}, Libraries: []core.LibraryID{}}
	link, err := Normalize(req.Manifest)
	if err != nil {
		return out, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	man, err := s.backend.Preview(ctx, link)
	if err != nil {
		return out, fmt.Errorf("manifeste Xperience inaccessible ou invalide")
	}
	if err = validate(req, man); err != nil {
		return out, err
	}
	id, installed, err := s.backend.Install(ctx, link, req.ManifestRefreshHours)
	if err != nil {
		return out, fmt.Errorf("impossible d’enregistrer la source Xperience")
	}
	if err = validate(req, installed); err != nil {
		return out, err
	}
	specs, err := s.cols.ListSpecs(ctx)
	if err != nil {
		return out, fmt.Errorf("collections indisponibles")
	}
	libs, err := s.libs.List(ctx)
	if err != nil {
		return out, fmt.Errorf("bibliothèques indisponibles")
	}
	seen := map[string]bool{}
	for _, c := range req.Catalogs {
		typ := c.Type
		if typ == "series" {
			typ = "show"
		}
		params := map[string]string{}
		for k, v := range c.Params {
			if v != "" {
				params[k] = v
			}
		}
		src := core.CollectionSource{Kind: extend.PluginID(id, stremio.Catalog{Type: c.Type, ID: c.ID}.Key()), Params: params, MaxItems: req.MaxItems}
		// Stable identity includes catalogue and parameters, but not editable names or limits.
		identity, _ := json.Marshal(struct {
			Kind   string
			Params map[string]string
		}{src.Kind, params})
		tid := fmt.Sprintf("remix-xperience-%x", sha256.Sum256(identity))
		if seen[tid] {
			continue
		}
		seen[tid] = true
		var found *core.CollectionSpec
		for i := range specs {
			sp := &specs[i]
			if sp.OwnerUserID == 0 && sp.TemplateID == tid && sp.MediaType == typ && len(sp.Sources) == 1 && sp.Sources[0].Kind == src.Kind && maps.Equal(sp.Sources[0].Params, params) {
				found = sp
				break
			}
		}
		if found == nil {
			visible := req.Mode != "libraries"
			sp := core.CollectionSpec{Name: strings.TrimSpace(c.Name), Description: "Sélection Xperience", TemplateID: tid, MediaType: typ, Sources: []core.CollectionSource{src}, Match: "all", Rules: []core.CollectionRule{}, Exclude: []core.CollectionRule{}, Sort: "source", Refresh: refreshSchedule(req.CollectionRefreshHours), ReleaseMode: "released", Enabled: true, Artwork: core.CollectionArtwork{Mode: "auto"}, BrowseInApps: visible}
			found, err = s.cols.CreateSpec(ctx, sp)
			if err != nil {
				return out, fmt.Errorf("création de la collection impossible ; réessayez sans dupliquer les éléments déjà créés")
			}
			specs = append(specs, *found)
		}
		if req.Mode != "libraries" {
			if publisher, ok := s.cols.(interface {
				PublishRemixRows(context.Context, core.CollectionSpec) error
			}); ok {
				if err := publisher.PublishRemixRows(ctx, *found); err != nil {
					return out, err
				}
			}
		}
		out.Collections = append(out.Collections, found.ID)
		if req.Mode == "collections" {
			continue
		}
		cid, err := strconv.ParseInt(found.ID, 10, 64)
		if err != nil || cid <= 0 {
			return out, fmt.Errorf("identifiant de collection invalide")
		}
		var lid core.LibraryID
		for _, lib := range libs {
			if lib.Source == core.LibraryCollection && lib.CollectionID == cid && string(lib.MediaType) == typ {
				lid = lib.ID
				break
			}
		}
		if lid == 0 {
			lib, err := s.libs.CreateCollection(ctx, found.Name, cid, core.MediaType(typ), presentations(typ))
			if err != nil {
				return out, fmt.Errorf("création de la bibliothèque impossible ; réessayez sans dupliquer les éléments déjà créés")
			}
			libs = append(libs, lib)
			lid = lib.ID
		}
		out.Libraries = append(out.Libraries, lid)
	}
	return out, nil
}
func presentations(typ string) core.Presentations {
	var p core.Presentations
	mb, st := "movies", "movie"
	if typ == "show" {
		mb, st = "tvshows", "series"
	}
	_ = json.Unmarshal([]byte(fmt.Sprintf(`{"plex":{"base":%q},"mediaBrowser":{"collectionType":%q},"stremio":{"type":%q},"xtream":{"category":%q},"silo":{"type":%q}}`, typ, mb, st, st, mb)), &p)
	return p
}
func (s *Service) Handler() http.Handler {
	mux := http.NewServeMux()
	handle := func(preview bool) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Cache-Control", "no-store")
			reply := func(code int, v any) { w.WriteHeader(code); _ = json.NewEncoder(w).Encode(v) }
			if s.backend == nil || s.cols == nil || s.libs == nil {
				reply(503, map[string]string{"error": "import Xperience indisponible"})
				return
			}
			var req Request
			if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 128<<10)).Decode(&req); err != nil {
				reply(400, map[string]string{"error": "requête invalide"})
				return
			}
			if preview {
				cats, err := s.Preview(r.Context(), req.Manifest)
				if err != nil {
					reply(400, map[string]string{"error": err.Error()})
					return
				}
				reply(200, map[string]any{"catalogs": cats})
				return
			}
			out, err := s.Apply(r.Context(), req)
			if err != nil {
				reply(400, map[string]any{"error": err.Error(), "result": out})
				return
			}
			reply(200, out)
		}
	}
	mux.HandleFunc("POST /preview", handle(true))
	mux.HandleFunc("POST /{$}", handle(false))
	return mux
}

func refreshSchedule(hours int) string {
	if hours == 24 {
		return "daily"
	}
	if hours == 1 {
		return "hourly"
	}
	if hours == 168 {
		return "weekly"
	}
	return fmt.Sprintf("%dh", hours)
}
