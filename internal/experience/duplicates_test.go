package experience

import (
	"context"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
)

type dupLibraries struct {
	fakePluginLibraries
	titles []core.Title
}

func (d *dupLibraries) Library(_ context.Context, id core.LibraryID) (core.Library, error) {
	return core.Library{ID: id, Source: core.LibraryPlugin}, nil
}

func (d *dupLibraries) DuplicateTitles(_ context.Context, lib core.Library, _, _ int) ([]PluginDuplicateGroup, int, error) {
	return []PluginDuplicateGroup{{Recording: "isrc:GBDUW0000053", Count: len(d.titles), Titles: d.titles}}, 1, nil
}

func (d *dupLibraries) RecordingTitles(_ context.Context, _ core.RatingKey) (core.Library, string, []core.Title, error) {
	return core.Library{ID: 9, Source: core.LibraryPlugin}, "isrc:GBDUW0000053", d.titles, nil
}

// A viewer's preferred release hides the others from that viewer's lists,
// while duplicate review keeps listing every release.
func TestPreferRecordingHidesOtherReleasesForThatViewer(t *testing.T) {
	ctx := context.Background()
	viewer := &core.User{ID: 5, AllowedLibraries: []core.LibraryID{9}}
	album, compilation := core.PluginKey(31), core.PluginKey(32)
	titles := []core.Title{{Key: album, Title: "One More Time", LibraryID: 9}, {Key: compilation, Title: "One More Time", LibraryID: 9}}
	prefs := map[string]core.RatingKey{}
	var hidden []core.RatingKey
	svc := &Service{d: ServiceDeps{
		Active:          func(context.Context, int64) (*core.User, error) { return viewer, nil },
		PluginLibraries: &dupLibraries{titles: titles},
		RecordingPreferences: func(_ context.Context, _ int64, _ core.LibraryID, recs []string) (map[string]core.RatingKey, error) {
			return prefs, nil
		},
		SetRecordingPreference: func(_ context.Context, uid int64, _ core.LibraryID, rec string, preferred core.RatingKey, hide []core.RatingKey) error {
			prefs[rec], hidden = preferred, hide
			return nil
		},
		HiddenRecordingCopies: func(context.Context, int64) ([]core.RatingKey, error) { return hidden, nil },
	}}
	page, err := svc.LibraryDuplicates(ctx, &core.Viewer{ID: 5}, 9, "", 10)
	if err != nil || len(page.Groups) != 1 || page.Groups[0].Count != 2 {
		t.Fatalf("duplicates: %+v %v", page, err)
	}
	g, err := svc.PreferRecording(ctx, &core.Viewer{ID: 5}, 9, "isrc:GBDUW0000053", album, []core.RatingKey{compilation})
	if err != nil || g.Preferred != album {
		t.Fatalf("prefer: %+v %v", g, err)
	}
	if _, err := svc.PreferRecording(ctx, &core.Viewer{ID: 5}, 9, "isrc:GBDUW0000053", album, []core.RatingKey{core.PluginKey(99)}); err == nil {
		t.Fatal("hiding a title that is not a release of the recording was accepted")
	}
	listed, err := svc.filterPluginTitles(ctx, viewer, titles, frontend.ItemQuery{})
	if err != nil || len(listed) != 1 || listed[0].Key != album {
		t.Fatalf("lists still show the hidden release: %+v %v", listed, err)
	}
	g, err = svc.TitleRecording(ctx, &core.Viewer{ID: 5}, album)
	if err != nil || g.Count != 2 || g.Preferred != album {
		t.Fatalf("review: %+v %v", g, err)
	}
}
