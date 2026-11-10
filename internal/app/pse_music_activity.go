package app

import "context"

// Le point d’entrée commun à Activité et Vue d’ensemble réunit les
// observations musicales et les sessions du moteur vidéo.
func (a *App) activitySessions(ctx context.Context) any {
	out := []any{}
	for _, row := range ownerSessionStatuses((ownerPlayback{Playback: a.playback, app: a}).Sessions()) {
		out = append(out, row)
	}
	if srv := pseRuntimeOf(a).music.Load(); srv != nil {
		rows := srv.NowPlaying()
		if len(rows) == 0 {
			return out
		}
		names := map[int64]string{}
		if viewers, err := a.pseViewers(ctx); err == nil {
			for _, v := range viewers {
				names[v.ID] = v.Name
			}
		}
		for _, row := range rows {
			if name := names[row.ViewerID]; name != "" {
				row.Username = name
			}
			out = append(out, row)
		}
	}
	return out
}
