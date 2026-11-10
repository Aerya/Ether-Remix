package torrentclient

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/debrid"
)

const gib = int64(1) << 30

// fakeUsage renvoie une place libre par dossier vu par Remix.
func fakeUsage(free map[string]int64) func(string) (int64, int64, error) {
	return func(p string) (int64, int64, error) {
		if n, ok := free[p]; ok {
			return 0, n, nil
		}
		return 0, 0, errors.New("absent")
	}
}

func TestProviderFindsTorrentOnAnyClientAndSkipsFullClient(t *testing.T) {
	a, b := newFakeClient(), newFakeClient()
	a.dir, b.dir = "/dl/a", "/dl/b"
	b.torrents[hashA] = &Torrent{Hash: hashA, Done: true, Root: "/dl/b"}
	free := map[string]int64{"/dl/a": 5 * gib, "/dl/b": 500 * gib}
	p := &Provider{Slots: []*Slot{{Client: a, Conf: ClientConf{N: 1}}, {Client: b, Conf: ClientConf{N: 2}}},
		Files: &FileServer{}, Usage: fakeUsage(free),
		Settings: Settings{Download: true, Category: "remix", MaxPerDay: 10, MinFree: 20 * gib}}
	ctx := t.Context()
	if cached, err := p.CheckCached(ctx, []string{hashA}); err != nil || !cached[hashA] {
		t.Fatalf("torrent du client 2 non trouvé : %v %v", cached, err)
	}
	// Client 1 sous le seuil : le nouveau torrent part sur le client 2.
	hashB := strings.Repeat("b", 40)
	_, err := p.Acquire(ctx, debrid.AcquireRequest{InfoHash: hashB, Magnet: "magnet:?xt=urn:btih:" + hashB})
	var lde *core.LocalDownloadError
	if !errors.As(err, &lde) || !lde.Started || lde.Client != "Client 2 (qBittorrent)" {
		t.Fatalf("ajout : %v", err)
	}
	if len(a.adds) != 0 || len(b.adds) != 1 {
		t.Fatalf("ajouts client 1=%d client 2=%d", len(a.adds), len(b.adds))
	}
	// Les deux pleins : rien n'est ajouté, la place est signalée.
	free["/dl/b"] = gib
	p.Slots[1].freeAt = time.Time{}
	hashC := strings.Repeat("c", 40)
	_, err = p.Acquire(ctx, debrid.AcquireRequest{InfoHash: hashC, Magnet: "magnet:?xt=urn:btih:" + hashC})
	if !errors.Is(err, core.ErrLocalNoSpace) || errors.As(err, &lde) {
		t.Fatalf("plein : %v", err)
	}
	if p.added != 1 {
		t.Fatalf("ajouts comptés %d, attendu 1 (réservation rendue)", p.added)
	}
	if msg := core.LocalDownloadMessage(err, "fr"); !strings.Contains(msg, "Plus assez de place") {
		t.Fatalf("message %q", msg)
	}
}

func TestProviderFallsBackWhenClientRefuses(t *testing.T) {
	a, b := newFakeClient(), newFakeClient()
	a.addErr = errors.New("qBittorrent injoignable")
	p := &Provider{Slots: []*Slot{{Client: a, Conf: ClientConf{N: 1}}, {Client: b, Conf: ClientConf{N: 2}}}, Files: &FileServer{},
		Settings: Settings{Download: true, Category: "remix", MaxPerDay: 10}}
	_, err := p.Acquire(t.Context(), debrid.AcquireRequest{InfoHash: hashA, Magnet: "magnet:?xt=urn:btih:" + hashA})
	if !errors.As(err, new(*core.LocalDownloadError)) || len(b.adds) != 1 {
		t.Fatalf("repli : %v, ajouts %d", err, len(b.adds))
	}
	b.addErr = errors.New("refus")
	h := strings.Repeat("d", 40)
	_, err = p.Acquire(t.Context(), debrid.AcquireRequest{InfoHash: h, Magnet: "magnet:?xt=urn:btih:" + h})
	if !errors.Is(err, core.ErrDownloadRefused) || errors.Is(err, core.ErrDownloadQueued) {
		t.Fatalf("tous refusent : %v", err)
	}
}

