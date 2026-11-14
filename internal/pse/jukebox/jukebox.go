// Package jukebox provides the optional French quick setup using JukeBoX's public MDBList lists.
package jukebox

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"golang.org/x/net/html"
)

const Profile = "https://mdblist.com/lists/jukebox8786/"

var slugRE = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

type List struct {
	Slug string `json:"slug"`
	Name string `json:"name"`
	Type string `json:"type"`
}

var starters = []List{
	{"les-plus-pirates-films", "Films · JukeBox", "movie"},
	{"les-plus-populaires-de-la-semaine-series-tv", "Séries · JukeBox", "show"},
	{"les-plus-populaires-de-la-semaine-anime", "Animes · JukeBox", "mixed"},
}

type Collections interface {
	ListSpecs(context.Context) ([]core.CollectionSpec, error)
	CreateSpec(context.Context, core.CollectionSpec) (*core.CollectionSpec, error)
}
type Libraries interface {
	List(context.Context) ([]core.Library, error)
	CreateCollection(context.Context, string, int64, core.MediaType, core.Presentations) (core.Library, error)
}
type Service struct {
	cols   Collections
	libs   Libraries
	client *http.Client
	// Serialize imports (including retries from another console) without holding a DB transaction during builds.
	mu        sync.Mutex
	catalogMu sync.Mutex
	catalog   []List
	fetched   time.Time
}

func New(cols Collections, libs Libraries) *Service {
	return &Service{cols: cols, libs: libs, client: &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(r *http.Request, via []*http.Request) error {
		if r.URL.Scheme != "https" || r.URL.Host != "mdblist.com" || len(via) > 3 {
			return fmt.Errorf("redirection MDBList refusée")
		}
		return nil
	}}}
}

type Request struct {
	Lists     []List `json:"lists"`
	Libraries bool   `json:"libraries"`
}
type Result struct {
	Collections []string         `json:"collections"`
	Libraries   []core.LibraryID `json:"libraries"`
}

