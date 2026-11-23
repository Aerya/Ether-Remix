package plex

// Ajout Remix : écrans de liste servis immédiatement (voir docs/pse/FORK.fr.md).

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/frontend/kit/instantscreens"
)

// psePolicyStamp résume ce qui décide des droits d'un écran : spectateur
// restreint ou délégué, son profil (bibliothèques, contrôle parental) et les
// réglages du propriétaire. Un écran dont cette empreinte a changé n'est
// jamais servi en l'état.
func (s *Server) psePolicyStamp(pr *Request) string {
	if pr == nil || pr.User == nil {
		return ""
	}
	userJSON, _ := json.Marshal(pr.User)
	var config uint64
	if fn := s.getHost().PageFreshness; fn != nil {
		config = fn().Config
	}
	return strconv.FormatBool(s.viewerRestricted(pr.User)) + "|" + strconv.FormatBool(pr.User.Delegated) + "|" +
		strconv.FormatUint(config, 36) + "|" + string(userJSON)
}

// pseStaleHome indique qu'un écran en cache peut être servi tout de suite
// alors que son empreinte dure a changé (visionnage, disposition) ou qu'il a
// dépassé homeMaxStale : il est alors reconstruit en arrière-plan. Jamais pour
// un spectateur restreint, ni après un changement de droits ou de réglages,
// ni pour un écran provisoire ou de plus de instantscreens.MaxAge.
func (s *Server) pseStaleHome(e *homeEntry, hard string, pr *Request, now time.Time) bool {
	if e == nil || !instantscreens.Enabled() || e.provisional || e.psePolicy == "" || pr == nil || pr.User == nil {
		return false
	}
	age := now.Sub(e.builtAt)
	if age < 0 || age >= instantscreens.MaxAge || s.viewerRestricted(pr.User) {
		return false
	}
	if e.hard == hard && age < homeMaxStale {
		return false // déjà servi par le cas normal d'Ether
	}
	if pseViewerMasks(e.hard) != pseViewerMasks(hard) {
		return false // titre masqué ou « pas intéressé » : reconstruire tout de suite
	}
	return e.psePolicy == s.psePolicyStamp(pr)
}

// pseViewerMasks extrait de l'empreinte dure les deux derniers compteurs de
// rev.Viewer : exclusions « pas intéressé » et règles de catalogue du
// spectateur. Leur changement retire des titres de l'écran.
func pseViewerMasks(hard string) string {
	viewer, _, _ := strings.Cut(hard, "|")
	parts := strings.Split(viewer, ".")
	if len(parts) < 2 {
		return viewer
	}
	return strings.Join(parts[len(parts)-2:], ".")
}
