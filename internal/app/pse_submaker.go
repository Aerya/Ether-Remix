package app

import (
	"context"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/egress"
	"github.com/braedonsaunders/ether/internal/pse/submaker"
	"strconv"
	"time"
)

func (a *App) pseSubmaker() *submaker.Service {
	rt := pseRuntimeOf(a)
	rt.submakerOnce.Do(func() {
		if a.db == nil {
			return
		}
		s := submaker.New(a.db, egress.Client(egress.CapStremio, 8*time.Minute))
		s.Match = func(ctx context.Context, r submaker.Rule, key core.RatingKey) bool {
			if a.collections == nil {
				return false
			}
			ids := []string{r.Target}
			if r.Scope == "library" {
				ids = nil
				if th := a.pseThemes(); th != nil {
					for _, l := range th.Libraries() {
						if strconv.FormatInt(l.ID, 10) == r.Target {
							ids = l.Collections
							break
						}
					}
				}
			}
			for _, id := range ids {
				for _, k := range a.collections.PSEQualityKeys(ctx, id) {
					if k == key {
						return true
					}
				}
			}
			return false
		}
		rt.submaker = s
		a.closers = append(a.closers, s.Close)
	})
	return rt.submaker
}
func (a *App) prepareSubmaker(ctx context.Context, req core.SearchRequest, rels []core.Release) {
	s := a.pseSubmaker()
	if s == nil {
		return
	}
	n := 0
	for _, r := range rels {
		if n >= 2 {
			break
		}
		p, pending := s.Ensure(ctx, req, r)
		if p != nil || pending {
			n++
		}
	}
}
