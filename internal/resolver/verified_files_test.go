package resolver

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

// alkoRelease is a verified AllDebrid file as the StreamFusion Reborn
// importer builds it.
func alkoRelease(id, name string, sizeMB int64) core.Release {
	r := rel("sfr-alko:"+id, name, sizeMB)
	r.NZBURL = ""
	r.Protocol = core.ProtocolHTTP
	r.Indexer = "StreamFusion Reborn (Alkodiques)"
	r.StreamURL = "https://alldebrid.com/f/" + id
	r.StreamDebrid, r.StreamOnDemand = "alldebrid", true
	r.DebridCached = []string{"alldebrid"}
	return r
}

// remuxTorrent est le torrent de meilleure résolution (1080p) face à
// l'Alkodique 720p : le rang qualité du profil 1080p le place devant.
func remuxTorrent() core.Release {
	r := rel("torrent-remux", "The.Signal.1999.1080p.WEB-DL.DDP5.1.H.264-GRP", 8000)
	r.NZBURL, r.Protocol, r.InfoHash = "", core.ProtocolTorrent, hashA
	return r
}

func TestVerifiedFilesRanking(t *testing.T) {
	const web = "The.Signal.1999.720p.WEB-DL.DDP5.1.H.264-ALKO"
	tests := []struct {
		name     string
		mode     string
		releases []core.Release
		want     string
	}{
		{"first: l'Alkodique passe devant un meilleur rang", "first", []core.Release{remuxTorrent(), alkoRelease("a1", web, 3000)}, "sfr-alko:a1"},
		{"défaut vide = first", "", []core.Release{remuxTorrent(), alkoRelease("a1", web, 3000)}, "sfr-alko:a1"},
		{"valeur inconnue = first", "bizarre", []core.Release{remuxTorrent(), alkoRelease("a1", web, 3000)}, "sfr-alko:a1"},
		{"off: comportement actuel", "off", []core.Release{remuxTorrent(), alkoRelease("a1", web, 3000)}, "torrent-remux"},
		{"tie: le meilleur rang garde la tête", "tie", []core.Release{remuxTorrent(), alkoRelease("a1", web, 3000)}, "torrent-remux"},
		{"hors résolution min du profil: jamais promu", "first", []core.Release{remuxTorrent(), alkoRelease("a1", "The.Signal.1999.480p.WEB-DL.DDP5.1.H.264-ALKO", 1500)}, "torrent-remux"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := testCfg()
			cfg.Resolver.VerifiedFiles = tc.mode
			svc := New(&fakeIndexer{releases: tc.releases}, fakeHealth{}, nil, cfg, Options{})
			if got := resolveOrder(t, svc, matrixTitle()); got[0] != tc.want {
				t.Fatalf("ordre %v, tête attendue %s", got, tc.want)
			}
		})
	}
}

func TestVerifiedFilesTieOrdersEqualQuality(t *testing.T) {
	web := "The.Signal.1999.1080p.WEB-DL.DDP5.1.H.264-GRP"
	tor := rel("torrent-web", web, 6000)
	tor.NZBURL, tor.Protocol, tor.InfoHash = "", core.ProtocolTorrent, hashA
	tor.Grabs = 5000
	cfg := testCfg()
	cfg.Resolver.VerifiedFiles = "tie"
	svc := New(&fakeIndexer{releases: []core.Release{tor, alkoRelease("a1", web, 3000)}}, fakeHealth{}, nil, cfg, Options{})
	if got := resolveOrder(t, svc, matrixTitle()); got[0] != "sfr-alko:a1" {
		t.Fatalf("ordre %v, l'Alkodique devait mener à qualité égale", got)
	}
}

