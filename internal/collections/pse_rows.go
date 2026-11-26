package collections

import (
	"context"
	"github.com/braedonsaunders/ether/internal/core"
)

// PublishRemixRows places imported selections through the Recommendations
// engine. Publication is independent of the collection's browse setting.
func (s *Service) PublishRemixRows(ctx context.Context, sp core.CollectionSpec) error {
	if p, ok := s.personalScorer().(interface {
		PublishRemixRows(context.Context, core.CollectionSpec) error
	}); ok {
		return p.PublishRemixRows(ctx, sp)
	}
	return nil
}
