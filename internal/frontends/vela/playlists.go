package vela

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/kit/ids"
)

func (i *instance) viewerPlaylists() (frontend.ViewerPlaylists, bool) {
	vp, ok := i.host.Experience.(frontend.ViewerPlaylists)
	if !ok {
		return nil, false
	}
	if avail, ok := i.host.Experience.(frontend.PlaylistAvailability); ok && !avail.PlaylistsAvailable() {
		return nil, false
	}
	return vp, true
}

func (i *instance) playlistsAvailable() bool {
	_, ok := i.viewerPlaylists()
	return ok
}

func playlistKey(r *http.Request) (frontend.RatingKey, error) {
	k, err := key(r)
	if err != nil || k.Kind() != frontend.KindPlaylist {
		return 0, frontend.ErrNotFound
	}
	return k, nil
}

func parsePlaylistTitleIDs(rawIDs []string) ([]frontend.RatingKey, error) {
	keys := make([]frontend.RatingKey, 0, len(rawIDs))
	for _, raw := range rawIDs {
		if raw == "" {
			return nil, frontend.ErrBadPlaylist
		}
		k, err := ids.ParseRatingKeyGUID(raw)
		if err != nil {
			return nil, frontend.ErrPlaylistUnsupportedKind
		}
		keys = append(keys, k)
	}
	return keys, nil
}

