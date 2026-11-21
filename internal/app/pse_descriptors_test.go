package app

import (
	"os"
	"regexp"
	"testing"
)

// La liste Go des entrées du groupe Remix doit rester identique à REMIX_NAV
// (identifiants, icônes, adresses et ordre) : la console les traduit et les filtre.
func TestPSENavItemsMatchConsole(t *testing.T) {
	src, err := os.ReadFile("../../web/src/pse/navigation.tsx")
	if err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile(`id:\s*"(pse-[a-z]+)",\s*tab:\s*"([a-z]+)",\s*fr:\s*"[^"]*",\s*en:\s*"[^"]*",\s*icon:\s*"([A-Za-z]+)"`)
	got := re.FindAllStringSubmatch(string(src), -1)
	if len(got) != len(pseNavItems) {
		t.Fatalf("%d entrées côté console, %d côté Go", len(got), len(pseNavItems))
	}
	for i, m := range got {
		it := pseNavItems[i]
		if m[1] != it.id || "/remix/"+m[2] != it.route || m[3] != it.icon {
			t.Errorf("entrée %d : console %v, Go %+v", i, m[1:], it)
		}
	}
}
