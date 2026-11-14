package core

import "context"

// Ajout Remix — voir docs/pse/FORK.fr.md. Une collection d'exception de
// langue (VOSTFR, concerts, SeerrCatalog) garde ses titres sans VF ; ailleurs
// le filtre VF s'applique entièrement.

type pseLangFreeKey struct{}

// WithPSELangFree marque ctx : les exceptions de langue comptent.
func WithPSELangFree(ctx context.Context) context.Context {
	return context.WithValue(ctx, pseLangFreeKey{}, true)
}

// PSELangFree indique si ctx a été marqué par WithPSELangFree.
func PSELangFree(ctx context.Context) bool {
	v, _ := ctx.Value(pseLangFreeKey{}).(bool)
	return v
}

type pseCollectionKey struct{}

// WithPSECollection attache à ctx l'identifiant de la collection servie.
func WithPSECollection(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, pseCollectionKey{}, id)
}

// PSECollection renvoie l'identifiant attaché par WithPSECollection.
func PSECollection(ctx context.Context) string {
	v, _ := ctx.Value(pseCollectionKey{}).(string)
	return v
}
