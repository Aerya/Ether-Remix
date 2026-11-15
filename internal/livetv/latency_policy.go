package livetv

import "github.com/braedonsaunders/ether/internal/config"

func (s *Service) latencyPolicy() config.LiveLatencyPolicy {
	c := config.LiveTV{}
	if s != nil && s.o.LatencyProfile != nil {
		c.LatencyProfile = s.o.LatencyProfile()
	}
	return c.LatencyPolicy()
}
