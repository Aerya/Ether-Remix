// Package debrid streams torrent releases through owner-configured debrid
// services (Real-Debrid, TorBox, AllDebrid, Premiumize, Debrid-Link,
// Offcloud), and Stremio addons' own HTTP stream links (direct.go). A
// torrent the service already holds ("cached") is added to the owner's
// account and its main video file is read over HTTPS range requests;
// nothing is downloaded in full and Ether never seeds.
//
// The rest of Ether treats a release payload as opaque bytes (normally an
// NZB). A debrid release's payload is a small descriptor (see Payload), and
// the Indexer, Engine and health-check wrappers in this package dispatch on
// it, delegating everything else to the Usenet implementations.
package debrid

import (
	"bytes"
	"crypto/sha1"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/archive"
	"github.com/braedonsaunders/ether/internal/core"
)

// payloadMagic prefixes every encoded Payload.
const payloadMagic = "ETHER-DEBRID/1\n"

// ZipMember describes one named file located inside a provider's server-side
// ZIP archive (such as TorBox's auto-zipped 100+-file cached torrents). When
// set on Payload, FileID and OuterSize identify the outer .zip on the provider
// while Path and Size identify the named member inside that zip.
type ZipMember struct {
	OuterSize  int64  `json:"outerSize"`        // exact byte size of the outer .zip on CDN
	DataOffset int64  `json:"dataOffset"`       // start of member payload bytes (past Local File Header)
	CompSize   int64  `json:"compSize"`         // compressed byte length inside the outer .zip
	Method     uint16 `json:"method,omitempty"` // ZIP compression method (0 = store, 8 = deflate)
	CRC32      uint32 `json:"crc32,omitempty"`  // expected uncompressed IEEE CRC-32
}

// Payload identifies one file of a torrent held by a debrid provider. It is
// persisted by playback for resume, so it stores stable identifiers (info
// hash, magnet, provider torrent and file ids, path and size) from which a
// fresh link can be generated after the last one expires. It never holds
// an API key.
type Payload struct {
	NativeTorrentOnly bool      `json:"nativeTorrentOnly,omitempty"` // quick mode: fail through resolver, never through another debrid account
	VerifiedFile      bool      `json:"verifiedFile,omitempty"`      // standalone file: never add a magnet
	AddonID           string    `json:"addonId,omitempty"`
	Key               string    `json:"key,omitempty"`       // payload cache key (release + target)
	ReleaseID         string    `json:"releaseId,omitempty"` // core.Release.ID (segment cache key)
	Provider          string    `json:"provider"`            // config.DebridProvider.ID
	Kind              string    `json:"kind"`                // config.DebridKinds
	InfoHash          string    `json:"infoHash"`            // lower-case hex v1 info hash
	Magnet            string    `json:"magnet,omitempty"`    // magnet URI used to (re)add the torrent
	Private           bool      `json:"private,omitempty"`   // private-tracker torrent: bare hash & existing cache only
	TorrentID         string    `json:"torrentId,omitempty"` // provider torrent / magnet id
	FileID            string    `json:"fileId,omitempty"`    // provider file id
	Path              string    `json:"path"`                // file path inside the torrent
	Size              int64     `json:"size"`                // file size in bytes
	Link              string    `json:"link,omitempty"`      // provider-side file link (RD/AllDebrid), unrestricted on demand
	URL               string    `json:"url,omitempty"`       // last direct download URL
	Expires           time.Time `json:"expires,omitempty"`   // when URL should be regenerated
	// OnReplayEdge records that URL stands on a CDN edge the engine moved
	// the link to (resumeOnAnotherEdge), not on the host the provider
	// signed. Media reads on such a link refuse redirects: a followed hop
	// would receive the signed URL as Go's Referer header, token included,
	// from an authority the provider never approved.
	OnReplayEdge bool                `json:"onReplayEdge,omitempty"`
	Target       *core.ReleaseTarget `json:"target,omitempty"`
	Release      string              `json:"release,omitempty"` // release name, for logs
	// Direct HTTP streams (Kind KindHTTP, direct.go): Link is the addon's
	// stream link, Headers the request headers it requires and Origin the
	// owner-configured addon origin. Link and Headers can carry the
	// addon's credentials.
	StreamProvider string            `json:"streamProvider,omitempty"`
	Headers        map[string]string `json:"headers,omitempty"`
	Origin         string            `json:"origin,omitempty"`
	// Zip is set when the provider holds the torrent only as a server-side ZIP
	// and Path/Size name a member inside that archive.
	Zip *ZipMember `json:"zip,omitempty"`
}

