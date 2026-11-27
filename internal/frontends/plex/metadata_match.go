package plex

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontends/plex/model"
)

func (s *Server) handleMetadataMatch(w http.ResponseWriter, r *http.Request) {
	key, err := keyParam(r)
	if err != nil {
		writeHTTPError(w, http.StatusNotFound)
		return
	}
	pr := FromContext(r.Context())
	if pr.User.Delegated {
		writeHTTPError(w, http.StatusForbidden)
		return
	}
	matcher := s.getHost().TitleMatch
	if matcher == nil || (key.Kind() != frontend.KindMovie && key.Kind() != frontend.KindShow) {
		s.handlePlaylistMetadataRefusal(w, r)
		return
	}
	ctx := r.Context()
	title, err := s.metadataTitle(ctx, pr, key)
	if err != nil || title == nil || !s.visibleTitle(ctx, pr.User, title) {
		writeHTTPError(w, http.StatusNotFound)
		return
	}
	q := r.URL.Query()
	if strings.HasSuffix(r.URL.Path, "/match") {
		source, id, ok := strings.Cut(q.Get("guid"), "://")
		if !ok || id == "" || strings.ContainsAny(id, "/?#") {
			writeHTTPError(w, http.StatusBadRequest)
			return
		}
		switch source {
		case string(frontend.MetadataTMDB), string(frontend.MetadataTVDB):
			n, err := strconv.ParseInt(id, 10, 64)
			if err != nil || n <= 0 || strconv.FormatInt(n, 10) != id {
				writeHTTPError(w, http.StatusBadRequest)
				return
			}
		default:
			if !strings.HasPrefix(source, "plugin:") {
				writeHTTPError(w, http.StatusBadRequest)
				return
			}
		}
		next, err := matcher.ApplyTitleMatch(ctx, key, frontend.ProviderRef{Source: frontend.MetadataSourceID(source), ID: id, Type: title.Type})
		if err != nil {
			writeMatchError(w, err)
			return
		}
		w.Header().Set("Content-Location", "/library/metadata/"+next.String())
		w.WriteHeader(http.StatusOK)
		return
	}
	name := q.Get("title")
	if name == "" {
		name = title.Title
	}
	year, _ := strconv.Atoi(q.Get("year"))
	matches, err := matcher.SearchTitleMatches(ctx, title.Type, name, year, nil)
	if err != nil {
		writeMatchError(w, err)
		return
	}
	mc := &model.MediaContainer{Size: len(matches), Identifier: model.LibraryIdentifier}
	for n, candidate := range matches {
		mc.SearchResult = append(mc.SearchResult, model.SearchResult{
			Name: candidate.Title.Title, Year: candidate.Title.Year,
			Lifespan: strconv.Itoa(candidate.Title.Year),
			GUID:     string(candidate.Ref.Source) + "://" + candidate.Ref.ID,
			Score:    float64(max(1, 100-n)),
		})
	}
	s.write(w, r, mc)
}

func writeMatchError(w http.ResponseWriter, err error) {
	if errors.Is(err, frontend.ErrNotFound) {
		writeHTTPError(w, http.StatusNotFound)
		return
	}
	writeHTTPError(w, http.StatusServiceUnavailable)
}
