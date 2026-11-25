package mediabrowser

// Ajout Remix : le montage apparaît dans le nom de version (voir docs/pse/FORK.fr.md).

import (
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/frontend"
)

func TestPSEEditionInMediaSourceName(t *testing.T) {
	v := frontend.ReleaseVariant{Release: frontend.Release{Size: 7 << 30, Info: frontend.ReleaseInfo{Height: 1080, Edition: "Director's Cut"}}}
	if name := mediaSourceName(v); !strings.Contains(name, "Director's Cut") {
		t.Fatalf("montage absent du nom %q", name)
	}
}
