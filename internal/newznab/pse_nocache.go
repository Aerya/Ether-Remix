package newznab

// pseLocalSource : la source Alkodiques (API StreamFusion) cherche dans un
// miroir local, sans appel réseau. Son cache de recherche n'apporte rien et
// écrirait en clair les liens AllDebrid (jetons privés) dans newznab_cache.
const pseLocalSource = "remix-alko"

func (c *Client) pseNoSearchCache() bool { return c.Config().ID == pseLocalSource }
