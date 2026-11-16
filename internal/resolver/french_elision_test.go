package resolver

import (
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/releaseparse"
	"github.com/braedonsaunders/ether/internal/releasepolicy"
	"strings"
	"testing"
)

func TestFrenchMovieElisionsAndCorroboratedPart(t *testing.T) {
	title := &core.Title{Key: core.MovieKey(123), IDs: core.IDs{TMDB: 123}, Type: core.TypeMovie, Title: "La Bataille de Gaulle : L’Âge de fer", Year: 2026, OriginalTitle: "De Gaulle Part 1: Resistance", AlternativeTitles: []string{"De Gaulle Part I: Resistance"}}
	cfg := testCfg()
	svc := New(&fakeIndexer{}, fakeHealth{}, nil, cfg, Options{})
	profile := SelectProfile(cfg.Quality, webCaps(), nil)
	names := []string{
		"La.Bataille.de.Gaulle.L.Age.de.Fer.2026.VOF.1080p.BluRay.Remux.AVC.TrueHD.Atmos.7.1-ALN",
		"la.bataille.de.gaulle.l.age.de.fer.2026.french.1080p.bluray.x264-ulysse",
		"La.Bataille.De.Gaulle.Partie.1.L.Age.De.Fer.2026.FRENCH.1080p.BluRay.x264-SPQR",
		"la.bataille.de.gaulle.l.age.de.fer.2026.french.720p.bluray.x264-ulysse",
		"La.Bataille.De.Gaulle.L.Age.De.Fer.2026.FRENCH.720p.WEB.H264-SUPPLY",
	}
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			r := rel("r", name, 1500)
			r.Info = releaseparse.Parse(name)
			if got := svc.rejectReason(r, title, searchRequestFor(title), profile, cfg.Resolver, webCaps(), nil, false); got != "" {
				t.Fatalf("parsed=%+v rejected=%s", r.Info, got)
			}
			if _, err := svc.scoreRelease(r, profile, webCaps(), title, releasepolicy.Result{}, formatFacts{}); err != nil {
				t.Fatalf("accepted release cannot be scored: %v", err)
			}
		})
	}
	for _, name := range []string{strings.Replace(names[2], "Partie.1", "Partie.2", 1), strings.Replace(names[0], "2026", "2010", 1), strings.Replace(names[0], "Fer", "Feu", 1)} {
		r := rel("r", name, 1500)
		r.Info = releaseparse.Parse(name)
		if svc.rejectReason(r, title, searchRequestFor(title), profile, cfg.Resolver, webCaps(), nil, false) == "" {
			t.Fatalf("wrong movie accepted: %s", name)
		}
	}
	r := rel("r", names[0], 1500)
	r.Info = releaseparse.Parse(r.Name)
	r.TitleIDs = core.IDs{TMDB: 456}
	if got := svc.rejectReason(r, title, searchRequestFor(title), profile, cfg.Resolver, webCaps(), nil, false); got != "indexer title identifier mismatch" {
		t.Fatalf("wrong ID accepted: %s", got)
	}
	if releaseparse.MovieTitleMatches(releaseparse.Parse(names[2]), []string{title.Title}) {
		t.Fatal("unconfirmed part accepted")
	}
}
