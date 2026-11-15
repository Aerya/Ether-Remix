package config

import "strings"

// SourceProxy overrides the global route only when enabled. It deliberately has
// no bypass list: every request on this route, including redirects, uses it.
// Credentials use the ordinary config encryption and redaction walkers.
type SourceProxy struct {
	Enabled  bool   `json:"enabled"`
	URL      string `json:"url"`
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
}

func (p SourceProxy) Outbound() Outbound {
	return Outbound{Enabled: p.Enabled, URL: p.URL, Username: p.Username, Password: p.Password, Debrid: true, Stremio: true}
}
func (p SourceProxy) Problems(prefix string) []OutboundProblem {
	problems := p.Outbound().Problems()
	for i := range problems {
		problems[i].Field = prefix + strings.TrimPrefix(problems[i].Field, "outbound")
	}
	return problems
}
