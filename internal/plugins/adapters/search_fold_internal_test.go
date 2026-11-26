package adapters

import (
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
)

func TestScorePluginSearchHitFoldsCaseAndAccents(t *testing.T) {
	titles := []core.Title{
		{Title: "James Bond"},
		{Title: "James Bond Theme"},
		{Title: "The Music of James Bond"},
		{Title: "Amélie"},
		{Title: "Amélie Poulain Suite"},
	}
	for _, group := range [][]string{
		{"james bond", "James Bond", "JAMES BOND"},
		{"amelie", "Amélie", "AMÉLIE"},
	} {
		for _, title := range titles {
			want := scorePluginSearchHit(title, core.KindItem{}, group[0])
			for _, q := range group[1:] {
				if got := scorePluginSearchHit(title, core.KindItem{}, q); got != want {
					t.Errorf("score(%q, %q) = %v, want %v as for %q", title.Title, q, got, want, group[0])
				}
			}
		}
	}
	if scorePluginSearchHit(core.Title{Title: "Amélie"}, core.KindItem{}, "amelie") != scorePluginSearchHit(core.Title{Title: "Amélie"}, core.KindItem{}, "Amélie") {
		t.Fatal("an unaccented query must score as the accented exact title")
	}
}
