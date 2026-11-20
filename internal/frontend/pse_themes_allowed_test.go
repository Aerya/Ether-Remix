package frontend

import (
	"slices"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
)

func TestThemeAllowed(t *testing.T) {
	ids := func(v ...int) []core.LibraryID {
		var out []core.LibraryID
		for _, x := range v {
			out = append(out, core.LibraryID(x))
		}
		return out
	}
	cases := []struct {
		name   string
		viewer *core.User
		sec    int
		want   bool
	}{
		{"nil", nil, 5101, true},
		{"propriétaire", &core.User{IsOwner: true, AllowedLibraries: ids(1)}, 5101, true},
		{"liste vide", &core.User{}, 5101, true},
		{"thème présent", &core.User{AllowedLibraries: ids(1, 5101)}, 5101, true},
		{"autre thème coché", &core.User{AllowedLibraries: ids(1, 5102)}, 5101, false},
		{"ancien réglage", &core.User{AllowedLibraries: ids(1, 2)}, 5103, true},
		{"moitié Séries suit le principal", &core.User{AllowedLibraries: ids(2, 5101)}, 5201, true},
		{"moitié Séries refusée", &core.User{AllowedLibraries: ids(2, 5102)}, 5201, false},
		{"5201 stocké traité comme 5101", &core.User{AllowedLibraries: ids(2, 5201)}, 5101, true},
	}
	for _, c := range cases {
		if got := ThemeAllowed(c.viewer, c.sec); got != c.want {
			t.Errorf("%s : %v, attendu %v", c.name, got, c.want)
		}
	}
}

func TestNormalizeAllowedLibraries(t *testing.T) {
	got := NormalizeAllowedLibraries([]core.LibraryID{1, 5201, 5101, 5102, 1})
	if want := []core.LibraryID{1, 5101, 5102}; !slices.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	if NormalizeAllowedLibraries(nil) != nil {
		t.Fatal("nil attendu")
	}
}
