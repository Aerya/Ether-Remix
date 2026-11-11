package music

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
)

// ibPlaylists keeps provider ordering and repeated songs. Missing/trashed tracks
// have no catalogue entry and are omitted; no fuzzy remote search substitutes a
// different recording for a personal library track.
func ibPlaylists(raw json.RawMessage, available map[string]Track) ([]Playlist, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return []Playlist{}, nil
	}
	table, err := parseIBTable(raw)
	if err != nil {
		return nil, err
	}
	if _, ok := table.Map["tracks"]; !ok {
		return nil, fmt.Errorf("titres des playlists iBroadcast manquants")
	}
	ids := make([]string, 0, len(table.Rows))
	for id := range table.Rows {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	lists := []Playlist{}
	for _, id := range ids {
		if table.trashed(id) {
			continue
		}
		var refs []json.RawMessage
		if err := json.Unmarshal(table.value(id, "tracks"), &refs); err != nil {
			return nil, fmt.Errorf("playlist iBroadcast invalide : %w", err)
		}
		pl := Playlist{Ref: id, Name: table.text(id, "name"), Tracks: []Track{}}
		for _, rawID := range refs {
			var ref string
			if json.Unmarshal(rawID, &ref) != nil {
				ref = string(rawID)
			}
			if n, err := strconv.ParseInt(ref, 10, 64); err != nil || n <= 0 {
				return nil, fmt.Errorf("identifiant de titre iBroadcast invalide")
			}
			if track, ok := available[ref]; ok {
				pl.Tracks = append(pl.Tracks, track)
			}
		}
		lists = append(lists, pl)
	}
	return lists, nil
}
