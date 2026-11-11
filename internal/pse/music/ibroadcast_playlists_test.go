package music

import (
	"encoding/json"
	"github.com/braedonsaunders/ether/internal/store"
	"testing"
)

func TestIBroadcastPlaylistOrderIsolationAndRefresh(t *testing.T) {
	raw := json.RawMessage(`{"map":{"name":0,"tracks":1,"trashed":2},"1":["Sélection",[20,"10",20,99],false],"2":["Corbeille",[10],true],"3":["Vide",[],false]}`)
	lists, err := ibPlaylists(raw, map[string]Track{"10": {ID: 10}, "20": {ID: 20}})
	if err != nil || len(lists) != 2 {
		t.Fatal(lists, err)
	}
	got := lists[0].Tracks
	if len(got) != 3 || got[0].ID != 20 || got[1].ID != 10 || got[2].ID != 20 {
		t.Fatal(got)
	}
	db := store.OpenTest(t)
	p := &Playlists{DB: db}
	ctx := t.Context()
	if err := p.replace(ctx, 7, "ibroadcast:account-a", lists); err != nil {
		t.Fatal(err)
	}
	if err := p.replace(ctx, 8, "ibroadcast:account-b", lists); err != nil {
		t.Fatal(err)
	}
	// A subsequent import updates rather than duplicates; removing an account's
	// playlist cannot delete the same provider playlist imported by another user.
	if err := p.replace(ctx, 7, "ibroadcast:account-a", lists[:1]); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM pse_music_playlists WHERE viewer_id=7").Scan(&count); err != nil || count != 1 {
		t.Fatal(count, err)
	}
	if err := db.QueryRow("SELECT COUNT(*) FROM pse_music_playlists WHERE viewer_id=8").Scan(&count); err != nil || count != 2 {
		t.Fatal(count, err)
	}
	if _, err := ibPlaylists(json.RawMessage(`{"map":{"name":0,"tracks":1},"1":["Invalid",{}]}`), nil); err == nil {
		t.Fatal("malformed list accepted")
	}
}

func TestIBroadcastAccountsDisconnectIndependently(t *testing.T) {
	db := store.OpenTest(t)
	ctx := t.Context()
	a := &IBroadcast{DB: db, Sealer: fakeSealer{}, AccountID: 10, OwnerID: 7}
	b := &IBroadcast{DB: db, Sealer: fakeSealer{}, AccountID: 20, OwnerID: 8}
	if err := a.save(ctx, ibCredentials{Refresh: "account-a"}); err != nil {
		t.Fatal(err)
	}
	if err := b.save(ctx, ibCredentials{Refresh: "account-b"}); err != nil {
		t.Fatal(err)
	}
	for _, account := range []*IBroadcast{a, b} {
		if err := ReplaceOwned(ctx, db, account.provider(), []Track{{ID: ibBase + account.AccountID, Title: account.provider()}}); err != nil {
			t.Fatal(err)
		}
		if err := (&Playlists{DB: db}).replace(ctx, account.OwnerID, account.provider(), []Playlist{{Ref: "1", Name: "Private"}}); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.Disconnect(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := a.load(ctx); err == nil {
		t.Fatal("disconnected account retained")
	}
	c, err := b.load(ctx)
	if err != nil || c.Refresh != "account-b" {
		t.Fatal(c.Refresh, err)
	}
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM pse_music_playlists WHERE viewer_id=8").Scan(&count); err != nil || count != 1 {
		t.Fatal(count, err)
	}
	if err := db.QueryRow("SELECT COUNT(*) FROM pse_music_owned WHERE provider=?", b.provider()).Scan(&count); err != nil || count != 1 {
		t.Fatal(count, err)
	}
}
