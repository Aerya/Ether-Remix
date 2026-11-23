package catalog

import "github.com/braedonsaunders/ether/internal/store"

func init() {
	// Partial artwork lookup indexes for restricted-viewer CDN checks (issue #598),
	// and a non-adult episode release index covering show_key so upcoming/recent
	// episode counts and shelves seek episodes first and probe shows by PK.
	store.Register("catalog", 62, `
CREATE INDEX IF NOT EXISTS catalog_titles_artwork_poster ON catalog_titles(poster, key) WHERE adult=0 AND poster<>'';
CREATE INDEX IF NOT EXISTS catalog_titles_artwork_backdrop ON catalog_titles(backdrop, key) WHERE adult=0 AND backdrop<>'';
CREATE INDEX IF NOT EXISTS catalog_titles_artwork_still ON catalog_titles(still, key) WHERE adult=0 AND still<>'';
CREATE INDEX IF NOT EXISTS catalog_titles_artwork_logo ON catalog_titles(logo, key) WHERE adult=0 AND logo<>'';
CREATE INDEX IF NOT EXISTS catalog_titles_episode_release ON catalog_titles(released_ts DESC, key ASC, show_key) WHERE type='episode' AND release_known=1 AND adult=0;`)
}
