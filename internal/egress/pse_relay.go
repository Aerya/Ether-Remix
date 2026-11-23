package egress

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// MediaFlowRequest réécrit une requête GET pour qu'elle passe par un relais MediaFlow
// choisi par l'utilisateur (adresse et mot de passe API). Même protocole que la
// transport MediaFlow d'AllDebrid : jeton chiffré dans l'adresse, mot de passe en
// en-tête, identité de contenu. Seuls les en-têtes sûrs sont transmis au relais.
func MediaFlowRequest(req *http.Request, relayURL, password string) (*http.Request, error) {
	root, err := url.Parse(strings.TrimRight(strings.TrimSpace(relayURL), "/"))
	if err != nil || root.Hostname() == "" || (root.Scheme != "http" && root.Scheme != "https") {
		return nil, errors.New("adresse du relais invalide")
	}
	if password == "" {
		return nil, errors.New("mot de passe du relais manquant")
	}
	endpoint := "/proxy/stream"
	if strings.HasPrefix(strings.ToLower(req.URL.Hostname()), "api.") {
		endpoint = "/proxy/forward"
	}
	data := map[string]any{"d": req.URL.String(), "exp": time.Now().Add(5 * time.Minute).Unix(), "h_accept-encoding": "identity"}
	for name, values := range req.Header {
		switch strings.ToLower(name) {
		case "user-agent", "accept", "x-api-key":
			data["h_"+strings.ToLower(name)] = strings.Join(values, ", ")
		}
	}
	token, err := mediaFlowToken(data, password)
	if err != nil {
		return nil, err
	}
	root.Path += endpoint
	root.RawQuery = url.Values{"token": {token}}.Encode()
	routed := req.Clone(req.Context())
	routed.URL = root
	routed.Host = ""
	routed.Header = make(http.Header)
	routed.Header.Set("api_password", password)
	routed.Header.Set("Accept-Encoding", "identity")
	return routed, nil
}

// MediaFlowRelayClient est le client HTTP du relais : il ne passe par aucun proxy,
// ne suit aucune redirection (une redirection du relais pourrait contourner le
// relais ou exposer le mot de passe) et a un délai borné.
func MediaFlowRelayClient(timeout time.Duration) *http.Client {
	t := baseTransport()
	t.Proxy = nil
	return &http.Client{
		Transport:     t,
		Timeout:       timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}
