package plugins

import (
	"strconv"
	"strings"

	"github.com/braedonsaunders/ether/internal/stremio"
)

// MaxListItems is the most titles one sync of a plugin list reads: the
// ceiling for listSources[].maxItems and for the owner's grant.
const MaxListItems = stremio.MaxCatalogItems

// DefaultListItems is what one sync of a list that declares no maxItems
// reads. The install review shows it like any declared size.
const DefaultListItems = 500

// RequestedItems is how many titles one sync of the list asks to read:
// its declared maxItems, or DefaultListItems when it declares none.
func (ls ListSourceMeta) RequestedItems() int {
	if ls.MaxItems > 0 {
		return min(ls.MaxItems, MaxListItems)
	}
	return DefaultListItems
}

// ListSource finds the list source with the given key.
func (m Manifest) ListSource(key string) (ListSourceMeta, bool) {
	e := m.EffectiveEther()
	if e == nil || !m.HasCapability(CapListSource) {
		return ListSourceMeta{}, false
	}
	for _, ls := range e.Contributes.ListSources {
		if ls.Key == key {
			return ls, true
		}
	}
	return ListSourceMeta{}, false
}

// ListItemsRequested is the most titles any of the plugin's lists asks
// to sync per refresh, or 0 when it has no lists. It is the figure the
// install and update reviews show and the owner grants.
func (m Manifest) ListItemsRequested() int {
	e := m.EffectiveEther()
	if e == nil || !m.HasCapability(CapListSource) {
		return 0
	}
	n := 0
	for _, ls := range e.Contributes.ListSources {
		if ls.Key != "" {
			n = max(n, ls.RequestedItems())
		}
	}
	return n
}

// listItemsLine is the review line for a list size: "Syncs up to 2,000
// titles per list". The console translates it by its prefix.
func listItemsLine(n int) string {
	return ListItemsLinePrefix + groupThousands(n) + " titles per list"
}

// ListItemsLinePrefix starts the review line listItemsLine writes.
const ListItemsLinePrefix = "Syncs up to "

func groupThousands(n int) string {
	s := strconv.Itoa(n)
	var b strings.Builder
	for i, r := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// ListItemsGrant is a plugin's list sync size as the reviews and the
// plugin drawer show it: what its largest list asks for and what the
// owner allows.
type ListItemsGrant struct {
	Requested int `json:"requested"`
	Granted   int `json:"granted"`
}

// GrantedListItems is how many titles one sync of the plugin's list ls
// reads: what the list declares, within the owner's global ceiling, or
// the owner's per-plugin limit when one is set, which replaces the
// request in either direction like every other per-plugin limit.
func (m *Manager) GrantedListItems(pluginID string, ls ListSourceMeta) int {
	c := m.runtimeCeilings(pluginID)
	if m != nil && m.runtimeConfig().RuntimeOverrides[pluginID].ListItems > 0 {
		return c.listItems
	}
	return min(ls.RequestedItems(), c.listItems)
}

// ListItemsGrant is the plugin's list sync size for the reviews and the
// drawer, or nil when the plugin has no lists.
func (m *Manager) ListItemsGrant(pluginID string, man Manifest) *ListItemsGrant {
	req := man.ListItemsRequested()
	if req == 0 {
		return nil
	}
	return &ListItemsGrant{Requested: req, Granted: m.GrantedListItems(pluginID, ListSourceMeta{MaxItems: req})}
}
