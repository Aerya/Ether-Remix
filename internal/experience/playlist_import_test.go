package experience

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
)

type importLibraries struct {
	recapLibraries
	asked int
}

func (l *importLibraries) Library(_ context.Context, id core.LibraryID) (core.Library, error) {
	return core.Library{ID: id, Source: core.LibraryPlugin}, nil
}
func (l *importLibraries) Libraries(context.Context) ([]core.Library, error) {
	return []core.Library{{ID: 9, Source: core.LibraryPlugin}}, nil
}
func (l *importLibraries) ImportSupport(context.Context, core.Library) ([]string, bool) {
	return []string{"deezer"}, true
}
func (l *importLibraries) ImportLink(context.Context, core.Library, string) (core.KindImportResponse, error) {
	return core.KindImportResponse{}, core.ErrNotFound
}

// Answers the first three tracks; never the fourth (asked again, then left out).
func (l *importLibraries) IdentifyTracks(_ context.Context, _ core.Library, tracks []core.ImportTrack) ([]core.ImportMatch, error) {
	l.asked++
	var out []core.ImportMatch
	for i, t := range tracks {
		switch t.Title {
		case "One More Time":
			out = append(out, core.ImportMatch{Index: i, Key: core.PluginKey(1), By: "isrc"})
		case "Digital Love":
			out = append(out, core.ImportMatch{Index: i, Key: core.PluginKey(2), By: "title"})
		case "Aerodynamic":
			out = append(out, core.ImportMatch{Index: i, Candidates: []core.RatingKey{core.PluginKey(3), core.PluginKey(4)}})
		}
	}
	return out, nil
}

func TestPlaylistImportMatchesAndReportsHonestly(t *testing.T) {
	ctx := context.Background()
	viewer := &core.User{ID: 6, AllowedLibraries: []core.LibraryID{9}}
	lib := core.Library{ID: 9, Source: core.LibraryPlugin}
	views := map[core.RatingKey]PluginItemView{}
	for seq := int64(1); seq <= 4; seq++ {
		views[core.PluginKey(seq)] = PluginItemView{Library: lib, Title: core.Title{Key: core.PluginKey(seq), Title: "t", LibraryID: 9}}
	}
	libs := &importLibraries{recapLibraries: recapLibraries{views: views}}
	svc := &Service{d: ServiceDeps{Active: func(context.Context, int64) (*core.User, error) { return viewer, nil }, PluginLibraries: libs}}
	if links, files := svc.PlaylistImportSupport(ctx); !files || len(links) != 1 {
		t.Fatalf("support: %v %v", links, files)
	}
	csv := "Track Name,Artist Name(s),ISRC\nOne More Time,Daft Punk,GBDUW0000053\nDigital Love,Daft Punk,\nAerodynamic,Daft Punk,\nUnknown Song,Nobody,\n"
	id, err := svc.StartPlaylistImport(ctx, &core.Viewer{ID: 6}, frontend.PlaylistImportRequest{Library: 9, FileName: "Road Trip.csv", FileFormat: "csv", FileText: csv})
	if err != nil {
		t.Fatal(err)
	}
	var imp frontend.PlaylistImport
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		if imp, err = svc.PlaylistImport(ctx, &core.Viewer{ID: 6}, id); err != nil || imp.State == "done" {
			break
		}
	}
	if err != nil || imp.State != "done" || imp.Name != "Road Trip" || imp.Total != 4 || imp.Done != 4 {
		t.Fatalf("import: %+v %v", imp, err)
	}
	if len(imp.Matched) != 2 || imp.Matched[0].By != "isrc" || imp.Matched[1].By != "title" {
		t.Fatalf("matched: %+v", imp.Matched)
	}
	if len(imp.Unmatched) != 2 || imp.Unmatched[0].Reason != "ambiguous" || len(imp.Unmatched[0].Candidates) != 2 || imp.Unmatched[1].Reason != "not_found" {
		t.Fatalf("unmatched: %+v", imp.Unmatched)
	}
	if libs.asked != importMaxAsks {
		t.Fatalf("an unanswered track is asked %d times, want %d", libs.asked, importMaxAsks)
	}
	if _, err := svc.SavePlaylistImport(ctx, &core.Viewer{ID: 6}, id, "", nil, map[int]core.RatingKey{2: core.PluginKey(1)}); !errors.Is(err, core.ErrInvalidImportChoice) {
		t.Fatalf("a choice outside the candidates: %v", err)
	}
	if _, err := svc.PlaylistImport(ctx, &core.Viewer{ID: 7}, id); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("another viewer read the import: %v", err)
	}
	if _, err := svc.StartPlaylistImport(ctx, &core.Viewer{ID: 6}, frontend.PlaylistImportRequest{Library: 9, FileFormat: "csv", FileText: "a,b\n1,2\n"}); !errors.Is(err, core.ErrUnsupportedImport) {
		t.Fatalf("an unreadable file: %v", err)
	}
}
