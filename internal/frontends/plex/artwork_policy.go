package plex

import (
	"github.com/braedonsaunders/ether/internal/frontend"
	"net/http"
	"net/url"
	"path"
)

const artworkPolicyLookupSQL = `SELECT key FROM (SELECT key FROM catalog_titles WHERE adult=0 AND poster<>'' AND poster=? LIMIT 20) p
UNION SELECT key FROM (SELECT key FROM catalog_titles WHERE adult=0 AND backdrop<>'' AND backdrop=? LIMIT 20) b
UNION SELECT key FROM (SELECT key FROM catalog_titles WHERE adult=0 AND still<>'' AND still=? LIMIT 20) s
UNION SELECT key FROM (SELECT key FROM catalog_titles WHERE adult=0 AND logo<>'' AND logo=? LIMIT 20) l
LIMIT 20`

// Absolute CDN URLs contain no rating key. A restricted viewer can only proxy
// artwork already associated with a visible mirrored title.
func (s *Server) remoteArtworkAllowed(r *http.Request, u *url.URL) bool {
	viewer := FromContext(r.Context()).User
	restricted := viewer == nil || viewer.MaxContentRating != "" || len(viewer.AllowedLibraries) > 0
	if policy, ok := s.policy().(frontend.ArtworkAccessPolicy); ok {
		restricted = restricted || policy.Restricted(viewer)
	}
	if !restricted {
		return true
	}
	if s.runtimeDB() == nil {
		return false
	}
	base := path.Base(u.Path)
	if base == "" || base == "." || base == "/" {
		return false
	}
	imagePath := "/" + base
	rows, err := s.runtimeDB().QueryContext(r.Context(), artworkPolicyLookupSQL, imagePath, imagePath, imagePath, imagePath)
	if err != nil {
		return false
	}
	var keys []frontend.RatingKey
	for rows.Next() {
		var key frontend.RatingKey
		if rows.Scan(&key) != nil {
			rows.Close()
			return false
		}
		keys = append(keys, key)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return false
	}
	if pre, ok := s.catalog().(frontend.StoredTitlePreloader); ok && len(keys) > 1 {
		pre.PreloadStored(r.Context(), keys)
	}
	for _, key := range keys {
		title, err := s.catalog().Get(r.Context(), key)
		if err == nil && s.visibleTitle(r.Context(), viewer, title) {
			return true
		}
	}
	return false
}
