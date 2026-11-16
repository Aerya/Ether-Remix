package frontend

import (
	"golang.org/x/text/language"
	"golang.org/x/text/language/display"
)

// Slots are stable across configuration changes, including empty countries.
// Adapters publish only groups that contain collections visible to the viewer.
func navigationGroups() []struct{ ID, Name string } {
	groups := []struct{ ID, Name string }{{"studios", "Studios & labels"}, {"services", "Services"}, {"genres", "Genres"}}
	for a := 'A'; a <= 'Z'; a++ {
		for b := 'A'; b <= 'Z'; b++ {
			code := string([]rune{a, b})
			name := code
			if region, err := language.ParseRegion(code); err == nil {
				name = display.French.Regions().Name(region)
			}
			groups = append(groups, struct{ ID, Name string }{"services-" + code, "Services · " + name})
		}
	}
	return groups
}

// NavigationGroupOrder keeps services and their country shelves adjacent while
// preserving the stable numerical slots used in media-browser identifiers.
func NavigationGroupOrder() []int {
	order := []int{0, 1}
	for n := 3; n < len(NavigationGroups); n++ {
		order = append(order, n)
	}
	return append(order, 2)
}
