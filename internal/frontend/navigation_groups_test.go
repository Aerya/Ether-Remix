package frontend

import "testing"

func TestNavigationCountrySlotsStable(t *testing.T) {
	if NavigationGroups[0].ID != "studios" || NavigationGroups[1].ID != "services" || NavigationGroups[2].ID != "genres" {
		t.Fatal("legacy IDs changed")
	}
	seen := map[string]bool{}
	for _, g := range NavigationGroups {
		if seen[g.ID] {
			t.Fatal("duplicate", g.ID)
		}
		seen[g.ID] = true
	}
	if !seen["services-FR"] || !seen["services-US"] {
		t.Fatal("missing countries")
	}
	order := NavigationGroupOrder()
	if len(order) != len(NavigationGroups) || order[len(order)-1] != 2 {
		t.Fatal("wrong ordering")
	}
}
