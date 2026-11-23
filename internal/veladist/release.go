// Package veladist installs owner-requested, signed Vela releases. It does no
// background work and is independent of viewer requests.
package veladist

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/braedonsaunders/ether/internal/updater"
)

// Dedicated Vela release pins. Override with -ldflags -X for lab builds or
// key rotation. An explicitly empty current pin fails closed unless next is set.
const productionPublicKeyB64 = "KJWM3PXm9MbSSkTsG+/GB8/lCPC6i1+1qMKxmLo3cUA="

var PublicKeyB64 = productionPublicKeyB64
var NextPublicKeyB64 string

const StableURL = "https://github.com/braedonsaunders/vela/releases/download/vela-stable/channel.json"
const maxMetadata = 8 << 20
const maxArchive = 512 << 20
const maxExtracted = 2 << 30

type Asset struct {
	URL    string `json:"url"`
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
}
type Release struct {
	SchemaVersion  int       `json:"schemaVersion"`
	Kind           string    `json:"kind"`
	Channel        string    `json:"channel"`
	Version        string    `json:"version"`
	PublishedAt    time.Time `json:"publishedAt"`
	ExpiresAt      time.Time `json:"expiresAt"`
	SourceRevision string    `json:"sourceRevision"`
	SourceDirty    *bool     `json:"sourceDirty"`
	Protocol       struct {
		MinInclusive *int `json:"minInclusive"`
		MaxInclusive *int `json:"maxInclusive"`
	} `json:"protocol"`
	MinimumEtherVersion string `json:"minimumEtherVersion"`
	Archive             Asset  `json:"archive"`
	Manifest            Asset  `json:"manifest"`
	Engines             map[string]struct {
		Manifest Asset `json:"manifest"`
	} `json:"engines"`
	UniversalRuntimeKey string `json:"universalRuntimeKey"`
}

func Verify(raw, sig []byte, keys []ed25519.PublicKey, ether, installed string, now time.Time) (*Release, error) {
	signature, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(sig)))
	if err != nil || len(signature) != ed25519.SignatureSize {
		return nil, errors.New("invalid release signature")
	}
	valid := false
	for _, key := range keys {
		if len(key) == ed25519.PublicKeySize && ed25519.Verify(key, raw, signature) {
			valid = true
			break
		}
	}
	if !valid {
		return nil, errors.New("release signature is not trusted")
	}
	if !utf8.Valid(raw) {
		return nil, errors.New("release descriptor is not UTF-8")
	}
	var d Release
	if err = json.Unmarshal(raw, &d); err != nil {
		return nil, errors.New("invalid release descriptor")
	}
	if d.SchemaVersion != 1 || d.Kind != "vela-release" || d.Channel != "stable" {
		return nil, errors.New("unsupported release channel or schema")
	}
	if d.PublishedAt.IsZero() || d.ExpiresAt.IsZero() || !d.ExpiresAt.After(now) || !d.ExpiresAt.After(d.PublishedAt) || d.PublishedAt.After(now.Add(5*time.Minute)) {
		return nil, errors.New("release has expired or has invalid dates")
	}
	if d.SourceDirty == nil || *d.SourceDirty || d.SourceRevision == "" {
		return nil, errors.New("release is not a clean source build")
	}
	if d.Protocol.MinInclusive == nil || d.Protocol.MaxInclusive == nil || *d.Protocol.MinInclusive > 0 || *d.Protocol.MaxInclusive < 0 {
		return nil, errors.New("release does not support this Ether protocol")
	}
	if n, e := updater.CompareVersions(d.MinimumEtherVersion, ether); e != nil || n > 0 {
		return nil, errors.New("release requires a newer Ether version")
	}
	if _, e := updater.ParseVersion(d.Version); e != nil || !safeName(d.Version) {
		return nil, errors.New("invalid release version")
	}
	if installed != "" {
		if n, e := updater.CompareVersions(d.Version, installed); e != nil || n <= 0 {
			return nil, errors.New("release is not newer than the installed version")
		}
	}
	for _, a := range []Asset{d.Archive, d.Manifest} {
		if err = validAsset(a); err != nil {
			return nil, err
		}
	}
	for id, e := range d.Engines {
		if !safeName(id) {
			return nil, errors.New("invalid engine name")
		}
		if err = validAsset(e.Manifest); err != nil {
			return nil, err
		}
	}
	return &d, nil
}
func safeName(s string) bool {
	return s != "" && s != "." && s != ".." && !strings.ContainsAny(s, "/\\\x00:")
}
func validAsset(a Asset) error {
	digest, e := hex.DecodeString(a.SHA256)
	if a.Bytes <= 0 || a.Bytes > maxArchive || e != nil || len(digest) != 32 {
		return errors.New("invalid release asset size or digest")
	}
	return nil
}
func pinnedKeys() []ed25519.PublicKey {
	var keys []ed25519.PublicKey
	for _, s := range []string{PublicKeyB64, NextPublicKeyB64} {
		if b, e := base64.StdEncoding.DecodeString(s); e == nil && len(b) == 32 {
			keys = append(keys, ed25519.PublicKey(b))
		}
	}
	return keys
}

func openSource(ctx context.Context, client *http.Client, address string, local bool) (io.ReadCloser, error) {
	u, e := url.Parse(address)
	if e != nil || u.User != nil {
		return nil, errors.New("invalid release URL")
	}
	if local && u.Scheme == "file" && (u.Host == "" || u.Host == "localhost") && u.RawQuery == "" && u.Fragment == "" {
		return os.Open(localFilePath(u.Path, runtime.GOOS))
	}
	if u.Scheme != "https" || u.Host == "" {
		return nil, errors.New("release downloads require HTTPS")
	}
	req, e := http.NewRequestWithContext(ctx, "GET", address, nil)
	if e != nil {
		return nil, errors.New("invalid release request")
	}
	res, e := client.Do(req)
	if e != nil {
		return nil, errors.New("release download failed")
	}
	if res.StatusCode != 200 {
		res.Body.Close()
		return nil, fmt.Errorf("release download returned HTTP %d", res.StatusCode)
	}
	return res.Body, nil
}
func readSource(ctx context.Context, client *http.Client, address string, local bool, limit int64) ([]byte, error) {
	r, e := openSource(ctx, client, address, local)
	if e != nil {
		return nil, e
	}
	defer r.Close()
	b, e := io.ReadAll(io.LimitReader(r, limit+1))
	if e != nil {
		return nil, errors.New("release download interrupted")
	}
	if int64(len(b)) > limit {
		return nil, errors.New("release download exceeds its size limit")
	}
	return b, nil
}
func metadata(ctx context.Context, client *http.Client, a Asset, local bool) ([]byte, error) {
	if a.Bytes > maxMetadata {
		return nil, errors.New("manifest is too large")
	}
	b, e := readSource(ctx, client, a.URL, local, a.Bytes)
	if e != nil {
		return nil, e
	}
	if int64(len(b)) != a.Bytes || !digestMatches(b, a.SHA256) {
		return nil, errors.New("manifest size or digest mismatch")
	}
	return b, nil
}
func digestMatches(b []byte, want string) bool {
	d := sha256.Sum256(b)
	return strings.EqualFold(hex.EncodeToString(d[:]), want)
}

func localFilePath(p, goos string) string {
	if goos == "windows" {
		if len(p) >= 3 && p[0] == '/' && p[2] == ':' && ((p[1] >= 'A' && p[1] <= 'Z') || (p[1] >= 'a' && p[1] <= 'z')) {
			p = p[1:]
		}
		return strings.ReplaceAll(p, "/", `\`)
	}
	return p
}
