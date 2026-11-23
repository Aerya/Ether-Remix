package debrid

import (
	"context"
	"errors"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"testing"
)

// Collection identity is independent of file size, listing order, and explicit
// addon selectors. This also exercises the shared archive-member name matcher.
func TestMoviePackIdentity(t *testing.T) {
	target := &core.ReleaseTarget{MovieTitles: []string{"The Mummy"}, MovieYear: 1999}
	files := []File{
		{Path: "The Mummy Returns.mkv", Size: 9000},
		{Path: "The Mummy (1959)/The Mummy.mkv", Size: 8000},
		{Path: "The Mummy Returns (2001).mkv", Size: 7000},
		{Path: "The Mummy (1999)/The.Mummy.1999.1080p.mkv", Size: 5000},
	}
	f, err := ChooseFile(files, target)
	if err != nil || f != files[3] {
		t.Fatalf("movie: %+v %v", f, err)
	}
	if _, err := ChooseFile(files[:3], target); !errors.Is(err, core.ErrWrongContent) {
		t.Fatalf("wrong pack accepted: %v", err)
	}
	for i := range files {
		copy := *target
		copy.FileIndex = &i
		_, err := ChooseFile(files, &copy)
		if (err == nil) != (i == 3) {
			t.Errorf("index %d: %v", i, err)
		}
	}
	copy := *target
	copy.ExactFile = files[0].Path
	if _, err := ChooseFile(files, &copy); !errors.Is(err, core.ErrWrongContent) {
		t.Fatalf("wrong explicit path: %v", err)
	}
	// A bare correctly titled member is still subject to runtime validation at Play.
	if _, err := ChooseFile([]File{{Path: "The Mummy.mkv", Size: 4000}}, target); err != nil {
		t.Fatal(err)
	}
}

func TestMovieSingleFeatureFallback(t *testing.T) {
	target := &core.ReleaseTarget{MovieTitles: []string{"The Mummy"}, MovieYear: 1999}
	for _, name := range []string{"movie.mkv", "odd.scene.name.mkv", "The Mummy Returns.mkv"} {
		files := []File{{Path: name, Size: 5000}, {Path: "sample.mkv", Size: 100}, {Path: "extras/Deleted.mkv", Size: 200}}
		f, err := ChooseFile(files, target)
		if err != nil || f.Path != name {
			t.Fatalf("single feature %s: %+v %v", name, f, err)
		}
		// Opening/replaying the chosen payload must not reapply a multi-file gate.
		if !fileIdentityMatches(f, target) {
			t.Fatalf("payload refused %s", name)
		}
	}
}

func TestMovieSingleTorrentOpen(t *testing.T) {
	h := newHarness(t, 1<<20, "realdebrid")
	h.cacheEverywhere(testHash)
	h.provs["realdebrid-0"].files = []File{{ID: "1", Path: "movie.mkv", Size: 1 << 20}, {ID: "2", Path: "sample.mkv", Size: 1000}}
	rel := torrentRelease("single-movie")
	rel.Target = &core.ReleaseTarget{MovieTitles: []string{"The Mummy"}, MovieYear: 1999}
	payload, err := h.svc.Acquire(context.Background(), rel)
	if err != nil {
		t.Fatal(err)
	}
	e := NewEngine(h.svc, EngineOptions{Logger: h.svc.log})
	defer e.Close()
	src, err := e.Open(context.Background(), rel.ID, payload)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	if src.Name() != "movie.mkv" {
		t.Fatal(src.Name())
	}
}

func TestMovieSingleAddonStream(t *testing.T) {
	r := newDirectRig(t, config.Debrid{CachedOnly: true})
	rel := r.release("same-origin.mkv")
	rel.Target = &core.ReleaseTarget{MovieTitles: []string{"The Mummy"}, MovieYear: 1999}
	raw, err := r.svc.DirectPayload(context.Background(), rel)
	if err != nil {
		t.Fatal(err)
	}
	e := NewEngine(r.svc, EngineOptions{})
	defer e.Close()
	src, err := e.Open(context.Background(), rel.ID, raw)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	if src.Name() != "same-origin.mkv" {
		t.Fatal(src.Name())
	}
}

