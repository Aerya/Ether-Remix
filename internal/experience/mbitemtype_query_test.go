package experience

import (
	"context"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
)

// TestMatchesUserQueryDeclaredMBType proves a title answers IncludeItemTypes
// and ExcludeItemTypes by the MediaBrowser type its kind declares, not only
// by its projected core type.
func TestMatchesUserQueryDeclaredMBType(t *testing.T) {
	s := &Service{}
	v := &core.User{ID: 1}
	track := core.Title{Key: core.PluginKey(42), Type: core.TypeEpisode, MBItemType: "Audio", Title: "Fog Horn"}

	ok, err := s.matchesUserQuery(context.Background(), v, track, frontend.ItemQuery{UnservedTypes: []string{"audio"}})
	if err != nil || !ok {
		t.Fatalf("audio include = %v, %v", ok, err)
	}
	ok, err = s.matchesUserQuery(context.Background(), v, track, frontend.ItemQuery{UnservedTypes: []string{"musicalbum"}})
	if err != nil || ok {
		t.Fatalf("musicalbum include = %v, %v", ok, err)
	}
	ok, err = s.matchesUserQuery(context.Background(), v, track, frontend.ItemQuery{ExcludedTypes: []core.MediaType{"audio"}})
	if err != nil || ok {
		t.Fatalf("audio exclude = %v, %v", ok, err)
	}
	// The projected core type keeps matching as before.
	ok, err = s.matchesUserQuery(context.Background(), v, track, frontend.ItemQuery{Types: []core.MediaType{core.TypeEpisode}})
	if err != nil || !ok {
		t.Fatalf("episode include = %v, %v", ok, err)
	}
}
