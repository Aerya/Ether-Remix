package screens

import (
	"context"
	"strings"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/plugins/screendoc"
)

// Titles and channels in screens (docs/plugin-screens.md, Sources: items,
// channels; envelopes $item and $channel). Ether finds the rating keys;
// the frontend projects them into its own title and channel cards for the
// viewer and drops any the viewer may not see, so a document can show a
// plugin's library items or Live TV channels with nothing new to learn.

// Cards is the frontend side of a read: its card projection. A frontend's
// ImageSigner may also implement it.
type Cards interface {
	// TitleCards returns cards for the keys the viewer may see, by key.
	TitleCards(ctx context.Context, keys []core.RatingKey) map[core.RatingKey]any
	// ChannelCards returns cards for the channels the viewer may see, by id.
	ChannelCards(ctx context.Context, ids []string, group string) ([]string, map[string]any)
}

// ItemsQuery is the shelf spec an items source carries.
type ItemsQuery struct {
	Kind, Level, Sort, Direction, Facet string
	Values                              []string
	Limit                               int
}

// ItemFinder finds a plugin's own library items; the app implements it.
type ItemFinder interface {
	// KindShelf returns up to q.Limit item keys of pluginID's kind q.Kind
	// for viewer (content-rating capped; library visibility is enforced
	// again by the frontend's cards).
	KindShelf(ctx context.Context, viewer *core.User, pluginID string, q ItemsQuery) ([]core.RatingKey, error)
	// KindKeys maps the plugin's own external item ids to rating keys.
	KindKeys(ctx context.Context, pluginID, kind string, ids []string) (map[string]core.RatingKey, error)
}

// SetItemFinder installs the item lookup.
func (s *Service) SetItemFinder(f ItemFinder) {
	if s != nil {
		s.mu.Lock()
		s.items = f
		s.mu.Unlock()
	}
}

func (s *Service) itemFinder() ItemFinder {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.items
}

func (rc *readCtx) cards() Cards {
	c, _ := rc.images.(Cards)
	return c
}

// titleRecords projects keys into item:<key> records, in order, dropping
// what the viewer may not see.
func (rc *readCtx) titleRecords(ctx context.Context, keys []core.RatingKey) []map[string]string {
	refs := []map[string]string{}
	cards := rc.cards()
	if cards == nil || len(keys) == 0 {
		return refs
	}
	got := cards.TitleCards(ctx, keys)
	for _, k := range keys {
		card, ok := got[k]
		if !ok {
			continue
		}
		key := "item:" + k.String()
		rc.addRecord(key, card)
		refs = append(refs, recordRef(key))
	}
	return refs
}

func (rc *readCtx) itemsSource(ctx context.Context, src screendoc.Source) (any, error) {
	f := rc.s.itemFinder()
	if f == nil {
		return nil, errUnsupported
	}
	q := ItemsQuery{
		Kind:      rc.b.text(src.Args, "kind"),
		Level:     rc.b.text(src.Args, "level"),
		Sort:      rc.b.text(src.Args, "sort"),
		Direction: rc.b.text(src.Args, "direction"),
		Facet:     rc.b.text(src.Args, "facet"),
		Limit:     20,
	}
	if n, ok := src.Args["limit"].(int); ok && n > 0 {
		q.Limit = n
	}
	if list, ok := src.Args["values"].([]any); ok {
		for _, v := range list {
			if s, ok := v.(string); ok && strings.TrimSpace(rc.b.fill(s)) != "" {
				q.Values = append(q.Values, strings.TrimSpace(rc.b.fill(s)))
			}
		}
	}
	keys, err := f.KindShelf(ctx, rc.viewer, rc.t.plugin.pluginID, q)
	if err != nil {
		return nil, err
	}
	return map[string]any{"items": rc.titleRecords(ctx, keys)}, nil
}

func (rc *readCtx) channelsSource(ctx context.Context, src screendoc.Source) (any, error) {
	cards := rc.cards()
	if cards == nil {
		return nil, errUnsupported
	}
	var ids []string
	if list, ok := src.Args["ids"].([]any); ok {
		for _, v := range list {
			if s, ok := v.(string); ok {
				if id := strings.TrimPrefix(strings.TrimSpace(rc.b.fill(s)), "channel:"); id != "" {
					ids = append(ids, id)
				}
			}
		}
	}
	order, got := cards.ChannelCards(ctx, ids, rc.b.text(src.Args, "group"))
	refs := []map[string]string{}
	for _, id := range order {
		card, ok := got[id]
		if !ok {
			continue
		}
		key := "channel:" + id
		rc.addRecord(key, card)
		refs = append(refs, recordRef(key))
		if len(refs) >= 50 {
			break
		}
	}
	return map[string]any{"channels": refs}, nil
}
