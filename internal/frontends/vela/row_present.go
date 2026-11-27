package vela

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/braedonsaunders/ether/internal/frontend"
)

// Row presentation on the Vela wire (Recommendations pages). The engine fills
// row.Layout.Present; the wire carries:
//
//	layout       standard | hero | wide | tiles | tracks ("" = skin default)
//	card         poster | landscape | square | round ("" = follows the library)
//	role         the items' media role (album, artist, track, movie, …)
//	personal     titles differ per viewer
//	library      the library of a one-row-per-library row
//	experience   watch | listen | read | gallery | live
//	presentation "ranked" for numbered (Top 10) rows
//	key          the row's identity across bootstraps (feed rows only)
//
// Unknown values draw as a standard shelf (Vela's commitment). The retired
// "style" field is never sent.
//
// A feed row's id names one visit's snapshot: Ether re-mints it whenever the
// feed is rebuilt (watch progress, favourites, policy), and an old id answers
// 409 cursor_expired so the client re-reads bootstrap. key is the same for the
// same row across rebuilds: the page row ("r3k9…", "r3k9…@12" for a
// one-row-per-library row) or, for Ether's own picks, the row's source
// ("home.continue", "movies.trending"). A page row that serves several rows
// (count > 1) numbers the extra ones "#2", "#3" in page order. key is opaque
// (clients compare it, never parse it), identical for every viewer of the
// same page, and absent on surface rows, which match by plugin + surface +
// library.

// assignRowKeys fills each feed row's Key (see the wire notes above) for one
// page listing, in page order. Surface rows carry none.
func assignRowKeys(rows []frontend.Row) {
	seen := map[string]int{}
	for n := range rows {
		row := &rows[n]
		if row.Layout.Present.Surface != nil {
			row.Key = ""
			continue
		}
		key := row.Key
		if key == "" {
			key = row.ID
		}
		if p := row.Layout.Present; p.RowID != "" {
			key = p.RowID
			if p.Library > 0 {
				key += "@" + strconv.FormatInt(int64(p.Library), 10)
			}
		}
		seen[key]++
		if seen[key] > 1 {
			key += "#" + strconv.Itoa(seen[key])
		}
		row.Key = key
	}
}

// presentRow writes row's presentation fields onto wire.
func presentRow(wire object, row frontend.Row) {
	p := row.Layout.Present
	if row.RankedTop10 || p.Numbered {
		wire["presentation"] = "ranked"
	}
	if p.Layout != "" {
		wire["layout"] = p.Layout
	}
	if p.Card != "" {
		wire["card"] = p.Card
	}
	if role := rowRole(row); role != "" {
		wire["role"] = role
	}
	if p.Personal {
		wire["personal"] = true
	}
	if p.Library > 0 {
		wire["library"] = strconv.FormatInt(int64(p.Library), 10)
	}
	if p.Experience != "" {
		wire["experience"] = p.Experience
	}
}

// libraryDefaults fills the card and experience a library page's rows left
// to the library.
func libraryDefaults(wire object, view *frontend.View) {
	if view == nil || wire["kind"] == "surface" {
		return
	}
	if _, set := wire["card"]; !set && view.Card != "" {
		wire["card"] = view.Card
	}
	if _, set := wire["experience"]; !set {
		wire["experience"] = view.EffectiveExperience()
	}
}

// rowRole is the media role of the row's items: the first item's declared
// role (album, artist, track, playlist, audiobook, …), else its video kind.
func rowRole(row frontend.Row) string {
	if row.Layout.Present.Role != "" {
		return row.Layout.Present.Role
	}
	for _, e := range row.Entries {
		switch {
		case e.Title != nil:
			return titleRole(*e.Title)
		case e.Collection != nil:
			return string(frontend.MediaRoleCollection)
		case e.LiveChannel != nil:
			return "channel"
		}
	}
	for _, t := range row.Items {
		return titleRole(t)
	}
	return ""
}

func titleRole(t frontend.Title) string {
	if t.MediaRole != "" && t.MediaRole != frontend.MediaRoleItem {
		return string(t.MediaRole)
	}
	return titleWire(t)["kind"].(string)
}

// tracksEntries keeps the leaf items a track list can draw: tracks with a
// duration and an album or artist parent. layout "tracks" guarantees them.
func tracksEntries(row frontend.Row) frontend.Row {
	if row.Layout.Present.Layout != "tracks" {
		return row
	}
	leaf := func(t *frontend.Title) bool {
		if t == nil || t.MediaRole != frontend.MediaRoleTrack || t.Runtime <= 0 {
			return false
		}
		for _, p := range t.MediaParents {
			if p.Role == frontend.MediaRoleAlbum || p.Role == frontend.MediaRoleArtist {
				return true
			}
		}
		return false
	}
	entries := make([]frontend.RowEntry, 0, len(row.Entries))
	for _, e := range row.Entries {
		if leaf(e.Title) {
			entries = append(entries, e)
		}
	}
	row.Entries = entries
	items := make([]frontend.Title, 0, len(row.Items))
	for n := range row.Items {
		if leaf(&row.Items[n]) {
			items = append(items, row.Items[n])
		}
	}
	row.Items = items
	return row
}

// surfaceRowWire is a plugin surface row: a stub the client draws from the
// plugin's document. The id is opaque; plugin, surface (the wire id) and
// library are explicit so no client parses it.
func surfaceRowWire(row frontend.Row) object {
	s := row.Layout.Present.Surface
	id := "surface:" + s.Plugin + ":" + s.Surface
	wire := object{"kind": "surface", "id": id, "name": row.Title, "plugin": s.Plugin, "surface": s.Surface}
	if lib := row.Layout.Present.Library; lib > 0 {
		l := strconv.FormatInt(int64(lib), 10)
		wire["id"] = id + "@" + l
		wire["library"] = l
	}
	if row.Purpose != "" {
		wire["purpose"] = row.Purpose
	}
	return wire
}

// surfaceGate reports which homeRow surfaces this viewer and client may draw:
// published for the viewer and app, with a document for the client's
// renderer. Clients without a renderer get none. One registry read per page.
func (i *instance) surfaceGate(r *http.Request, v *frontend.Viewer) map[string]bool {
	raw, err := i.screenSurfaces(r, v)
	if err != nil {
		return nil
	}
	var entries []struct {
		ID           string `json:"id"`
		Presentation string `json:"presentation"`
	}
	if json.Unmarshal(raw, &entries) != nil {
		return nil
	}
	out := map[string]bool{}
	for _, e := range entries {
		if e.Presentation == "homeRow" {
			out[e.ID] = true
		}
	}
	return out
}