func TestVerifiedFileFailureFallsBack(t *testing.T) {
	cfg := testCfg()
	svc := New(&fakeIndexer{releases: []core.Release{remuxTorrent(), alkoRelease("a1", "The.Signal.1999.1080p.WEB-DL.DDP5.1.H.264-ALKO", 6000)}},
		fakeHealth{unhealthy: map[string]bool{"sfr-alko:a1": true}}, nil, cfg, Options{})
	res, err := svc.Resolve(context.Background(), core.ResolveRequest{Title: matrixTitle(), Client: webCaps()})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if res.Release.ID != "torrent-remux" {
		t.Fatalf("lecture %s, attendu le candidat suivant après l'échec de l'Alkodique", res.Release.ID)
	}
}

func TestMarkVerifiedFileGates(t *testing.T) {
	good := candidate{rel: alkoRelease("a1", "x", 1), langOK: true}
	tests := []struct {
		name       string
		mode       string
		mutate     func(*candidate)
		first, tie bool
	}{
		{"éligible first", "first", func(*candidate) {}, true, false},
		{"éligible tie", "tie", func(*candidate) {}, false, true},
		{"off", "off", func(*candidate) {}, false, false},
		{"hors langue", "first", func(c *candidate) { c.langOK = false }, false, false},
		{"sous le score minimal", "first", func(c *candidate) { c.belowScore = true }, false, false},
		{"sous la qualité préférée", "first", func(c *candidate) { c.fallback = true }, false, false},
		{"règle avoid", "first", func(c *candidate) { c.policy.Fallback = 1 }, false, false},
		{"identité faible", "first", func(c *candidate) { c.identityWeak = true }, false, false},
		{"débit insuffisant", "first", func(c *candidate) { c.bandwidth = -1 }, false, false},
		{"taille inconnue", "first", func(c *candidate) { c.rel.Size = 0 }, false, false},
		{"torrent ordinaire", "first", func(c *candidate) { c.rel = remuxTorrent() }, false, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := good
			tc.mutate(&c)
			markVerifiedFile(&c, tc.mode)
			if c.vfFirst != tc.first || c.vfTie != tc.tie {
				t.Fatalf("vfFirst=%v vfTie=%v, attendu %v %v", c.vfFirst, c.vfTie, tc.first, tc.tie)
			}
		})
	}
}

// countingIndexer compte les NZB/descripteurs préparés par release.
type countingIndexer struct {
	*fakeIndexer
	mu      sync.Mutex
	fetched map[string]int
}

func (c *countingIndexer) FetchNZB(ctx context.Context, r core.Release) ([]byte, error) {
	c.mu.Lock()
	if c.fetched == nil {
		c.fetched = map[string]int{}
	}
	c.fetched[r.ID]++
	c.mu.Unlock()
	return c.fakeIndexer.FetchNZB(ctx, r)
}

func (c *countingIndexer) count(id string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.fetched[id]
}

func hdTorrent(id, name string, mb int64) core.Release {
	r := rel(id, name, mb)
	r.NZBURL, r.Protocol, r.InfoHash = "", core.ProtocolTorrent, hashA
	return r
}

// Un préchargement (contexte spéculatif) ne prépare aucun torrent devant
// l'Alkodique : il s'arrête sur le lien à la demande, puis la Lecture relance
// la résolution et ouvre l'Alkodique.
func TestVerifiedFileSpeculativeWarmThenPlay(t *testing.T) {
	idx := &countingIndexer{fakeIndexer: &fakeIndexer{releases: []core.Release{
		hdTorrent("tor-hd", "The.Signal.1999.1080p.WEB-DL.DDP5.1.H.264-GRP", 8000),
		alkoRelease("a1", "The.Signal.1999.720p.WEB-DL.DDP5.1.H.264-ALKO", 3000),
	}}}
	svc := New(idx, fakeHealth{}, nil, testCfg(), Options{})
	spec, done := core.WithSpeculative(context.Background())
	defer done()
	_, err := svc.Resolve(spec, core.ResolveRequest{Title: matrixTitle(), Client: webCaps()})
	if !errors.Is(err, core.ErrForegroundOnly) {
		t.Fatalf("préchargement: err=%v, attendu ErrForegroundOnly", err)
	}
	if n := idx.count("tor-hd"); n != 0 {
		t.Fatalf("torrent préparé %d fois pendant le préchargement", n)
	}
	res, err := svc.Resolve(context.Background(), core.ResolveRequest{Title: matrixTitle(), Client: webCaps()})
	if err != nil || res.Release.ID != "sfr-alko:a1" {
		t.Fatalf("lecture: res=%+v err=%v", res, err)
	}
	if n := idx.count("tor-hd"); n != 0 {
		t.Fatalf("torrent préparé %d fois à la lecture", n)
	}
}