// IsPayload reports whether b is an encoded debrid Payload.
func IsPayload(b []byte) bool { return bytes.HasPrefix(b, []byte(payloadMagic)) }

// Encode serializes p with the payload prefix.
func (p Payload) Encode() []byte {
	body, _ := json.Marshal(p)
	return append([]byte(payloadMagic), body...)
}

// DecodePayload parses an encoded Payload.
func DecodePayload(b []byte) (Payload, error) {
	if !IsPayload(b) {
		return Payload{}, errors.New("debrid: not a debrid payload")
	}
	var p Payload
	if err := json.Unmarshal(b[len(payloadMagic):], &p); err != nil {
		return Payload{}, fmt.Errorf("debrid: corrupt payload: %w", err)
	}
	if p.Size <= 0 || (p.InfoHash == "" && !(p.IsDirect() && p.Link != "") && !(p.VerifiedFile && p.Kind == "alldebrid" && core.ValidAllDebridFileURL(p.Link))) {
		return Payload{}, errors.New("debrid: incomplete payload")
	}
	if p.Zip != nil {
		z := p.Zip
		if z.OuterSize <= 0 || z.DataOffset < 0 || z.CompSize <= 0 || z.CompSize > z.OuterSize-z.DataOffset {
			return Payload{}, errors.New("debrid: invalid zip member bounds")
		}
		switch z.Method {
		case archive.ZIPMethodStore:
			if z.CompSize != p.Size {
				return Payload{}, errors.New("debrid: stored zip member size mismatch")
			}
		case archive.ZIPMethodDeflate:
		default:
			return Payload{}, fmt.Errorf("debrid: unsupported zip member method %d", z.Method)
		}
	}
	return p, nil
}

// Name is the file's base name.
func (p Payload) Name() string { return path.Base(strings.ReplaceAll(p.Path, "\\", "/")) }

// isCompressedZip reports whether this payload streams compressed bytes of a
// member inside a provider server-side ZIP (e.g. deflate) that a downstream
// reader must inflate.
func (p Payload) isCompressedZip() bool {
	return p.Zip != nil && p.Zip.Method != archive.ZIPMethodStore
}

// streamSize is the byte length exposed by remoteFile (the member size for
// loose files and stored zip members; the compressed byte extent for deflate
// zip members).
func (p Payload) streamSize() int64 {
	if p.isCompressedZip() {
		return p.Zip.CompSize
	}
	return p.Size
}

// wireRange maps a logical byte range [start, end] of remoteFile onto the
// provider CDN's HTTP byte range and total resource size.
func (p Payload) wireRange(start, end int64) (wireStart, wireEnd, wireSize int64) {
	if p.Zip != nil && p.Zip.OuterSize > 0 {
		return p.Zip.DataOffset + start, p.Zip.DataOffset + end, p.Zip.OuterSize
	}
	return start, end, p.Size
}

// sameStreamEncoding reports whether other delivers the same byte stream
// representation as p (so an uncompressed payload never mixes with a
// compressed provider-zip payload in refresh or failover).
func (p Payload) sameStreamEncoding(other Payload) bool {
	if p.isCompressedZip() != other.isCompressedZip() {
		return false
	}
	if p.isCompressedZip() {
		return p.Zip.Method == other.Zip.Method && p.Zip.CompSize == other.Zip.CompSize && p.Size == other.Size
	}
	return p.Size == other.Size
}

// handle is the provider-facing identity of the payload's file.
func (p Payload) handle() Handle {
	size := p.Size
	if p.Zip != nil && p.Zip.OuterSize > 0 {
		size = p.Zip.OuterSize
	}
	return Handle{TorrentID: p.TorrentID, FileID: p.FileID, Path: p.Path, Size: size, Link: p.Link, Magnet: p.Magnet, InfoHash: p.InfoHash, Private: p.Private}
}

// fileTag identifies the file independently of the provider (base name and
// size); cached chunks carry it so a chunk of another file is never served.
func (p Payload) fileTag() [8]byte {
	key := strings.ToLower(p.Name()) + "|" + fmt.Sprint(p.Size)
	if p.isCompressedZip() {
		key += fmt.Sprintf("|zip:%d:%d", p.Zip.Method, p.Zip.CompSize)
	}
	sum := sha1.Sum([]byte(key))
	var t [8]byte
	copy(t[:], sum[:8])
	return t
}
