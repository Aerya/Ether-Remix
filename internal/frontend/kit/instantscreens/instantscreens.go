// Package instantscreens règle les écrans de liste servis immédiatement :
// un écran déjà construit est renvoyé tout de suite, puis reconstruit en
// arrière-plan, comme sur un serveur Plex. Ajout PSE : voir docs/pse/FORK.fr.md.
package instantscreens

import (
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/pse/pseenv"
)

// Key est le réglage (actif par défaut ; off pour retrouver le comportement d'Ether).
const Key = "PSE_INSTANT_SCREENS"

// MaxAge : au-delà, un écran n'est plus servi tel quel et se reconstruit sur la requête.
const MaxAge = 12 * time.Hour

// Enabled indique si les écrans immédiats sont actifs.
func Enabled() bool {
	switch strings.ToLower(strings.TrimSpace(pseenv.Get(Key))) {
	case "off", "0", "false", "no", "non":
		return false
	}
	return true
}
