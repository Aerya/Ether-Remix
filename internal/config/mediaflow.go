package config

import (
	"net/url"
	"strings"
)

// MediaFlow is a node-local relay. Enabling it forbids direct AllDebrid requests.
type MediaFlow struct {
	Enabled  bool   `json:"enabled"`
	URL      string `json:"url"`
	Password string `json:"password"`
}

func (m MediaFlow) Problems() []OutboundProblem {
	var out []OutboundProblem
	if !m.Enabled && m.URL == "" {
		return out
	}
	u, err := url.Parse(strings.TrimSpace(m.URL))
	if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		out = append(out, OutboundProblem{"outbound.allDebridMediaFlow.url", "must be an http(s) MediaFlow base URL without credentials, query or fragment"})
	}
	if m.Enabled && (m.Password == "" || !plainSecret(m.Password, true)) {
		out = append(out, OutboundProblem{"outbound.allDebridMediaFlow.password", "a MediaFlow API password is required"})
	}
	return out
}
