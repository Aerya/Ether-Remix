package experience

import (
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
)

func TestNamesTitleFoldsCaseAndAccents(t *testing.T) {
	items := []core.Title{{Title: "El Camino: A Breaking Bad Movie", SearchScore: 0.5}, {Title: "Amélie", SearchScore: 0.5}}
	for _, q := range []string{"breaking bad", "Breaking Bad", "BREAKING BAD", "amelie", "Amélie", "AMÉLIE"} {
		if !namesTitle(q, items) {
			t.Errorf("namesTitle(%q) = false: case or accents changed the classification", q)
		}
	}
}