func playlistTimestamp(pl frontend.Playlist) string {
	t := pl.UpdatedAt
	if t.IsZero() {
		t = pl.AddedAt
	}
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func (i *instance) playlistArtwork(r *http.Request, v *frontend.Viewer, token string, pls []frontend.Playlist) (map[frontend.RatingKey][]object, error) {
	art := map[frontend.RatingKey][]object{}
	if len(pls) == 0 || i.host.ImageGrants == nil {
		return art, nil
	}
	kinds := []string{frontend.ImagePoster, frontend.ImageBackdrop}
	if signer, ok := i.host.ImageGrants.(frontend.ImagePageSigner); ok {
		titles := make([]frontend.Title, 0, len(pls))
		seen := make(map[frontend.RatingKey]bool, len(pls))
		for _, pl := range pls {
			if !seen[pl.Key] {
				seen[pl.Key] = true
				titles = append(titles, frontend.Title{Key: pl.Key})
			}
		}
		images, err := signer.SignPage(r.Context(), frontend.ImagePageRequest{
			Credential: token,
			Device:     requestDevice(r),
			Titles:     titles,
			Kinds:      kinds,
		})
		if err != nil {
			return nil, err
		}
		for _, item := range images {
			for _, g := range item.Images {
				im := g.Image
				if im.Path == "" || g.Secret == "" {
					continue
				}
				art[item.Item] = append(art[item.Item], object{
					"id":        hash(im.Path),
					"kind":      im.Kind,
					"tag":       im.Tag,
					"sizes":     []object{imageSize(im, g.Secret)},
					"blurHash":  im.BlurHash,
					"thumbHash": im.ThumbHash,
				})
			}
		}
		return art, nil
	}
	if artReader, hasArt := i.host.Experience.(frontend.ItemArtwork); hasArt {
		seen := make(map[frontend.RatingKey]bool, len(pls))
		for _, pl := range pls {
			if seen[pl.Key] {
				continue
			}
			seen[pl.Key] = true
			imgs, ok, err := artReader.ItemArtwork(r.Context(), v, requestDevice(r), pl.Key)
			if err != nil || !ok {
				continue
			}
			for _, im := range imgs {
				if (im.Kind != frontend.ImagePoster && im.Kind != frontend.ImageBackdrop) || im.Path == "" {
					continue
				}
				secret, err := i.host.ImageGrants.Issue(r.Context(), frontend.ImageGrantRequest{
					Credential: token,
					Device:     requestDevice(r),
					Item:       pl.Key,
					Image:      im,
				})
				if err != nil || secret == "" {
					continue
				}
				art[pl.Key] = append(art[pl.Key], object{
					"id":        hash(im.Path),
					"kind":      im.Kind,
					"tag":       im.Tag,
					"sizes":     []object{imageSize(im, secret)},
					"blurHash":  im.BlurHash,
					"thumbHash": im.ThumbHash,
				})
			}
		}
	}
	return art, nil
}

func (i *instance) playlistCardsWire(r *http.Request, v *frontend.Viewer, token string, pls []frontend.Playlist) ([]object, error) {
	art, err := i.playlistArtwork(r, v, token, pls)
	if err != nil {
		return nil, err
	}
	out := make([]object, 0, len(pls))
	for _, pl := range pls {
		images := art[pl.Key]
		if images == nil {
			images = []object{}
		}
		card := object{
			"id":        ids.RatingKeyGUID(pl.Key),
			"kind":      "playlist",
			"name":      pl.Title,
			"artwork":   images,
			"itemCount": len(pl.Items),
			"smart":     pl.Smart,
			"updatedAt": playlistTimestamp(pl),
		}
		if pl.Summary != "" {
			card["summary"] = pl.Summary
		}
		out = append(out, card)
	}
	return out, nil
}

func (i *instance) listPlaylists(w http.ResponseWriter, r *http.Request) {
	v, token, err := i.viewer(r)
	if err != nil {
		fail(w, err, r)
		return
	}
	vp, ok := i.viewerPlaylists()
	if !ok {
		i.unavailable(w, r)
		return
	}
	pageLimit := limit(r)
	scope, err := i.livePageScope(r, v, token)
	if err != nil {
		fail(w, err, r)
		return
	}
	c, err := i.readLiveCursor(r, scope, hashJSON([]any{"playlists", pageLimit}))
	if err != nil {
		fail(w, err, r)
		return
	}
	all, err := vp.Playlists(r.Context(), v, frontend.PageQuery{Offset: 0, Limit: 500})
	if err != nil {
		fail(w, err, r)
		return
	}
	type playlistStamp struct {
		Key       frontend.RatingKey
		Title     string
		Smart     bool
		ItemCount int
		UpdatedAt int64
	}
	stamps := make([]playlistStamp, len(all.Items))
	for idx, pl := range all.Items {
		stamps[idx] = playlistStamp{
			Key:       pl.Key,
			Title:     pl.Title,
			Smart:     pl.Smart,
			ItemCount: len(pl.Items),
			UpdatedAt: pl.UpdatedAt.UnixNano(),
		}
	}
	snapshot := hashJSON(stamps)
	if c.Snapshot != "" && c.Snapshot != snapshot {
		fail(w, frontend.ErrLiveGuideStale, r)
		return
	}
	start := min(c.Offset, len(all.Items))
	end := min(start+pageLimit, len(all.Items))
	cards, err := i.playlistCardsWire(r, v, token, all.Items[start:end])
	if err != nil {
		fail(w, err, r)
		return
	}
	out := object{"items": cards, "continuation": "exhausted"}
	if end < len(all.Items) {
		next, err := i.nextLiveCursor(c, end, snapshot)
		if err != nil {
			fail(w, err, r)
			return
		}
		out["continuation"], out["nextCursor"] = "more", next
	}
	cache(w, r, out)
}

func (i *instance) createPlaylist(w http.ResponseWriter, r *http.Request) {
	v, token, err := i.viewer(r)
	if err != nil {
		fail(w, err, r)
		return
	}
	vp, ok := i.viewerPlaylists()
	if !ok {
		i.unavailable(w, r)
		return
	}
	var b struct {
		Name     string   `json:"name"`
		TitleIDs []string `json:"titleIds"`
	}
	if body(r, &b) != nil {
		problem(w, 400, "request", "invalid_request")
		return
	}
	keys, err := parsePlaylistTitleIDs(b.TitleIDs)
	if err != nil {
		fail(w, err, r)
		return
	}
	pl, err := vp.CreateViewerPlaylist(r.Context(), v, b.Name, keys)
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

func (i *instance) playlistDetail(w http.ResponseWriter, r *http.Request) {
	v, token, err := i.viewer(r)
	if err != nil {
		fail(w, err, r)
		return
	}
	vp, ok := i.viewerPlaylists()
	if !ok {
		i.unavailable(w, r)
		return
	}
	k, err := playlistKey(r)
	if err != nil {
		problem(w, 404, "not-found", "not_found")
		return
	}
	pageLimit := limit(r)
	scope, err := i.livePageScope(r, v, token)
	if err != nil {
		fail(w, err, r)
		return
	}
	c, err := i.readLiveCursor(r, scope, hashJSON([]any{"playlist-detail", ids.RatingKeyGUID(k), pageLimit}))
	if err != nil {
		fail(w, err, r)
		return
	}
	contents, err := vp.ViewerPlaylistContents(r.Context(), v, k)
	if err != nil {
		fail(w, err, r)
		return
	}
	entryStamps := make([]string, len(contents.Entries))
	for idx, entry := range contents.Entries {
		entryStamps[idx] = entry.EntryID
	}
	snapshot := hashJSON([]any{
		contents.Playlist.Key,
		contents.Playlist.Title,
		contents.Playlist.Summary,
		contents.Playlist.Smart,
		contents.Playlist.UpdatedAt.UnixNano(),
		entryStamps,
	})
	if c.Snapshot != "" && c.Snapshot != snapshot {
		fail(w, frontend.ErrLiveGuideStale, r)
		return
	}
	start := min(c.Offset, len(contents.Entries))
	end := min(start+pageLimit, len(contents.Entries))
	pageEntries := contents.Entries[start:end]
	pageTitles := make([]frontend.Title, len(pageEntries))
	for idx, entry := range pageEntries {
		pageTitles[idx] = entry.Title
	}
	wiredTitles, err := i.titles(r, v, token, pageTitles)
	if err != nil {
		fail(w, err, r)
		return
	}
	items := make([]object, 0, len(pageEntries))
	for idx, entry := range pageEntries {
		t := wiredTitles[idx]
		item := object{
			"id":           t["id"],
			"entryId":      entry.EntryID,
			"kind":         "title",
			"title":        t,
			"receiptToken": "",
		}
		if !entry.AddedAt.IsZero() {
			item["addedAt"] = entry.AddedAt.UTC().Format(time.RFC3339)
		}
		items = append(items, item)
	}
	art, err := i.playlistArtwork(r, v, token, []frontend.Playlist{contents.Playlist})
	if err != nil {
		fail(w, err, r)
		return
	}
	images := art[contents.Playlist.Key]
	if images == nil {
		images = []object{}
	}
	collection := object{
		"id":         ids.RatingKeyGUID(contents.Playlist.Key),
		"kind":       "playlist",
		"name":       contents.Playlist.Title,
		"artwork":    images,
		"smart":      contents.Playlist.Smart,
		"entryCount": len(contents.Entries),
		"updatedAt":  playlistTimestamp(contents.Playlist),
	}
	if contents.Playlist.Summary != "" {
		collection["summary"] = contents.Playlist.Summary
	}
	out := object{
		"collection":   collection,
		"items":        items,
		"continuation": "exhausted",
	}
	if end < len(contents.Entries) {
		next, err := i.nextLiveCursor(c, end, snapshot)
		if err != nil {
			fail(w, err, r)
			return
		}
		out["continuation"], out["nextCursor"] = "more", next
	}
	varyLanguage(w)
	cache(w, r, out)
}

func (i *instance) renamePlaylist(w http.ResponseWriter, r *http.Request) {
	v, token, err := i.viewer(r)
	if err != nil {
		fail(w, err, r)
		return
	}
	vp, ok := i.viewerPlaylists()
	if !ok {
		i.unavailable(w, r)
		return
	}
	k, err := playlistKey(r)
	if err != nil {
		problem(w, 404, "not-found", "not_found")
		return
	}
	var b struct {
		Name    *string `json:"name"`
		Summary *string `json:"summary"`
	}
	if body(r, &b) != nil || (b.Name == nil && b.Summary == nil) {
		problem(w, 400, "request", "invalid_request")
		return
	}
	var pl *frontend.Playlist
	if editor, can := vp.(frontend.ViewerPlaylistEditor); can {
		pl, err = editor.EditViewerPlaylist(r.Context(), v, k, b.Name, b.Summary)
	} else if b.Name != nil && b.Summary == nil {
		pl, err = vp.RenameViewerPlaylist(r.Context(), v, k, *b.Name)
	} else {
		err = frontend.ErrNotAvailable
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
	send(w, http.StatusOK, cards[0])
}

func (i *instance) deletePlaylist(w http.ResponseWriter, r *http.Request) {
	v, _, err := i.viewer(r)
	if err != nil {
		fail(w, err, r)
		return
	}
	vp, ok := i.viewerPlaylists()
	if !ok {
		i.unavailable(w, r)
		return
	}
	k, err := playlistKey(r)
	if err != nil {
		problem(w, 404, "not-found", "not_found")
		return
	}
	if err := vp.DeletePlaylist(r.Context(), v, k); err != nil {
		fail(w, err, r)
		return
	}
	send(w, http.StatusOK, object{"accepted": true})
}

func (i *instance) addPlaylistEntries(w http.ResponseWriter, r *http.Request) {
	v, _, err := i.viewer(r)
	if err != nil {
		fail(w, err, r)
		return
	}
	vp, ok := i.viewerPlaylists()
	if !ok {
		i.unavailable(w, r)
		return
	}
	k, err := playlistKey(r)
	if err != nil {
		problem(w, 404, "not-found", "not_found")
		return
	}
	var b struct {
		TitleIDs []string `json:"titleIds"`
	}
	if body(r, &b) != nil || len(b.TitleIDs) == 0 {
		problem(w, 400, "request", "invalid_request")
		return
	}
	keys, err := parsePlaylistTitleIDs(b.TitleIDs)
	if err != nil {
		fail(w, err, r)
		return
	}
	entryCount, err := vp.AddViewerPlaylistEntries(r.Context(), v, k, keys)
	if err != nil {
		fail(w, err, r)
		return
	}
	send(w, http.StatusOK, object{"entryCount": entryCount})
}

func (i *instance) removePlaylistEntry(w http.ResponseWriter, r *http.Request) {
	v, _, err := i.viewer(r)
	if err != nil {
		fail(w, err, r)
		return
	}
	vp, ok := i.viewerPlaylists()
	if !ok {
		i.unavailable(w, r)
		return
	}
	k, err := playlistKey(r)
	if err != nil {
		problem(w, 404, "not-found", "not_found")
		return
	}
	entryID := r.PathValue("entryId")
	if entryID == "" {
		problem(w, 404, "not-found", "not_found")
		return
	}
	if err := vp.RemoveViewerPlaylistEntry(r.Context(), v, k, entryID); err != nil {
		fail(w, err, r)
		return
	}
	send(w, http.StatusOK, object{"accepted": true})
}

func (i *instance) movePlaylistEntry(w http.ResponseWriter, r *http.Request) {
	v, _, err := i.viewer(r)
	if err != nil {
		fail(w, err, r)
		return
	}
	vp, ok := i.viewerPlaylists()
	if !ok {
		i.unavailable(w, r)
		return
	}
	k, err := playlistKey(r)
	if err != nil {
		problem(w, 404, "not-found", "not_found")
		return
	}
	entryID := r.PathValue("entryId")
	if entryID == "" {
		problem(w, 404, "not-found", "not_found")
		return
	}
	var rawBody map[string]json.RawMessage
	if body(r, &rawBody) != nil {
		problem(w, 400, "request", "invalid_request")
		return
	}
	rawAfter, hasAfter := rawBody["afterEntryId"]
	if !hasAfter {
		problem(w, 400, "request", "invalid_request")
		return
	}
	afterEntryID := ""
	if strings.TrimSpace(string(rawAfter)) != "null" {
		if err := json.Unmarshal(rawAfter, &afterEntryID); err != nil || afterEntryID == "" {
			problem(w, 400, "request", "invalid_request")
			return
		}
	}
	if err := vp.MoveViewerPlaylistEntry(r.Context(), v, k, entryID, afterEntryID); err != nil {
		fail(w, err, r)
		return
	}
	send(w, http.StatusOK, object{"accepted": true})
}

func (i *instance) titlePlaylists(w http.ResponseWriter, r *http.Request) {
	v, _, err := i.viewer(r)
	if err != nil {
		fail(w, err, r)
		return
	}
	vp, ok := i.viewerPlaylists()
	if !ok {
		i.unavailable(w, r)
		return
	}
	k, err := key(r)
	if err != nil {
		problem(w, 404, "not-found", "not_found")
		return
	}
	memberships, err := vp.TitlePlaylists(r.Context(), v, k)
	if err != nil {
		fail(w, err, r)
		return
	}
	items := make([]object, 0, len(memberships))
	for _, m := range memberships {
		entryIDs := m.EntryIDs
		if entryIDs == nil {
			entryIDs = []string{}
		}
		items = append(items, object{
			"id":       ids.RatingKeyGUID(m.Playlist.Key),
			"name":     m.Playlist.Title,
			"smart":    m.Playlist.Smart,
			"entryIds": entryIDs,
		})
	}
	cache(w, r, object{"items": items})
}