func TestMoviePackAudioLanguagePreference(t *testing.T) {
	const gb = int64(1 << 30)
	const mb = int64(1 << 20)

	dual := []File{
		{ID: "en", Path: "The.Mummy.1999.Pack/The.Mummy.1999.ENGLISH.1080p.mkv", Size: 6 * gb},
		{ID: "fr", Path: "The.Mummy.1999.Pack/The.Mummy.1999.FRENCH.1080p.mkv", Size: 5 * gb},
		{ID: "fr-clip", Path: "The.Mummy.1999.Pack/The.Mummy.1999.FRENCH.mkv", Size: 100 * mb},
	}

	// French profile prefers the 5 GiB French feature over the larger 6 GiB English file.
	gotFR, err := ChooseFile(dual, &core.ReleaseTarget{
		MovieTitles:      []string{"The Mummy"},
		MovieYear:        1999,
		WantedLanguages:  []string{"french", "english"},
		OriginalLanguage: "english",
	})
	if err != nil || gotFR.ID != "fr" {
		t.Fatalf("French preference: got %+v err=%v, want fr", gotFR, err)
	}

	// Unmatched language falls back to largest full-length file.
	gotFallback, err := ChooseFile([]File{dual[0], dual[1]}, &core.ReleaseTarget{
		MovieTitles:     []string{"The Mummy"},
		MovieYear:       1999,
		WantedLanguages: []string{"japanese"},
	})
	if err != nil || gotFallback.ID != "en" {
		t.Fatalf("Fallback to largest: got %+v err=%v, want en", gotFallback, err)
	}

	// A tiny clip (<35% of feature size) never wins over a full-length feature.
	gotClipGuard, err := ChooseFile([]File{dual[0], dual[2]}, &core.ReleaseTarget{
		MovieTitles:      []string{"The Mummy"},
		MovieYear:        1999,
		WantedLanguages:  []string{"french"},
		OriginalLanguage: "english",
	})
	if err != nil || gotClipGuard.ID != "en" {
		t.Fatalf("Tiny clip guard: got %+v err=%v, want en", gotClipGuard, err)
	}

	// Foreign film with untagged original + larger English dub: "Original"
	// preference picks the untagged Japanese file; English preference picks the dub.
	foreign := []File{
		{ID: "dub", Path: "Spirited.Away.2001.ENGLISH.DUBBED.1080p.mkv", Size: 6 * gb},
		{ID: "orig", Path: "Spirited.Away.2001.1080p.mkv", Size: 5 * gb},
	}
	gotOrig, err := ChooseFile(foreign, &core.ReleaseTarget{
		MovieTitles:      []string{"Spirited Away"},
		MovieYear:        2001,
		WantedLanguages:  []string{"japanese", "english"},
		OriginalLanguage: "japanese",
	})
	if err != nil || gotOrig.ID != "orig" {
		t.Fatalf("Original Japanese preference: got %+v err=%v, want orig", gotOrig, err)
	}
	gotDub, err := ChooseFile(foreign, &core.ReleaseTarget{
		MovieTitles:      []string{"Spirited Away"},
		MovieYear:        2001,
		WantedLanguages:  []string{"english", "japanese"},
		OriginalLanguage: "japanese",
	})
	if err != nil || gotDub.ID != "dub" {
		t.Fatalf("English dub preference: got %+v err=%v, want dub", gotDub, err)
	}

	// Subfolder language layout and title-word language ("The French Dispatch").
	subfolders := []File{
		{ID: "de", Path: "The French Dispatch (2021)/German/The.French.Dispatch.2021.1080p.mkv", Size: 6 * gb},
		{ID: "en", Path: "The French Dispatch (2021)/English/The.French.Dispatch.2021.1080p.mkv", Size: 5 * gb},
	}
	gotDispatch, err := ChooseFile(subfolders, &core.ReleaseTarget{
		MovieTitles:      []string{"The French Dispatch"},
		MovieYear:        2021,
		WantedLanguages:  []string{"french", "english"},
		OriginalLanguage: "english",
	})
	if err != nil || gotDispatch.ID != "en" {
		t.Fatalf("Title-word language protection: got %+v err=%v, want en", gotDispatch, err)
	}
}
