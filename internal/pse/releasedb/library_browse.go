package releasedb

import (
	"context"
	"encoding/json"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

func init() {
	store.Register("pse_release_library", 2, `ALTER TABLE pse_release_library ADD COLUMN browse TEXT NOT NULL DEFAULT '';
 CREATE INDEX pse_release_library_browse_match ON pse_release_library(title_key,json_extract(browse,'$.name'),json_extract(browse,'$.source')) WHERE browse<>'' AND browse<>'{}';`)
	const mediaKey = `CASE WHEN title_key/1000000000000000=4 THEN 2000000000000000+(title_key%1000000000000000)/10000000 ELSE title_key END`
	store.Register("pse_release_library", 3, `CREATE INDEX pse_release_library_media_dates ON pse_release_library (`+mediaKey+`,first_seen,last_seen);
 CREATE INDEX pse_release_library_pending ON pse_release_library(title_key) WHERE browse='';
 CREATE INDEX pse_release_library_browse_first ON pse_release_library(first_seen,title_key);
 CREATE INDEX pse_release_library_browse_last ON pse_release_library(last_seen,title_key);`)
	store.Register("pse_releases", 11, `CREATE INDEX pse_releases_media_dates ON pse_releases (`+mediaKey+`,first_seen,last_seen);
 CREATE INDEX pse_releases_browse_first ON pse_releases(first_seen,title_key);
 CREATE INDEX pse_releases_browse_last ON pse_releases(last_seen,title_key);`)

}

// BrowseRelease is an explicit allowlist: never persist URLs, headers or IDs here.
type BrowseRelease struct {
	Name       string   `json:"name"`
	Source     string   `json:"source"`
	Protocol   string   `json:"protocol"`
	Size       int64    `json:"size"`
	Height     int      `json:"height"`
	VideoCodec string   `json:"videoCodec"`
	AudioCodec string   `json:"audioCodec"`
	Languages  []string `json:"languages"`
	Cached     []string `json:"cached"`
	Published  int64    `json:"published"`
}

func browseRelease(r core.Release) string {
	p := int64(0)
	if !r.PubDate.IsZero() {
		p = r.PubDate.Unix()
	}
	b, _ := json.Marshal(BrowseRelease{r.Name, r.Indexer, r.Protocol, r.Size, r.Info.Height, r.Info.VideoCodec, r.Info.AudioCodec, r.Info.Languages, r.DebridCached, p})
	return string(b)
}

// PrepareBrowse upgrades old encrypted entries in bounded batches. Broken entries
// are marked as empty objects, so they cannot stall the browser indefinitely.
func (l Library) PrepareBrowse(ctx context.Context) (bool, error) {
	return l.PrepareBrowseMedia(ctx, 0)
}
func (l Library) PrepareBrowseMedia(ctx context.Context, key core.RatingKey) (bool, error) {
	predicate := "browse=''"
	args := []any{}
	if key != 0 {
		predicate += " AND (title_key=?"
		args = append(args, int64(key))
		if key.Kind() == core.KindShow {
			predicate += " OR (title_key>=? AND title_key<?)"
			args = append(args, int64(core.EpisodeKey(key.TMDB(), 0, 0)), int64(core.EpisodeKey(key.TMDB()+1, 0, 0)))
		}
		predicate += ")"
	}

	rows, err := l.Store.db.QueryContext(ctx, `SELECT title_key,source_id,release_id,payload FROM pse_release_library WHERE `+predicate+` LIMIT 2000`, args...)
	if err != nil {
		return false, err
	}
	type entry struct {
		key                 int64
		source, id, payload string
	}
	entries := []entry{}
	for rows.Next() {
		var e entry
		if err = rows.Scan(&e.key, &e.source, &e.id, &e.payload); err != nil {
			break
		}
		entries = append(entries, e)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return false, err
	}
	if len(entries) == 0 {
		return false, nil
	}
	tx, err := l.Store.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	for _, e := range entries {
		projection := "{}"
		if raw, x := l.Open(e.payload); x == nil {
			var r core.Release
			if json.Unmarshal([]byte(raw), &r) == nil {
				projection = browseRelease(r)
			}
		}
		if _, err = tx.ExecContext(ctx, `UPDATE pse_release_library SET browse=? WHERE title_key=? AND source_id=? AND release_id=? AND payload=? AND browse=''`, projection, e.key, e.source, e.id, e.payload); err != nil {
			return false, err
		}
	}
	if err = tx.Commit(); err != nil {
		return false, err
	}
	var pending bool
	err = l.Store.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pse_release_library WHERE `+predicate+`)`, args...).Scan(&pending)
	return pending, err
}
