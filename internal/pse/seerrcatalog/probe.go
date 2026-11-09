package seerrcatalog

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"time"
)

// Probe checks the saved credentials without importing or searching any media.
// Return only fixed codes: upstream bodies and errors can contain credentials.
func (s *Service) Probe(ctx context.Context, c Settings) string {
	c.Enabled = true // A connection can be tested before trusting this source.
	if !c.valid() {
		return "configuration"
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.URL+"/api/integrations/remix/v1/availability?limit=1", nil)
	if err != nil {
		return "configuration"
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	resp, err := s.HTTP.Do(req)
	if err != nil {
		return "unreachable"
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case 401, 403:
		return "unauthorized"
	case 404:
		return "not_found"
	case 200:
	default:
		return "unavailable"
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, (4<<20)+1))
	if err != nil || len(raw) > 4<<20 {
		return "invalid_feed"
	}
	var p page
	if json.Unmarshal(raw, &p) != nil || p.Schema != Schema || p.Revision == nil || p.Items == nil {
		return "invalid_feed"
	}
	return "ok"
}
