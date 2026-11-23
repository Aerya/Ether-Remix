package episodeorder

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/braedonsaunders/ether/internal/core"
)

type ReleasePreference struct {
	Mode    string `json:"mode"` // automatic, tmdb, group
	GroupID string `json:"groupId"`
}

func (s *Service) ReleasePreference(ctx context.Context, show core.RatingKey) (ReleasePreference, error) {
	p := ReleasePreference{Mode: "automatic"}
	err := s.db.QueryRowContext(ctx, `SELECT mode,group_id FROM episodeorder_release_preferences WHERE show_key=?`, int64(show)).Scan(&p.Mode, &p.GroupID)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	}
	return p, err
}

func (s *Service) SaveReleasePreference(ctx context.Context, show core.RatingKey, p ReleasePreference) error {
	if show.Kind() != core.KindShow || show.TMDB() <= 0 {
		return fmt.Errorf("invalid show")
	}
	switch p.Mode {
	case "automatic", "tmdb":
		if p.GroupID != "" {
			return fmt.Errorf("unexpected group")
		}
	case "group":
		groups, err := s.source.EpisodeGroups(ctx, show.TMDB())
		if err != nil {
			return err
		}
		valid := false
		for _, g := range groups {
			if g.ID == p.GroupID {
				valid = true
				break
			}
		}
		if !valid {
			return fmt.Errorf("episode group does not belong to show")
		}
		if _, err = s.group(ctx, p.GroupID); err != nil {
			return err
		}
	default:
		return fmt.Errorf("invalid release numbering")
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO episodeorder_release_preferences(show_key,mode,group_id) VALUES(?,?,?) ON CONFLICT(show_key) DO UPDATE SET mode=excluded.mode,group_id=excluded.group_id`, int64(show), p.Mode, p.GroupID)
	return err
}
