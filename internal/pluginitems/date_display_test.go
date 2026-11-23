package pluginitems

import (
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/plugins"
)

func TestProjectItemDateDisplayAndReleased(t *testing.T) {
	ts := time.Date(2026, 4, 15, 18, 30, 0, 0, time.UTC).Unix()
	item := core.KindItem{
		Key:      core.PluginKey(99),
		Level:    "video",
		Title:    "Match Day 1",
		Released: ts,
	}

	dateDecl := plugins.MediaKindMeta{
		Key: "streams",
		Library: plugins.KindLibraryDecl{
			Name:        "Streams",
			Card:        core.CardLandscape,
			DateDisplay: core.DateDisplayDate,
		},
		Levels: []plugins.KindLevel{{Key: "video", Playable: true}},
	}
	gotDate := Project(item, dateDecl, core.Presentations{})
	if gotDate.DateDisplay != core.DateDisplayDate {
		t.Fatalf("DateDisplay = %q, want %q", gotDate.DateDisplay, core.DateDisplayDate)
	}
	if gotDate.Released.UTC().Format("2006-01-02") != "2026-04-15" {
		t.Fatalf("Released = %v, want 2026-04-15", gotDate.Released)
	}
	if gotDate.Year != 2026 {
		t.Fatalf("Year = %d, want 2026", gotDate.Year)
	}

	yearDecl := dateDecl
	yearDecl.Library.DateDisplay = ""
	gotYear := Project(item, yearDecl, core.Presentations{})
	if gotYear.DateDisplay != "" {
		t.Fatalf("default DateDisplay = %q, want empty", gotYear.DateDisplay)
	}
}