func TestVerifiedFilesAllFailFallBackToTorrent(t *testing.T) {
	cfg := testCfg()
	cfg.Resolver.MaxCandidates = 4
	svc := New(&fakeIndexer{releases: []core.Release{
		hdTorrent("tor-hd", "The.Signal.1999.1080p.WEB-DL.DDP5.1.H.264-GRP", 8000),
		alkoRelease("a1", "The.Signal.1999.1080p.WEB-DL.DDP5.1.H.264-ALKO", 6000),
		alkoRelease("a2", "The.Signal.1999.1080p.BluRay.x264.DTS-ALKO", 9000),
	}}, fakeHealth{unhealthy: map[string]bool{"sfr-alko:a1": true, "sfr-alko:a2": true}}, nil, cfg, Options{})
	res, err := svc.Resolve(context.Background(), core.ResolveRequest{Title: matrixTitle(), Client: webCaps()})
	if err != nil || res.Release.ID != "tor-hd" {
		t.Fatalf("res=%+v err=%v", res, err)
	}
}

func TestVerifiedFileEpisode(t *testing.T) {
	title := &core.Title{
		Key: core.EpisodeKey(1396, 1, 1), Type: core.TypeEpisode, ShowIDs: core.IDs{TMDB: 1396},
		ShowTitle: "Boundary Bay", Title: "Pilot", Season: 1, Episode: 1, Runtime: 45 * time.Minute,
	}
	for _, tc := range []struct{ mode, want string }{{"first", "sfr-alko:e1"}, {"off", "tor-ep"}} {
		t.Run(tc.mode, func(t *testing.T) {
			cfg := testCfg()
			cfg.Resolver.VerifiedFiles = tc.mode
			svc := New(&fakeIndexer{releases: []core.Release{
				hdTorrent("tor-ep", "Boundary.Bay.S01E01.1080p.WEB-DL.DDP5.1.H.264-GRP", 2500),
				alkoRelease("e1", "Boundary.Bay.S01E01.720p.WEB-DL.DDP5.1.H.264-ALKO", 1200),
			}}, fakeHealth{}, nil, cfg, Options{})
			res, err := svc.Resolve(context.Background(), core.ResolveRequest{Title: title, Client: webCaps()})
			if err != nil || res.Release.ID != tc.want {
				t.Fatalf("res=%+v err=%v", res, err)
			}
		})
	}
}

// Un Alkodique hors langue ne passe pas devant : la langue acceptée du profil
// ne retient que l'anglais, l'Alkodique est taggé FRENCH.
func TestVerifiedFileOutOfLanguageNotPromoted(t *testing.T) {
	cfg := testCfg()
	for i := range cfg.Quality.Profiles {
		cfg.Quality.Profiles[i].PreferredLanguages = []string{"en"}
	}
	title := matrixTitle()
	title.Language = "en"
	svc := New(&fakeIndexer{releases: []core.Release{
		hdTorrent("tor-hd", "The.Signal.1999.1080p.WEB-DL.ENGLISH.DDP5.1.H.264-GRP", 8000),
		alkoRelease("a1", "The.Signal.1999.FRENCH.1080p.WEB-DL.DDP5.1.H.264-ALKO", 6000),
	}}, fakeHealth{}, nil, cfg, Options{})
	res, err := svc.Resolve(context.Background(), core.ResolveRequest{Title: title, Client: webCaps()})
	if err != nil || res.Release.ID != "tor-hd" {
		t.Fatalf("res=%+v err=%v", res, err)
	}
}

