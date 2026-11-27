package mediabrowser

import (
	"net/http"

	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/kit/ids"
)

func (i *instance) nextUpSeries(r *http.Request, viewer *frontend.Viewer) (frontend.RatingKey, bool) {
	raw := firstQuery(r, "seriesId")
	explicit := raw != ""
	if !explicit {
		raw = firstQuery(r, "parentId")
	}
	key, err := ids.ParseRatingKeyGUID(raw)
	if err != nil {
		return 0, explicit
	}
	if key.IsPluginKey() {
		title, ok := i.storedListTitle(r, viewer, key)
		if ok {
			switch title.Type {
			case frontend.TypeShow:
				return key, true
			case frontend.TypeSeason, frontend.TypeEpisode:
				return title.ShowKey, true
			}
		}
		return 0, true
	}
	switch key.Kind() {
	case frontend.KindShow:
		return key, true
	case frontend.KindSeason, frontend.KindEpisode:
		return i.seriesForItem(r, viewer, key), true
	default:
		return 0, explicit
	}
}

func (i *instance) seriesForItem(r *http.Request, viewer *frontend.Viewer, key frontend.RatingKey) frontend.RatingKey {
	title, ok := i.storedListTitle(r, viewer, key)
	if !ok {
		return 0
	}
	return parentShowKey(title)
}

func (i *instance) seriesNextUp(r *http.Request, v *frontend.Viewer, series frontend.RatingKey) ([]frontend.Title, error) {
	title, err := frontend.LoadSeriesNextUp(r.Context(), i.host.Experience, v, device(r), series, queryBool(r, "EnableResumable", true))
	if err != nil || title == nil {
		return nil, err
	}
	return []frontend.Title{*title}, nil
}
