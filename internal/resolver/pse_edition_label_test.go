package resolver

// Ajout Remix : le montage apparaît dans le libellé des versions (voir docs/pse/FORK.fr.md).

import (
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
)

func TestPSEEditionInVersionLabels(t *testing.T) {
	rel := core.Release{Size: 7 << 30, Info: core.ReleaseInfo{Height: 1080, Source: core.SourceBluRay, AudioCodec: "ac3", Channels: "5.1", Group: "SEL", Edition: "Director's Cut"}}
	for name, label := range map[string]string{"alt": alternativeLabel(rel), "auto": autoLabel(rel)} {
		if !strings.Contains(label, "Director's Cut") {
			t.Fatalf("%s : montage absent du libellé %q", name, label)
		}
	}
	rel.Info.Edition = ""
	if strings.Contains(alternativeLabel(rel), "·  ·") {
		t.Fatal("libellé sans montage mal formé")
	}
}