// "tie" : le routage des sources s'applique entre pairs de même qualité, donc
// avec une préférence explicite un torrent connu en cache (équivalent prêt)
// passe devant l'Alkodique. Avec "first", l'Alkodique garde la tête.
func TestVerifiedFileTieRespectsSourceRouting(t *testing.T) {
	web := "The.Signal.1999.1080p.WEB-DL.DDP5.1.H.264-GRP"
	for mode, want := range map[string]string{"tie": "tor-hd", "first": "sfr-alko:a1"} {
		t.Run(mode, func(t *testing.T) {
			tor := hdTorrent("tor-hd", web, 6000)
			tor.Grabs = 5000
			cfg := testCfg()
			cfg.Resolver.VerifiedFiles = mode
			cfg.Resolver.SourcePreference = "debrid"
			svc := New(&fakeIndexer{releases: []core.Release{tor, alkoRelease("a1", web, 6000)}}, fakeHealth{}, nil, cfg, Options{})
			svc.SetSourceSignals(cachedSet{hashA: true})
			if got := resolveOrder(t, svc, matrixTitle()); got[0] != want {
				t.Fatalf("ordre %v, tête attendue %s", got, want)
			}
		})
	}
}

// Cas L'Odyssée : profil 1080p, versions plus hautes et alternatives par
// résolution. Auto et le créneau 1080p sont des Alkodiques ; le créneau 4K
// explicite reste le 2160p.
func TestVerifiedFileVersionMenu(t *testing.T) {
	cfg := testCfg()
	cfg.Resolver.OfferHigherVersions = true
	cfg.Resolver.AlternativesPerResolution = true
	cfg.Resolver.SameQualityAlternatives = 1
	cfg.Resolver.MaxCandidates = 4
	idx := &fakeIndexer{releases: []core.Release{
		hdTorrent("tor-uhd", "The.Signal.1999.2160p.WEB-DL.H.265-GRP", 20000),
		hdTorrent("tor-hd", "The.Signal.1999.1080p.WEB-DL.DDP5.1.H.264-GRP", 8000),
		hdTorrent("tor-hd2", "The.Signal.1999.1080p.BluRay.x265.DTS-HD.MA.5.1-GRP", 12000),
		alkoRelease("a1", "The.Signal.1999.1080p.WEB-DL.DDP5.1.H.264-ALKO", 6000),
	}}
	svc := New(idx, fakeHealth{}, nil, cfg, Options{})
	caps := webCaps()
	caps.MaxHeight, caps.VideoCodecs = 2160, []string{"h264", "hevc"}
	primeSearchCache(t, svc, caps)
	req := core.ResolveRequest{Title: matrixTitle(), Client: caps}
	vs, err := svc.Variants(context.Background(), req, core.MaxVariants)
	if err != nil || len(vs) == 0 {
		t.Fatalf("variants: %v %v", vs, err)
	}
	var ids []string
	has2160, hasAlko1080 := false, false
	for _, v := range vs {
		ids = append(ids, v.ID)
		if v.Release.Info.Height == 2160 && v.Release.ID == "tor-uhd" {
			has2160 = true
		}
		if v.Release.Info.Height == 1080 && v.Release.ID == "sfr-alko:a1" {
			hasAlko1080 = true
		}
	}
	if !vs[0].Auto || vs[0].Release.ID != "sfr-alko:a1" {
		t.Fatalf("Auto = %s (%v), attendu l'Alkodique", vs[0].Release.ID, ids)
	}
	if !has2160 || !hasAlko1080 {
		t.Fatalf("menu %v : créneau 4K explicite=%v, créneau 1080p Alkodique=%v", ids, has2160, hasAlko1080)
	}
}
