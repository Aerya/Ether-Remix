package plex

import (
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontends/plex/model"
	"net/http"
)

func (s *Server) discoveryCardHome(r *http.Request, include bool) homeBuild {
	ctx := r.Context()
	pr := FromContext(ctx)
	mc := hubContainer()
	if include {
		page, err := s.exp().Collections(ctx, pr.User, frontend.Device{}, frontend.CollectionQuery{NavigationOnly: true, Limit: 500})
		if err != nil {
			s.log.Warn("navigation cards", "err", err)
		} else {
			for _, index := range frontend.NavigationGroupOrder() {
				group := frontend.NavigationGroups[index]
				hub := model.Hub{HubIdentifier: "remix.navigation." + group.ID, Title: group.Name, Type: "collection", Context: "hub.collection", Style: "shelf", Promoted: model.Ptr(true)}
				for _, c := range page.Items {
					if c.NavigationGroup != group.ID {
						continue
					}
					m := s.collectionMetadata(&c, ctx)
					m.Extra = append(m.Extra, model.A("thumbAspectRatio", float64(16)/9))
					m.Image = []model.Image{{Alt: c.Title, Type: "coverArt", URL: m.Thumb}}
					hub.Metadata = append(hub.Metadata, m)
				}
				hub.Size = len(hub.Metadata)
				hub.TotalSize = model.Ptr(hub.Size)
				if hub.Size > 0 {
					mc.Hub = append(mc.Hub, hub)
				}
			}
		}
	}
	mc.Size = len(mc.Hub)
	return homeBuild{mc: mc, provisional: include && len(mc.Hub) == 0, served: map[string]frontend.Hub{}}
}
