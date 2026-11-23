package collections

import (
	"context"
	"testing"

	"github.com/braedonsaunders/ether/internal/pse/listonly"
)

// Une construction lancée depuis une liste (accueil, collection ouverte) lit
// les fiches complètes : son résultat est enregistré et servi à tous.
func TestPSEBuildLeavesLightList(t *testing.T) {
	if listonly.On(pseFullBuild(listonly.With(context.Background()))) {
		t.Fatal("construction en liste légère")
	}
}
