package vault

import (
	"net/url"
	"strings"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/releaseparse"
)

// TorrentRecord keeps durable torrent identity and file metadata only.
func TorrentRecord(r core.Release, files []TorrentFile, info []byte) PutRequest {
	hash := strings.ToLower(r.InfoHash)
	q := url.Values{"xt": {"urn:btih:" + hash}}
	if u, err := url.Parse(r.MagnetURL); err == nil && u.Scheme == "magnet" {
		for _, tr := range u.Query()["tr"] {
			q.Add("tr", tr)
		}
	}
	magnet := "magnet:?" + q.Encode()
	r.MagnetURL = magnet
	r.InfoHash = hash
	r.NZBURL, r.StreamURL, r.StreamOrigin, r.StreamHeaders = "", "", "", nil
	if strings.Contains(r.GUID, "://") || strings.HasPrefix(r.GUID, "magnet:") {
		r.GUID = "urn:btih:" + hash
	}
	t := &Torrent{InfoHash: hash, Magnet: magnet, Info: info}
	for _, f := range files {
		if f.Path == "" || strings.Contains(f.Path, "://") {
			continue
		}
		if f.Episode == 0 {
			p := releaseparse.Parse(f.Path)
			if len(p.Episodes) == 1 {
				f.Episode = p.Episodes[0]
				if len(p.Seasons) == 1 {
					f.Season = p.Seasons[0]
				}
			}
		}
		t.Files = append(t.Files, f)
	}
	return PutRequest{Release: r, Torrent: t, Source: r.IndexerID, Private: r.Private}
}
