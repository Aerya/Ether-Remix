package vela

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/kit/ids"
)

func (i *instance) importSource() (frontend.PlaylistImports, bool) {
	source, ok := i.host.Experience.(frontend.PlaylistImports)
	return source, ok
}

func (i *instance) playlistImportFeature(ctx context.Context) object {
	source, ok := i.importSource()
	if !ok {
		return object{"available": false}
	}
	links, files := source.PlaylistImportSupport(ctx)
	if !files {
		return object{"available": false}
	}
	if links == nil {
		links = []string{}
	}
	return object{"available": true, "links": links, "files": []string{"m3u", "csv"}, "maxTracks": 500}
}

// startPlaylistImport serves POST /vela/v0/playlists/import.
func (i *instance) startPlaylistImport(w http.ResponseWriter, r *http.Request) {
	v, _, err := i.viewer(r)
	if err != nil {
		fail(w, err, r)
		return
	}
	source, ok := i.importSource()
	if !ok {
		i.unavailable(w, r)
		return
	}
	var b struct {
		Library string `json:"library"`
		URL     string `json:"url"`
		File    *struct {
			Name   string `json:"name"`
			Format string `json:"format"`
			Text   string `json:"text"`
		} `json:"file"`
	}
	if body(r, &b) != nil || (b.URL == "") == (b.File == nil) {
		problem(w, 400, "request", "invalid_request")
		return
	}
	lib, err := strconv.ParseInt(b.Library, 10, 64)
	if err != nil || lib <= 0 {
		problem(w, 400, "request", "invalid_request")
		return
	}
	req := frontend.PlaylistImportRequest{Library: frontend.LibraryID(lib), URL: b.URL}
	if b.File != nil {
		if len(b.File.Text) > 4<<20 {
			problem(w, 400, "request", "file_too_large")
			return
		}
		req.FileName, req.FileFormat, req.FileText = b.File.Name, b.File.Format, b.File.Text
	}
	id, err := source.StartPlaylistImport(r.Context(), v, req)
	if errors.Is(err, frontend.ErrUnsupportedImport) {
		code := "unsupported_link"
		if b.File != nil {
			code = "unsupported_file"
		}
		problem(w, 400, "request", code)
		return
	}
	if err != nil {
		fail(w, err, r)
		return
	}
	send(w, http.StatusAccepted, object{"id": id})
}

// playlistImport serves GET /vela/v0/playlists/import/{id}.
func (i *instance) playlistImport(w http.ResponseWriter, r *http.Request) {
	v, token, err := i.viewer(r)
	if err != nil {
		fail(w, err, r)
		return
	}
	source, ok := i.importSource()
	if !ok {
		i.unavailable(w, r)
		return
	}
	imp, err := source.PlaylistImport(r.Context(), v, r.PathValue("id"))
	if err != nil {
		fail(w, err, r)
		return
	}
	var ts []frontend.Title
	for _, m := range imp.Matched {
		ts = append(ts, m.Title)
	}
	for _, u := range imp.Unmatched {
		ts = append(ts, u.Candidates...)
	}
	wired, err := i.titles(r, v, token, ts)
	if err != nil {
		fail(w, err, r)
		return
	}
	n := 0
	matched := make([]object, 0, len(imp.Matched))
	for _, m := range imp.Matched {
		matched = append(matched, object{"index": m.Index, "source": importSourceWire(m.Source), "title": wired[n], "by": m.By})
		n++
	}
	unmatched := make([]object, 0, len(imp.Unmatched))
	for _, u := range imp.Unmatched {
		row := object{"index": u.Index, "source": importSourceWire(u.Source), "reason": u.Reason}
		if len(u.Candidates) > 0 {
			row["candidates"] = wired[n : n+len(u.Candidates)]
			n += len(u.Candidates)
		}
		unmatched = append(unmatched, row)
	}
	out := object{"id": imp.ID, "state": imp.State, "name": imp.Name, "total": imp.Total, "done": imp.Done, "matched": matched, "unmatched": unmatched}
	if imp.Description != "" {
		out["description"] = imp.Description
	}
	if imp.Error != "" {
		out["error"] = imp.Error
	}
	if imp.Truncated {
		out["truncated"] = true
		if imp.SourceTotal > 0 {
			out["sourceTotal"] = imp.SourceTotal
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	send(w, 200, out)
}

func importSourceWire(t frontend.ImportTrack) object {
	out := object{"title": t.Title, "artist": t.Artist}
	if t.Album != "" {
		out["album"] = t.Album
	}
	if t.ISRC != "" {
		out["isrc"] = t.ISRC
	}
	if t.DurationSec > 0 {
		out["durationSeconds"] = t.DurationSec
	}
	return out
}

// savePlaylistImport serves POST /vela/v0/playlists/import/{id}/save.
func (i *instance) savePlaylistImport(w http.ResponseWriter, r *http.Request) {
	v, token, err := i.viewer(r)
	if err != nil {
		fail(w, err, r)
		return
	}
	source, ok := i.importSource()
	if !ok {
		i.unavailable(w, r)
		return
	}
	var b struct {
		Name    string            `json:"name"`
		Exclude []int             `json:"exclude"`
		Choose  map[string]string `json:"choose"`
	}
	if body(r, &b) != nil {
		problem(w, 400, "request", "invalid_request")
		return
	}
	choose := map[int]frontend.RatingKey{}
	for idx, id := range b.Choose {
		n, err1 := strconv.Atoi(idx)
		k, err2 := ids.ParseRatingKeyGUID(id)
		if err1 != nil || err2 != nil {
			problem(w, 400, "request", "invalid_choice")
			return
		}
		choose[n] = k
	}
	pl, err := source.SavePlaylistImport(r.Context(), v, r.PathValue("id"), b.Name, b.Exclude, choose)
	if errors.Is(err, frontend.ErrInvalidImportChoice) {
		problem(w, 400, "request", "invalid_choice")
		return
	}
	if err != nil {
		fail(w, err, r)
		return
	}
	cards, err := i.playlistCardsWire(r, v, token, []frontend.Playlist{*pl})
	if err != nil {
		fail(w, err, r)
		return
	}
	send(w, http.StatusCreated, cards[0])
}
