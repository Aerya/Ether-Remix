package notify

// Fichier propre à Private Streaming Enhanced : seul point de contact entre
// le paquet notify d'Ether et la traduction des notifications. Voir
// docs/pse/FORK.fr.md (repère PSE-HOOK dans notify.go, fonction deliver).

import "github.com/braedonsaunders/ether/internal/pse/notifyfr"

// pseLocalize traduit le texte d'une notification juste avant l'envoi,
// sans modifier l'événement mis en file ni celui publié aux abonnés.
func pseLocalize(e Event) Event {
	e.Title, e.Message, e.HTML = notifyfr.Localize(e.Kind, e.Title, e.Message, e.HTML)
	return e
}
