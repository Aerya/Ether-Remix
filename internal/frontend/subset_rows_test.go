package frontend

import (
	"reflect"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
)

func TestSubsetParents(t *testing.T) {
	views := []View{
		{ID: "1", LibraryID: core.LibraryMovies, Name: "Movies", Type: core.TypeMovie},
		{ID: "3", LibraryID: 3, Name: "Kids Movies", Type: core.TypeMovie, Subset: true},
		{ID: "4", LibraryID: 4, Name: "Kids Shows", Type: core.TypeShow, Subset: true},
		{ID: "2", LibraryID: core.LibraryShows, Name: "TV Shows", Type: core.TypeShow},
		{ID: "9", LibraryID: 9, Name: "Studio", Type: core.TypeMovie, PluginID: "plug", KindKey: "clip"},
	}
	got := SubsetParents(views)
	want := map[string]string{"3": "1", "4": "2"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("SubsetParents = %v, want %v", got, want)
	}

	// Kid viewer or sibling rule subsets without a broader parent are never linked.
	siblings := []View{
		{ID: "3", LibraryID: 3, Name: "Kids Movies", Type: core.TypeMovie, Subset: true},
		{ID: "5", LibraryID: 5, Name: "Anime Movies", Type: core.TypeMovie, Subset: true},
	}
	if got := SubsetParents(siblings); len(got) != 0 {
		t.Fatalf("sibling rule subsets should have no parent, got %v", got)
	}

	// Unscoped custom library serves as parent when built-in is absent.
	customParent := []View{
		{ID: "5", LibraryID: 5, Name: "All Movies", Type: core.TypeMovie, Subset: false},
		{ID: "3", LibraryID: 3, Name: "Kids Movies", Type: core.TypeMovie, Subset: true},
	}
	if got := SubsetParents(customParent); !reflect.DeepEqual(got, map[string]string{"3": "5"}) {
		t.Fatalf("custom unscoped parent = %v, want 3->5", got)
	}
}

func TestSubsetRowDistinct(t *testing.T) {
	parentSame := map[int]bool{1: true, 2: true, 3: true, 4: true, 5: true}
	parentAll := map[int]bool{1: true, 2: true, 3: true, 4: true, 5: true, 6: true}

	// 5 of 10 overlap (50%) -> does not differ meaningfully.
	if SubsetRowDistinct([]int{1, 2, 3, 4, 5, 11, 12, 13, 14, 15}, parentSame, parentAll) {
		t.Fatal("50% overlap should not be treated as distinct")
	}
	// 4 of 10 overlap (60% novel) -> differs meaningfully.
	if !SubsetRowDistinct([]int{1, 2, 3, 4, 11, 12, 13, 14, 15, 16}, parentSame, parentAll) {
		t.Fatal("60% novel should be treated as distinct")
	}
	// Low overlap with same row (2/4), but 2/4 overlap with parentAll -> 50% repeated -> drop.
	if SubsetRowDistinct([]int{1, 6, 11, 12}, map[int]bool{1: true}, parentAll) {
		t.Fatal("50% overlap with parentAll should be dropped")
	}
	// Small 1-item row: distinct when novel, dropped when repeated.
	if !SubsetRowDistinct([]int{99}, parentSame, parentAll) {
		t.Fatal("single novel item should be distinct")
	}
	if SubsetRowDistinct([]int{1}, parentSame, parentAll) {
		t.Fatal("single repeated item should not be distinct")
	}
	if SubsetRowDistinct([]int(nil), parentSame, parentAll) {
		t.Fatal("empty row should not be distinct")
	}
}

func TestQualifyLibraryRowTitle(t *testing.T) {
	cases := []struct {
		title, lib, want string
	}{
		{"Hidden Gems for You", "Kids Movies", "Hidden Gems for You · Kids Movies"},
		{"Recently Added in TV Shows", "TV Shows", "Recently Added in TV Shows"},
		{"Top Kids Movies", "Kids Movies", "Top Kids Movies"},
		{"Popular Movies", "Kids Movies", "Popular Movies · Kids Movies"},
		{"MTV Classics", "TV", "MTV Classics · TV"},
		{"Hidden Gems for You · Kids Movies", "Kids Movies", "Hidden Gems for You · Kids Movies"},
	}
	for _, tc := range cases {
		if got := QualifyLibraryRowTitle(tc.title, tc.lib); got != tc.want {
			t.Errorf("QualifyLibraryRowTitle(%q, %q) = %q, want %q", tc.title, tc.lib, got, tc.want)
		}
	}
}

func TestAllocateRowBudget(t *testing.T) {
	if got := AllocateRowBudget([]int{18, 3, 2, 14}, 20); !reflect.DeepEqual(got, []int{8, 3, 2, 7}) {
		t.Fatalf("water-fill = %v, want [8 3 2 7]", got)
	}
	if got := AllocateRowBudget([]int{18, 14}, 20); !reflect.DeepEqual(got, []int{10, 10}) {
		t.Fatalf("even split = %v, want [10 10]", got)
	}
	if got := AllocateRowBudget([]int{18, 0, 0, 14}, 20); !reflect.DeepEqual(got, []int{10, 0, 0, 10}) {
		t.Fatalf("empty subsets = %v, want [10 0 0 10]", got)
	}
}
