package music

import (
	"context"
	"reflect"
	"testing"
)

func TestCatalogCacheSharedByPlexAndJellyfin(t *testing.T) {
	for _, jellyfinFirst := range []bool{false, true} {
		name := "PlexThenJellyfin"
		if jellyfinFirst {
			name = "JellyfinThenPlex"
		}
		t.Run(name, func(t *testing.T) {
			s := NewServer(Options{Source: artistSource{}})
			q := &jfReq{s: s}
			albumFirst, albumSecond := s.AlbumTracks, q.album
			artistFirst, artistSecond := s.ArtistAlbums, q.artist
			if jellyfinFirst {
				albumFirst, albumSecond = albumSecond, albumFirst
				artistFirst, artistSecond = artistSecond, artistFirst
			}
			ctx := context.Background()
			a, tracks, err := albumFirst(ctx, 10)
			if err != nil {
				t.Fatal(err)
			}
			b, cachedTracks, err := albumSecond(ctx, 10)
			if err != nil || !reflect.DeepEqual(a, b) || !reflect.DeepEqual(tracks, cachedTracks) {
				t.Fatalf("album cache mismatch: %v", err)
			}
			artist, albums, err := artistFirst(ctx, 1)
			if err != nil {
				t.Fatal(err)
			}
			cachedArtist, cachedAlbums, err := artistSecond(ctx, 1)
			if err != nil || !reflect.DeepEqual(artist, cachedArtist) || !reflect.DeepEqual(albums, cachedAlbums) {
				t.Fatalf("artist cache mismatch: %v", err)
			}
		})
	}
}
