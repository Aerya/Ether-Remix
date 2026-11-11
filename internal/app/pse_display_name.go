package app

import "context"

// renameViewer stores a viewer's display name and relabels their past plays.
func (a *App) renameViewer(ctx context.Context, id int64, name string) error {
	if err := a.users.SetDisplayName(ctx, id, name); err != nil {
		return err
	}
	if a.stats == nil {
		return nil
	}
	u, err := a.users.User(ctx, id)
	if err != nil {
		return err
	}
	return a.stats.RenameViewer(ctx, id, u.ActivityName())
}
