package livetv

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/braedonsaunders/ether/internal/store"
)

// errEmptyLineup marks a provider that answered with no channels at all while
// the source already has a lineup. Providers return an empty list during
// outages and maintenance; importing it would delete every channel and its
// owner settings. The refresh fails instead, keeping the last good lineup,
// and the next scheduled refresh tries again.
var errEmptyLineup = errors.New("the provider returned no channels")

// refuseEmptyLineup returns errEmptyLineup when the source has stored channels.
// A brand-new source with no channels yet may import an empty lineup.
func (s *Service) refuseEmptyLineup(ctx context.Context, src *Source) error {
	if src.ID == "" {
		return nil
	}
	var one int
	err := s.o.DB.QueryRowContext(ctx, `SELECT 1 FROM livetv_channels WHERE `+store.JSONText(store.DialectOf(s.o.DB), "data", "sourceId")+`=? LIMIT 1`, src.ID).Scan(&one)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return nil
	case err != nil:
		return err
	}
	return fmt.Errorf("%w; kept the channels from the last successful refresh and will try again", errEmptyLineup)
}
