package torrentclient

import (
	"context"
	"errors"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/debrid"
)

type offlineClient struct{ Client }

func (c offlineClient) Get(context.Context, string) (*Torrent, error) {
	return nil, errors.New("offline")
}

func TestProviderPrefersCompleteCopyOnSecondClient(t *testing.T) {
	a, b := newFakeClient(), newFakeClient()
	a.torrents[hashA] = &Torrent{Hash: hashA, Progress: 0.1}
	b.torrents[hashA] = &Torrent{Hash: hashA, Done: true, Root: "/downloads"}
	b.files[hashA] = []File{{Path: "/downloads/movie.mkv", Size: 10}}
	p := &Provider{Slots: []*Slot{{Client: a}, {Client: b}}}
	cached, err := p.CheckCached(t.Context(), []string{hashA})
	if err != nil || !cached[hashA] {
		t.Fatalf("cache: %v %v", cached, err)
	}
	_, err = p.Acquire(t.Context(), debrid.AcquireRequest{InfoHash: hashA, Choose: chooseFirstVideo})
	if err != nil {
		t.Fatalf("torrent complet sur le client 2 mais lecture refusée: %v", err)
	}
}
func TestProviderOfflineClientDoesNotBlockHealthyAdd(t *testing.T) {
	b := newFakeClient()
	p := &Provider{Slots: []*Slot{{Client: offlineClient{newFakeClient()}}, {Client: b}}, Settings: Settings{Download: true, MaxPerDay: 10}}
	_, err := p.Acquire(t.Context(), debrid.AcquireRequest{InfoHash: hashA, Magnet: "magnet:?xt=urn:btih:" + hashA})
	if len(b.adds) != 1 {
		t.Fatalf("client 2 sain ignoré: ajouts=%d erreur=%v", len(b.adds), err)
	}
}
func TestProviderBackgroundMusicMustNotAdd(t *testing.T) {
	c := newFakeClient()
	p := &Provider{Slots: one(c), Settings: Settings{Download: true, Music: true, MaxPerDay: 10}}
	_, _ = p.AcquireMusic(core.WithBackgroundBudget(t.Context(), func(context.Context, string, string, int64) error { return nil }), debrid.AcquireRequest{InfoHash: hashA, Magnet: "magnet:?xt=urn:btih:" + hashA}, true)
	if len(c.adds) != 0 {
		t.Fatalf("vérification en arrière-plan: %d téléchargement lancé", len(c.adds))
	}
}

func (c offlineClient) Add(context.Context, AddOptions) error { return errors.New("offline") }

func TestProviderMusicHonorsCachedOnlyRequest(t *testing.T) {
	c := newFakeClient()
	p := &Provider{Slots: one(c), Settings: Settings{Download: true, Music: true, MaxPerDay: 10}}
	_, err := p.AcquireMusic(t.Context(), debrid.AcquireRequest{InfoHash: hashA, Magnet: "magnet:?xt=urn:btih:" + hashA, CachedOnly: true}, true)
	if !errors.Is(err, core.ErrNotCached) || len(c.adds) != 0 {
		t.Fatalf("cache uniquement ignoré : %v, ajouts=%d", err, len(c.adds))
	}
}
func TestProviderAllClientsOfflineDoesNotAdd(t *testing.T) {
	p := &Provider{Slots: []*Slot{{Client: offlineClient{newFakeClient()}}, {Client: offlineClient{newFakeClient()}}}, Settings: Settings{Download: true, MaxPerDay: 10}}
	_, err := p.Acquire(t.Context(), debrid.AcquireRequest{InfoHash: hashA, Magnet: "magnet:?xt=urn:btih:" + hashA})
	if err == nil || errors.As(err, new(*core.LocalDownloadError)) {
		t.Fatalf("panne masquée : %v", err)
	}
}
