package screens

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

// Stream topics (docs/plugin-screens.md, Records, revs and live updates). A
// data read hands the client a sealed topic naming exactly what it read: the
// viewer, the app, the page or surface, its bindings and its time zone. The
// stream reopens that read; a client can neither forge nor widen it.

const (
	topicPrefix = "lt1."
	topicTTL    = 24 * time.Hour
)

// Topic is an opened stream topic.
type Topic struct {
	Viewer   int64             `json:"v"`
	Frontend string            `json:"f"`
	Kind     string            `json:"k"`
	ID       string            `json:"i"`
	Page     string            `json:"p,omitempty"`
	Params   map[string]string `json:"b,omitempty"`
	Sources  []string          `json:"s,omitempty"`
	TZ       string            `json:"z,omitempty"`
	Renderer int               `json:"r"`
	Locale   string            `json:"l,omitempty"`
	Expires  int64             `json:"e"`
}

// ErrInvalidTopic reports a topic that is malformed, expired, forged or
// belongs to another viewer.
var ErrInvalidTopic = errors.New("screens: invalid stream topic")

func (s *Service) sealTopic(viewer *core.User, req DataRequest, t target, b bindings) (string, bool) {
	if viewer == nil || viewer.ID <= 0 {
		return "", false
	}
	params := map[string]string{}
	for k, v := range req.Params {
		params[k] = v
	}
	tp := Topic{Viewer: viewer.ID, Frontend: req.Frontend, Kind: t.kind, ID: t.wireID, Page: t.page, Params: params, Sources: req.Sources,
		TZ: req.TZ, Renderer: req.Renderer, Locale: req.Locale, Expires: s.now().Add(topicTTL).Unix()}
	raw, err := json.Marshal(tp)
	if err != nil {
		return "", false
	}
	mac := hmac.New(sha256.New, s.topicKey)
	mac.Write(raw)
	return topicPrefix + base64.RawURLEncoding.EncodeToString(raw) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)[:16]), true
}

// OpenTopic verifies a topic for viewer.
func (s *Service) OpenTopic(viewer *core.User, token string) (Topic, error) {
	body, ok := strings.CutPrefix(token, topicPrefix)
	if !ok || viewer == nil {
		return Topic{}, ErrInvalidTopic
	}
	payload, sig, ok := strings.Cut(body, ".")
	if !ok {
		return Topic{}, ErrInvalidTopic
	}
	raw, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return Topic{}, ErrInvalidTopic
	}
	got, err := base64.RawURLEncoding.DecodeString(sig)
	if err != nil {
		return Topic{}, ErrInvalidTopic
	}
	mac := hmac.New(sha256.New, s.topicKey)
	mac.Write(raw)
	if !hmac.Equal(got, mac.Sum(nil)[:16]) {
		return Topic{}, ErrInvalidTopic
	}
	var tp Topic
	if err := json.Unmarshal(raw, &tp); err != nil {
		return Topic{}, ErrInvalidTopic
	}
	if tp.Viewer != viewer.ID || s.now().Unix() > tp.Expires {
		return Topic{}, ErrInvalidTopic
	}
	return tp, nil
}

// Request rebuilds the data read a topic names.
func (tp Topic) Request() DataRequest {
	return DataRequest{Kind: tp.Kind, ID: tp.ID, Page: tp.Page, Params: tp.Params, TZ: tp.TZ,
		ClientInfo: ClientInfo{Frontend: tp.Frontend, Renderer: tp.Renderer, Locale: tp.Locale}}
}

// Key identifies the read a topic names, for sharing work between streams.
func (tp Topic) Key() string {
	keys := make([]string, 0, len(tp.Params))
	for k, v := range tp.Params {
		keys = append(keys, k+"="+v)
	}
	sort.Strings(keys)
	return strings.Join([]string{tp.Frontend, tp.Kind, tp.ID, tp.Page, tp.TZ, strings.Join(keys, "&")}, "\x00")
}
