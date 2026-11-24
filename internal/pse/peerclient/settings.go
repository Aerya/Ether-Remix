// Package peerclient connects optional aMule and Soulseek services to Remix.
package peerclient

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/braedonsaunders/ether/internal/pse/pseenv"
)

type Settings struct {
	Kind                          string
	Enabled                       bool
	URL, Secret, Root, RemoteRoot string
}

func Load(kind string) Settings {
	p := "PSE_" + strings.ToUpper(kind)
	s := Settings{Kind: kind, URL: strings.TrimRight(strings.TrimSpace(pseenv.Get(p+"_URL")), "/"), Secret: strings.TrimSpace(pseenv.Get(p + "_KEY")), Root: strings.TrimSpace(pseenv.Get(p + "_ROOT"))}
	s.RemoteRoot = strings.TrimSpace(pseenv.Get(p + "_REMOTE_ROOT"))
	if s.RemoteRoot == "" {
		s.RemoteRoot = s.Root
	}
	v := strings.ToLower(pseenv.Get(p))
	s.Enabled = (v == "on" || v == "true" || v == "1") && s.Secret != "" && ValidateURL(s.URL) == nil && filepath.IsAbs(s.Root) && filepath.IsAbs(s.RemoteRoot)
	return s
}
func ValidateURL(v string) error {
	u, e := url.Parse(v)
	if e != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("adresse HTTP(S) sans identifiants ni paramètres attendue")
	}
	return nil
}
func (s Settings) Identity() string {
	h := sha256.Sum256([]byte(s.Kind + "\x00" + s.URL + "\x00" + s.Secret + "\x00" + s.Root + "\x00" + s.RemoteRoot))
	return hex.EncodeToString(h[:16])
}