func TestProviderReportsProgress(t *testing.T) {
	c := newFakeClient()
	c.torrents[hashA] = &Torrent{Hash: hashA, Progress: 0.42, ETA: 12 * time.Minute, Active: true}
	p := &Provider{Slots: one(c), Files: &FileServer{}, Lang: "fr", Settings: Settings{Download: true, MaxPerDay: 1}}
	_, err := p.Acquire(t.Context(), debrid.AcquireRequest{InfoHash: hashA, Magnet: "magnet:?xt=urn:btih:" + hashA})
	if !errors.Is(err, core.ErrDownloadQueued) || !errors.Is(err, core.ErrNotCached) {
		t.Fatalf("err %v", err)
	}
	msg := core.LocalDownloadMessage(err, "")
	if !strings.Contains(msg, "42 %") || !strings.Contains(msg, "12 min") {
		t.Fatalf("message %q", msg)
	}
	if en := core.LocalDownloadMessage(err, "en-US"); !strings.Contains(en, "42%") {
		t.Fatalf("message anglais %q", en)
	}
}

func TestTrackerRulesOverrideClientLimits(t *testing.T) {
	rules, err := ParseTrackerRules(" c411=2/72h ; exemple.org=/168h ")
	if err != nil {
		t.Fatal(err)
	}
	s := Settings{Trackers: rules}
	c := ClientConf{Ratio: 1, SeedTime: 24 * time.Hour}
	magnet := "magnet:?xt=urn:btih:" + hashA + "&tr=" + "https%3A%2F%2Ftracker.c411.org%2Fannounce%2FPASSKEY"
	if r, d := s.Limits(c, magnet); r != 2 || d != 72*time.Hour {
		t.Fatalf("c411 : %v %v", r, d)
	}
	magnet = "magnet:?xt=urn:btih:" + hashA + "&tr=udp%3A%2F%2Fopen.exemple.org%3A1337"
	if r, d := s.Limits(c, magnet); r != 1 || d != 168*time.Hour {
		t.Fatalf("exemple : %v %v (ratio du client gardé)", r, d)
	}
	if r, d := s.Limits(c, "magnet:?xt=urn:btih:"+hashA); r != 1 || d != 24*time.Hour {
		t.Fatalf("sans tracker : %v %v", r, d)
	}
	for _, bad := range []string{"c411", "c411=abc", "=2", "c411=0/0"} {
		if _, err := ParseTrackerRules(bad); err == nil {
			t.Fatalf("%q accepté", bad)
		}
	}
	// L'ajout pose ces limites sur le torrent.
	cl := newFakeClient()
	p := &Provider{Slots: []*Slot{{Client: cl, Conf: c}}, Files: &FileServer{}, Settings: Settings{Download: true, MaxPerDay: 1, Trackers: rules}}
	_, _ = p.Acquire(t.Context(), debrid.AcquireRequest{InfoHash: hashA, Magnet: "magnet:?xt=urn:btih:" + hashA + "&tr=https%3A%2F%2Ftracker.c411.org%2Fa"})
	if len(cl.adds) != 1 || cl.adds[0].Ratio != 2 || cl.adds[0].SeedTime != 72*time.Hour {
		t.Fatalf("ajout %+v", cl.adds)
	}
}

func TestMonitorUsesPerTorrentLimits(t *testing.T) {
	c := newFakeClient()
	fin := time.Now().Add(-30 * time.Hour)
	// Limite du tracker (48 h) posée à l'ajout : pas encore atteinte, même
	// si celle du client (24 h) l'est.
	c.torrents[hashA] = &Torrent{Hash: hashA, Category: "remix", Done: true, Active: true, Finished: fin, Seeding: 30 * time.Hour,
		HasLimits: true, SeedLimit: 48 * time.Hour}
	m := &Monitor{Provider: &Provider{Slots: []*Slot{{Client: c, Conf: ClientConf{SeedTime: 24 * time.Hour}}}, Settings: Settings{Category: "remix"}}}
	m.Round(t.Context())
	if len(c.stopped) != 0 {
		t.Fatalf("arrêté trop tôt %v", c.stopped)
	}
	c.torrents[hashA].Seeding = 49 * time.Hour
	m.Round(t.Context())
	if len(c.stopped) != 1 {
		t.Fatal("non arrêté à la limite du tracker")
	}
}