// Apply reuses only the same source and media type; a retry resumes after a partial failure.
// Existing user edits (names, targets, language and visibility) are never overwritten.
func (s *Service) Apply(ctx context.Context, req Request) (Result, error) {
	out := Result{Collections: []string{}, Libraries: []core.LibraryID{}}
	if len(req.Lists) == 0 || len(req.Lists) > 30 {
		return out, fmt.Errorf("choisissez entre 1 et 30 listes")
	}
	for _, l := range req.Lists {
		if !slugRE.MatchString(l.Slug) || len(l.Slug) > 200 || strings.TrimSpace(l.Name) == "" || len([]rune(l.Name)) > 120 || (l.Type != "movie" && l.Type != "show" && l.Type != "mixed") {
			return out, fmt.Errorf("liste ou type invalide")
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	specs, err := s.cols.ListSpecs(ctx)
	if err != nil {
		return out, err
	}
	libs, err := s.libs.List(ctx)
	if err != nil {
		return out, err
	}
	seen := map[string]bool{}
	for _, l := range req.Lists {
		types := []string{l.Type}
		if l.Type == "mixed" {
			types = []string{"movie", "show"}
		}
		for _, typ := range types {
			source := Profile + l.Slug
			key := source + "/" + typ
			if seen[key] {
				continue
			}
			seen[key] = true
			var found *core.CollectionSpec
			for i := range specs {
				sp := &specs[i]
				if sp.OwnerUserID == 0 && sp.TemplateID == "remix-jukebox-"+l.Slug+"-"+typ && sp.MediaType == typ && len(sp.Sources) == 1 && sp.Sources[0].Kind == "mdblist" && sp.Sources[0].URL == source {
					found = sp
					break
				}
			}
			name := strings.TrimSpace(l.Name)
			if l.Type == "mixed" {
				if typ == "movie" {
					name += " · Films"
				} else {
					name += " · Séries"
				}
			}
			if found == nil {
				sp := core.CollectionSpec{Name: name, Description: "Sélection JukeBox : " + source, MediaType: typ,
					TemplateID: "remix-jukebox-" + l.Slug + "-" + typ,
					Sources:    []core.CollectionSource{{Kind: "mdblist", URL: source, MediaType: typ}}, Match: "all",
					Rules: []core.CollectionRule{}, Exclude: []core.CollectionRule{}, Sort: "released", SortDesc: true,
					Refresh: "daily", ReleaseMode: "released", Enabled: true, Artwork: core.CollectionArtwork{Mode: "auto"},
					Targets: core.CollectionTargets{CollectionsTab: true, HomeRow: true, SectionRow: true, Users: core.AllUsers()}}
				found, err = s.cols.CreateSpec(ctx, sp)
				if err != nil {
					return out, err
				}
				specs = append(specs, *found)
			}
			out.Collections = append(out.Collections, found.ID)
			if !req.Libraries {
				continue
			}
			id, err := strconv.ParseInt(found.ID, 10, 64)
			if err != nil || id <= 0 {
				return out, fmt.Errorf("identifiant de collection invalide")
			}
			var libID core.LibraryID
			for _, lib := range libs {
				if lib.Source == core.LibraryCollection && lib.CollectionID == id && string(lib.MediaType) == typ {
					libID = lib.ID
					break
				}
			}
			if libID == 0 {
				lib, e := s.libs.CreateCollection(ctx, name, id, core.MediaType(typ), presentations(typ))
				if e != nil {
					return out, e
				}
				libs = append(libs, lib)
				libID = lib.ID
			}
			out.Libraries = append(out.Libraries, libID)
		}
	}
	return out, nil
}

func presentations(typ string) core.Presentations {
	var p core.Presentations
	mb, st := "movies", "movie"
	if typ == "show" {
		mb, st = "tvshows", "series"
	}
	// The quick setup explicitly publishes the new libraries to supported clients.
	_ = json.Unmarshal([]byte(fmt.Sprintf(`{"plex":{"base":%q},"mediaBrowser":{"collectionType":%q},"stremio":{"type":%q},"xtream":{"category":%q},"silo":{"type":%q}}`, typ, mb, st, st, mb)), &p)
	return p
}

// ParseCatalog only accepts public list links owned by jukebox8786. Poster-only
// anchors and unrelated links are ignored; labels are plain text, never HTML.
func ParseCatalog(r io.Reader) ([]List, error) {
	doc, err := html.Parse(r)
	if err != nil {
		return nil, err
	}
	entries := map[string]List{}
	var textOf func(*html.Node) string
	textOf = func(n *html.Node) string {
		if n.Type == html.TextNode {
			return n.Data
		}
		var b strings.Builder
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			b.WriteString(textOf(c))
		}
		return b.String()
	}
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "a" {
			for _, a := range n.Attr {
				if a.Key != "href" {
					continue
				}
				u, e := url.Parse(a.Val)
				if e != nil {
					continue
				}
				if u.IsAbs() && (u.Scheme != "https" || u.Host != "mdblist.com") || u.Host != "" && u.Host != "mdblist.com" {
					continue
				}
				slug := strings.TrimPrefix(strings.TrimSuffix(u.Path, "/"), "/lists/jukebox8786/")
				name := strings.Join(strings.Fields(textOf(n)), " ")
				if !strings.HasPrefix(u.Path, "/lists/jukebox8786/") || !slugRE.MatchString(slug) || name == "" || len([]rune(name)) > 120 {
					continue
				}
				typ := "mixed"
				lower := strings.ToLower(name)
				movie := strings.Contains(lower, "film")
				show := strings.Contains(lower, "séries") || strings.Contains(lower, "series")
				if movie && !show {
					typ = "movie"
				}
				if show && !movie {
					typ = "show"
				}
				entries[slug] = List{slug, name, typ}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	out := make([]List, 0, len(entries))
	for _, l := range entries {
		out = append(out, l)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	if len(out) == 0 {
		return nil, fmt.Errorf("catalogue JukeBox indisponible ; utilisez les trois listes proposées")
	}
	return out, nil
}
func (s *Service) Catalog(ctx context.Context) ([]List, error) {
	s.catalogMu.Lock()
	defer s.catalogMu.Unlock()
	if time.Since(s.fetched) < time.Hour {
		return s.catalog, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, Profile, nil)
	if err != nil {
		return nil, err
	}
	res, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("MDBList indisponible")
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return nil, fmt.Errorf("MDBList : HTTP %d", res.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, (2<<20)+1))
	if err != nil {
		return nil, err
	}
	if len(body) > 2<<20 {
		return nil, fmt.Errorf("catalogue MDBList trop volumineux")
	}
	lists, err := ParseCatalog(strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}
	s.catalog, s.fetched = lists, time.Now()
	return lists, nil
}
func (s *Service) Handler() http.Handler {
	mux := http.NewServeMux()
	reply := func(w http.ResponseWriter, status int, v any) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(v)
	}
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) { reply(w, 200, map[string]any{"lists": starters}) })
	mux.HandleFunc("GET /catalog", func(w http.ResponseWriter, r *http.Request) {
		lists, err := s.Catalog(r.Context())
		if err != nil {
			reply(w, 502, map[string]string{"error": err.Error()})
			return
		}
		reply(w, 200, map[string]any{"lists": lists})
	})
	mux.HandleFunc("POST /{$}", func(w http.ResponseWriter, r *http.Request) {
		var req Request
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10)).Decode(&req); err != nil {
			reply(w, 400, map[string]string{"error": "requête invalide"})
			return
		}
		if s.cols == nil || s.libs == nil {
			reply(w, 503, map[string]string{"error": "collections indisponibles"})
			return
		}
		out, err := s.Apply(r.Context(), req)
		if err != nil {
			reply(w, 400, map[string]any{"error": err.Error() + ". Vous pouvez réessayer sans dupliquer les éléments déjà créés.", "result": out})
			return
		}
		reply(w, 200, out)
	})
	return mux
}
