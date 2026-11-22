// Package listonly marque une requête de liste (bibliothèque, rangée,
// collection, catalogue) qui ne doit lire que ce qui est déjà en base : pas de
// fiche TMDB complétée, pas d'écriture du catalogue, pas de résolution ni
// d'appel debrid. Les détails ne partent qu'à l'ouverture d'une fiche ou à la
// lecture. Ajout PSE : voir docs/pse/FORK.fr.md.
package listonly

import (
	"context"
	"strings"

	"github.com/braedonsaunders/ether/internal/pse/pseenv"
)

// Key est le réglage qui active les listes légères (actif par défaut).
const Key = "PSE_LIGHT_LISTS"

type ctxKey struct{}

// Enabled indique si les listes légères sont actives.
func Enabled() bool {
	switch strings.ToLower(strings.TrimSpace(pseenv.Get(Key))) {
	case "off", "0", "false", "no", "non":
		return false
	}
	return true
}

// With marque ctx comme celui d'une liste, si le réglage est actif.
func With(ctx context.Context) context.Context {
	if ctx == nil || !Enabled() {
		return ctx
	}
	return context.WithValue(ctx, ctxKey{}, true)
}

// On indique si ctx appartient à une liste légère.
func On(ctx context.Context) bool {
	return ctx != nil && ctx.Value(ctxKey{}) != nil
}

// Without retire la marque : un travail partagé (construction d'une
// collection, index) lancé depuis une liste doit lire les fiches complètes,
// car son résultat est gardé et servi à tous.
func Without(ctx context.Context) context.Context {
	if !On(ctx) {
		return ctx
	}
	return context.WithValue(ctx, ctxKey{}, nil)
}