func TestMonitorAlertsOnceOnLowSpaceAndOnRecovery(t *testing.T) {
	c := newFakeClient()
	c.dir = "/downloads"
	free := map[string]int64{"/data": 3 * gib}
	now := time.Now()
	p := &Provider{Slots: []*Slot{{Client: c, Conf: ClientConf{N: 1, PathMap: []Mapping{{Client: "/downloads", Local: "/data"}}}}},
		Usage: fakeUsage(free), Now: func() time.Time { return now }, Settings: Settings{Category: "remix", MinFree: 20 * gib}}
	var mu sync.Mutex
	var alerts []string
	m := &Monitor{Provider: p, Alert: func(_ context.Context, critical bool, title, msg string) {
		mu.Lock()
		defer mu.Unlock()
		alerts = append(alerts, title+"|"+msg)
		if critical != strings.Contains(title, "insuffisante") {
			t.Errorf("gravité %v pour %q", critical, title)
		}
	}}
	m.Round(t.Context())
	m.Round(t.Context())
	if len(alerts) != 1 || !strings.Contains(alerts[0], "3,0 Go") {
		t.Fatalf("alertes %v, attendu une seule", alerts)
	}
	free["/data"] = 80 * gib
	now = now.Add(2 * time.Minute) // mesure périmée
	m.Round(t.Context())
	if len(alerts) != 2 || !strings.Contains(alerts[1], "place revenue") {
		t.Fatalf("retour : %v", alerts)
	}
	st := p.Status(t.Context())
	if len(st.Clients) != 1 || st.Clients[0].Free == nil || *st.Clients[0].Free != 80*gib || st.Clients[0].LowSpace {
		t.Fatalf("état %+v", st.Clients)
	}
}

func TestStatusListsDownloadsFirst(t *testing.T) {
	c := newFakeClient()
	c.torrents[hashA] = &Torrent{Hash: hashA, Name: "Fini", Category: "remix", Done: true, Progress: 1}
	h := strings.Repeat("e", 40)
	c.torrents[h] = &Torrent{Hash: h, Name: "En cours", Category: "remix", Progress: 0.3, ETA: time.Hour, Active: true}
	p := &Provider{Slots: one(c), Usage: fakeUsage(nil), Settings: Settings{Category: "remix", MaxPerDay: 10}}
	st := p.Status(t.Context())
	if len(st.Downloads) != 2 || st.Downloads[0].Name != "En cours" || st.Downloads[0].ETA != 3600 || st.Seeding != 1 {
		t.Fatalf("état %+v", st)
	}
	if st.Clients[0].Version != "1.0" || st.Clients[0].Free != nil || st.Clients[0].FreeNote == "" {
		t.Fatalf("client %+v", st.Clients[0])
	}
}

func TestLoadSettingsSeveralClients(t *testing.T) {
	t.Setenv("PSE_TORRENT", "on")
	t.Setenv("PSE_TORRENT_URL", "http://qb:8080")
	t.Setenv("PSE_TORRENT_KEY", "qbt_abc")
	t.Setenv("PSE_TORRENT_RATIO", "1.5")
	t.Setenv("PSE_TORRENT2", "on")
	t.Setenv("PSE_TORRENT2_CLIENT", "rtorrent")
	t.Setenv("PSE_TORRENT2_URL", "http://rt/RPC2")
	t.Setenv("PSE_TORRENT2_SEED_TIME", "96h")
	t.Setenv("PSE_TORRENT3_URL", "http://ignored") // client 3 non activé
	t.Setenv("PSE_TORRENT_MIN_FREE", "50")
	s := LoadSettings()
	if !s.Enabled || len(s.Clients) != 2 || s.MinFree != 50*gib {
		t.Fatalf("réglages %+v", s)
	}
	if c := s.Clients[0]; c.Key != "qbt_abc" || c.Ratio != 1.5 || c.Label() != "qBittorrent" {
		t.Fatalf("client 1 %+v", c)
	}
	if c := s.Clients[1]; c.Kind != "rtorrent" || c.SeedTime != 96*time.Hour || c.Label() != "Client 2 (rTorrent)" {
		t.Fatalf("client 2 %+v", c)
	}
	if s.Label() != "2 clients" {
		t.Fatalf("libellé %q", s.Label())
	}
}
