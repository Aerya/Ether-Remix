package catalog

import (
	"testing"

	"github.com/braedonsaunders/ether/internal/pse/listonly"
)

func TestPSEListCredits(t *testing.T) {
	if pseListCredits(t.Context()) != "" {
		t.Fatal("hors liste légère, toute la distribution est lue")
	}
	if pseListCredits(listonly.With(t.Context())) == "" {
		t.Fatal("liste légère : réalisateurs seuls")
	}
}
