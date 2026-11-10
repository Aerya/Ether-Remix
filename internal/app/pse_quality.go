package app

import (
	"context"
	"strconv"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/pse/quality"
	"github.com/braedonsaunders/ether/internal/pse/releasedb"
	"github.com/braedonsaunders/ether/internal/pse/themes"
)

func (a *App) pseQuality() *quality.Service {
	rt := pseRuntimeOf(a)
	rt.qualityOnce.Do(func() {
		if a.db == nil {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		s, err := quality.New(ctx, a.db)
		if err != nil {
			a.log.Error("profils qualité Remix", "err", err)
			return
		}
		rt.quality = s
		a.closers = append(a.closers, s.Close)
	})
	return rt.quality
}
func (a *App) refreshPSEQuality(ctx context.Context, th *themes.Service) {
	s := a.pseQuality()
	if s == nil || a.collections == nil {
		return
	}
	m := map[string][]core.RatingKey{}
	c := s.Config()
	libs := map[string][]string{}
	for _, l := range th.Libraries() {
		libs[strconv.FormatInt(l.ID, 10)] = l.Collections
	}
	for _, p := range c.Profiles {
		if p.Scope == "collection" {
			m["collection:"+p.Target] = a.collections.PSEQualityKeys(ctx, p.Target)
		}
		if p.Scope == "library" {
			for _, id := range libs[p.Target] {
				m["library:"+p.Target] = append(m["library:"+p.Target], a.collections.PSEQualityKeys(ctx, id)...)
			}
		}
	}
	s.SetMembers(m)
	st := releasedb.NewStore(a.db)
	if _, err := st.SyncPlayable(ctx, releasedb.Playable()); err != nil && ctx.Err() == nil {
		a.log.Warn("recalcul des profils qualité", "err", err)
	}
}
