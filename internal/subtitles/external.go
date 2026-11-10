package subtitles

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/langtag"
	"os"
	"path/filepath"
)

// StoreExternal stores a release-bound artifact without changing manual selections.
// Its caller attaches it only to the matching playback release.
func (s *Service) StoreExternal(title *core.Title, language, release string, body []byte, translated bool) (core.StreamInfo, error) {
	if s == nil || s.dir == "" || title == nil || langtag.Normalize(language) == "" || len(body) > maxSubtitleBytes {
		return core.StreamInfo{}, fmt.Errorf("invalid external subtitle")
	}
	srt, vtt, e := NormalizeLang(body, language)
	if e != nil {
		return core.StreamInfo{}, e
	}
	hash := sha256.Sum256([]byte(fmt.Sprintf("%d\x00%s\x00%s\x00%s", title.Key, language, release, srt)))
	id := "submaker-" + hex.EncodeToString(hash[:])
	if e = os.MkdirAll(s.dir, 0755); e != nil {
		return core.StreamInfo{}, e
	}
	meta, _ := json.Marshal(map[string]any{"titleKey": title.Key.String(), "language": language, "release": release, "fetchedAt": s.now().UTC()})
	for _, f := range []struct {
		ext string
		b   []byte
	}{{"srt", []byte(srt)}, {"vtt", []byte(vtt)}, {"json", meta}} {
		if e = writeFileAtomic(filepath.Join(s.dir, id+"."+f.ext), f.b); e != nil {
			return core.StreamInfo{}, e
		}
	}
	label := "SubMaker"
	if translated {
		label += " · AI"
	}
	return core.StreamInfo{Type: "subtitle", Codec: "srt", Language: language, Title: label, ProviderTitle: label, External: true, ExternalURL: URLPrefix + id}, nil
}
