package mediabrowser

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/kit/ids"
)

type remoteMatch struct {
	Name           string            `json:"Name"`
	ProductionYear int               `json:"ProductionYear,omitempty"`
	ProviderIDs    map[string]string `json:"ProviderIds"`
	SearchProvider string            `json:"SearchProviderName,omitempty"`
	Overview       string            `json:"Overview,omitempty"`
	ImageURL       string            `json:"ImageUrl,omitempty"`
}

func (i *instance) searchRemoteMatch(typ frontend.MediaType) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := i.requireAdmin(w, r, false); !ok {
			return
		}
		var body struct {
			SearchInfo remoteMatch `json:"SearchInfo"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&body) != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if i.host.TitleMatch == nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		info := body.SearchInfo
		matches, err := i.host.TitleMatch.SearchTitleMatches(i.identityContext(r), typ, info.Name, info.ProductionYear, info.references(typ))
		if err != nil {
			writeRemoteMatchError(w, err)
			return
		}
		out := make([]remoteMatch, 0, len(matches))
		for _, candidate := range matches {
			t, ref := candidate.Title, candidate.Ref
			label, idName := remoteMatchProvider(ref.Source)
			match := remoteMatch{Name: t.Title, ProductionYear: t.Year, Overview: t.Summary,
				SearchProvider: label, ProviderIDs: nativeProviderIDs(t.IDs)}
			match.ProviderIDs[idName] = ref.ID
			switch ref.Source {
			case frontend.MetadataTMDB:
				if frontend.ValidArtworkPath(t.PosterPath) {
					match.ImageURL = "https://image.tmdb.org/t/p/w342" + t.PosterPath
				}
			case frontend.MetadataTVDB:
				if strings.HasPrefix(t.PosterPath, "https://artworks.thetvdb.com/") {
					match.ImageURL = t.PosterPath
				}
			}
			out = append(out, match)
		}
		jsonReply(w, out)
	}
}

func (i *instance) applyRemoteMatch(w http.ResponseWriter, r *http.Request) {
	viewer, ok := i.requireAdmin(w, r, false)
	if !ok {
		return
	}
	key, err := ids.ParseRatingKeyGUID(r.PathValue("itemId"))
	if err != nil || (key.Kind() != frontend.KindMovie && key.Kind() != frontend.KindShow) {
		http.NotFound(w, r)
		return
	}
	if _, ok := i.authorizePlaybackItem(r, viewer, key); !ok {
		http.NotFound(w, r)
		return
	}
	var body remoteMatch
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&body) != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	typ := frontend.TypeMovie
	if key.Kind() == frontend.KindShow {
		typ = frontend.TypeShow
	}
	ref, ok := body.selectedReference(typ)
	if !ok {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if i.host.TitleMatch == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	next, err := i.host.TitleMatch.ApplyTitleMatch(i.identityContext(r), key, ref)
	if err != nil {
		writeRemoteMatchError(w, err)
		return
	}
	w.Header().Set("Content-Location", "/Items/"+ids.RatingKeyGUID(next))
	w.WriteHeader(http.StatusNoContent)
}

func nativeProviderIDs(ids frontend.IDs) map[string]string {
	providers := map[string]string{}
	if ids.TMDB > 0 {
		providers["Tmdb"] = strconv.FormatInt(ids.TMDB, 10)
	}
	if ids.TVDB > 0 {
		providers["Tvdb"] = strconv.FormatInt(ids.TVDB, 10)
	}
	if ids.IMDB != "" {
		providers["Imdb"] = ids.IMDB
	}
	return providers
}

func remoteMatchProvider(source frontend.MetadataSourceID) (label, idName string) {
	switch source {
	case frontend.MetadataTMDB:
		return "TheMovieDb", "Tmdb"
	case frontend.MetadataTVDB:
		return "TheTVDB", "Tvdb"
	default:
		return string(source), string(source)
	}
}

func (m remoteMatch) providerID(provider string) string {
	var id string
	for name, value := range m.ProviderIDs {
		if !strings.EqualFold(name, provider) || strings.TrimSpace(value) == "" {
			continue
		}
		value = strings.TrimSpace(value)
		if id != "" && id != value {
			return ""
		}
		id = value
	}
	return id
}

func (m remoteMatch) references(typ frontend.MediaType) []frontend.ProviderRef {
	var refs []frontend.ProviderRef
	for _, source := range []frontend.MetadataSourceID{frontend.MetadataTMDB, frontend.MetadataTVDB, "imdb"} {
		id := m.providerID(string(source))
		if source == "imdb" {
			digits, ok := strings.CutPrefix(id, "tt")
			if !ok || digits == "" || len(digits) > 16 || strings.IndexFunc(digits, func(r rune) bool { return r < '0' || r > '9' }) >= 0 {
				continue
			}
		} else if n, err := strconv.ParseInt(id, 10, 64); err != nil || n <= 0 || strconv.FormatInt(n, 10) != id {
			continue
		}
		refs = append(refs, frontend.ProviderRef{Source: source, ID: id, Type: typ})
	}
	return refs
}

func (m remoteMatch) selectedReference(typ frontend.MediaType) (frontend.ProviderRef, bool) {
	label := strings.TrimSpace(m.SearchProvider)
	var selected frontend.ProviderRef
	for _, ref := range m.references(typ) {
		if ref.Source == "imdb" {
			continue
		}
		name, _ := remoteMatchProvider(ref.Source)
		if label != "" {
			if strings.EqualFold(label, name) || strings.EqualFold(label, string(ref.Source)) {
				return ref, true
			}
			continue
		}
		if selected.ID != "" {
			return frontend.ProviderRef{}, false
		}
		selected = ref
	}
	if strings.HasPrefix(label, "plugin:") {
		if id := m.providerID(label); id != "" {
			return frontend.ProviderRef{Source: frontend.MetadataSourceID(label), ID: id, Type: typ}, true
		}
	}
	return selected, selected.ID != ""
}

func writeRemoteMatchError(w http.ResponseWriter, err error) {
	if errors.Is(err, frontend.ErrNotFound) {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusServiceUnavailable)
}
