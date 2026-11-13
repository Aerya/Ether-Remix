package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/pse/music"
	"github.com/braedonsaunders/ether/internal/pse/torrentclient"
)

type torrentMusicFinder struct{ musicAlbumFinder }

func (torrentMusicFinder) FindAlbum(context.Context, string) ([]music.TorrentHit, error) {
	return []music.TorrentHit{{Title: "Artist Album FLAC", InfoHash: strings.Repeat("a", 40), Magnet: "magnet:?xt=urn:btih:" + strings.Repeat("a", 40), Seeders: 10}}, nil
}

type torrentMusicClient struct {
	torrentclient.Client
	done bool
	root string
}

func (c *torrentMusicClient) Get(context.Context, string) (*torrentclient.Torrent, error) {
	return &torrentclient.Torrent{Hash: strings.Repeat("a", 40), Done: c.done, Root: c.root}, nil
}
func (c *torrentMusicClient) Files(context.Context, torrentclient.Torrent) ([]torrentclient.File, error) {
	return []torrentclient.File{{Path: filepath.Join(c.root, "01 First.mp3"), Size: 4}}, nil
}
func TestTorrentMusicRetriesCompletedDownload(t *testing.T) {
	a := &App{}
	defer pseRuntimes.Delete(a)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "01 First.mp3"), []byte("test"), 0600); err != nil {
		t.Fatal(err)
	}
	c := &torrentMusicClient{root: dir}
	pseRuntimeOf(a).torrent.prov = &torrentclient.Provider{Slots: []*torrentclient.Slot{{Client: c}}}
	account := &pseTorrentMusicAccount{a: a}
	now := time.Now()
	mk := func() *music.DebridResolver {
		return &music.DebridResolver{Finder: torrentMusicFinder{}, Account: account, Now: func() time.Time { return now }}
	}
	res := mk()
	track := music.Track{ID: 1, Number: 1, Title: "First", Artist: music.Artist{Name: "Artist"}, Album: music.Album{ID: 1, Title: "Album"}}
	if _, err := res.Resolve(t.Context(), track); err == nil {
		t.Fatal("torrent incomplet accepté")
	}
	c.done = true
	now = now.Add(6 * time.Second)
	// Un résolveur neuf confirme que le même fichier est devenu lisible.
	if _, err := mk().Resolve(t.Context(), track); err != nil {
		t.Fatalf("contrôle fichier complet: %v", err)
	}
	if _, err := res.Resolve(t.Context(), track); err != nil {
		t.Fatalf("album complet toujours absent après 6 secondes: %v", err)
	}
}

type absentTorrentClient struct {
	torrentclient.Client
	adds int
}

func (c *absentTorrentClient) Get(context.Context, string) (*torrentclient.Torrent, error) {
	return nil, nil
}
func (c *absentTorrentClient) Add(context.Context, torrentclient.AddOptions) error {
	c.adds++
	return nil
}
func TestTorrentMusicDiagnosticDoesNotDownload(t *testing.T) {
	a := &App{}
	defer pseRuntimes.Delete(a)
	c := &absentTorrentClient{}
	pseRuntimeOf(a).torrent.prov = &torrentclient.Provider{Slots: []*torrentclient.Slot{{Client: c}}, Settings: torrentclient.Settings{Download: true, Music: true, MaxPerDay: 10}}
	hits, _ := (torrentMusicFinder{}).FindAlbum(t.Context(), "")
	_, err := (&pseTorrentMusicAccount{a: a}).OpenCached(music.WithCachedOnly(t.Context()), hits[0])
	if err == nil || c.adds != 0 {
		t.Fatalf("diagnostic : erreur=%v, ajouts=%d", err, c.adds)
	}
}
