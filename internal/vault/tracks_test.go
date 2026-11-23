package vault

import (
	"context"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

func TestVaultTracksAndAliasKeys(t *testing.T) {
	ctx := context.Background()
	db := store.OpenTest(t)
	dir := t.TempDir()
	s, err := Open(ctx, Options{Dir: dir, DB: db})
	if err != nil {
		t.Fatal(err)
	}

	rel := core.Release{ID: "idx-rel-1", IndexerID: "indexer-a", Name: "Anime.2024.1080p.MULTi.mkv"}
	key, err := s.Put(ctx, PutRequest{NZB: []byte(fixture), Release: rel})
	if err != nil {
		t.Fatal(err)
	}

	entry, err := s.Entry(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	vaultRel := ReleaseFor(entry)
	if vaultRel.ContentID != string(key) {
		t.Fatalf("ReleaseFor ContentID = %q, want %q", vaultRel.ContentID, key)
	}

	ar, ok := s.(AliasResolver)
	if !ok {
		t.Fatal("vault does not implement AliasResolver")
	}
	keys, err := ar.KeysFor(ctx, []core.Release{
		rel,
		vaultRel,
		{ID: "unvaulted-1", IndexerID: "indexer-a"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if keys[rel.ID] != key {
		t.Fatalf("KeysFor alias = %q, want %q", keys[rel.ID], key)
	}
	if keys[vaultRel.ID] != key {
		t.Fatalf("KeysFor vault release = %q, want %q", keys[vaultRel.ID], key)
	}
	if _, exists := keys["unvaulted-1"]; exists {
		t.Fatal("unvaulted release unexpectedly resolved to a vault key")
	}

	ts, ok := s.(TrackStore)
	if !ok {
		t.Fatal("vault does not implement TrackStore")
	}
	probed := core.MediaInfo{
		Container: "mkv",
		Duration:  90 * time.Minute,
		Size:      4 << 30,
		Streams: []core.StreamInfo{
			{Index: 0, Type: "video", Codec: "hevc", Width: 1920, Height: 1080},
			{Index: 1, Type: "audio", Codec: "eac3", Channels: 6, Language: "jpn", Default: true},
			{Index: 2, Type: "audio", Codec: "eac3", Channels: 6, Language: "eng"},
			{Index: 3, Type: "subtitle", Codec: "srt", Language: "eng", External: true, ExternalURL: "https://secret.example/sub.srt"},
		},
	}
	if err := ts.SetTracks(ctx, key, 0, 0, probed); err != nil {
		t.Fatal(err)
	}

	// Re-open the vault to confirm durability and startup preload.
	reopened, err := Open(ctx, Options{Dir: dir, DB: db})
	if err != nil {
		t.Fatal(err)
	}
	got, hit, err := reopened.(TrackStore).Tracks(ctx, key, 0, 0)
	if err != nil || !hit {
		t.Fatalf("Tracks after reopen: hit=%v err=%v", hit, err)
	}
	if got.Estimated || got.Video() == nil {
		t.Fatalf("unexpected tracks: %+v", got)
	}
	var audioLangs []string
	for _, st := range got.Streams {
		if st.External {
			t.Fatalf("external sidecar stream leaked into vault tracks: %+v", st)
		}
		if st.Type == "audio" {
			audioLangs = append(audioLangs, st.Language)
		}
	}
	if len(audioLangs) != 2 || audioLangs[0] != "jpn" || audioLangs[1] != "eng" {
		t.Fatalf("audio languages = %v, want [jpn eng]", audioLangs)
	}

	if err := reopened.Purge(ctx, key); err != nil {
		t.Fatal(err)
	}
	if _, hit, err := reopened.(TrackStore).Tracks(ctx, key, 0, 0); err != nil || hit {
		t.Fatalf("Tracks after purge: hit=%v err=%v", hit, err)
	}
}
