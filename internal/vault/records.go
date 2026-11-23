package vault

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"strings"

	"github.com/braedonsaunders/ether/internal/events"
	"github.com/braedonsaunders/ether/internal/nzb"
	"github.com/braedonsaunders/ether/internal/releaseparse"
)

func recordKey(r *Record) Key {
	if r.Torrent != nil {
		return TorrentKey(r.Torrent.InfoHash)
	}
	return ContentKey(r.NZB)
}
func (v *library) passwords(r *Record, seal bool) error {
	if r.NZB == nil {
		return nil
	}
	for _, field := range []string{"password", "pass", "pwd"} {
		for i, s := range r.NZB.Meta[field] {
			if v.Sealer == nil {
				return errors.New("vault: password requires credential sealer")
			}
			label := "vault/" + string(r.Key) + "/" + field
			if seal {
				x, e := v.Sealer.Seal(label, []byte(s))
				if e != nil {
					return e
				}
				r.NZB.Meta[field][i] = x
			} else {
				x, e := v.Sealer.Unseal(label, s)
				if e != nil {
					return e
				}
				r.NZB.Meta[field][i] = string(x)
			}
		}
	}
	return nil
}
func (v *library) Get(ctx context.Context, k Key) (*Record, error) {
	l, e := v.loc(ctx, v.DB, k)
	if e != nil {
		return nil, e
	}
	// Check the indexed frame even on a cache hit: a restore may leave
	// the index intact while removing or replacing its packs.
	b, e := v.read(l)
	if e != nil {
		if errors.Is(e, ErrPayloadMissing) {
			v.emitPayloadMissing(ctx, k, l.pack)
		}
		return nil, e
	}
	v.cacheMu.Lock()
	for i, c := range v.cache {
		if c.key == k && c.loc == l {
			copy(v.cache[1:i+1], v.cache[:i])
			v.cache[0] = c
			v.cacheMu.Unlock()
			return cloneRecord(c.r), nil
		}
	}
	v.cacheMu.Unlock()
	r, e := Decode(b[12:])
	if e != nil || r.Key != k {
		v.emitPayloadMissing(ctx, k, l.pack)
		return nil, ErrPayloadMissing
	}
	if e = v.passwords(r, false); e != nil {
		return nil, e
	}
	if r.NZB != nil {
		r.Password = r.NZB.Password()
		r.XML, _ = r.NZB.Subset(func(*nzb.File) bool { return true })
	}
	if len(b) < 1<<20 && len(r.XML) < 8<<20 {
		v.cacheMu.Lock()
		v.cache = append([]cached{{k, l, r}}, v.cache...)
		if len(v.cache) > 4 {
			v.cache = v.cache[:4]
		}
		v.cacheMu.Unlock()
		return cloneRecord(r), nil
	}
	return r, nil
}
func cloneRecord(r *Record) *Record {
	c := *r
	c.XML = slices.Clone(r.XML)
	c.Par2 = slices.Clone(r.Par2)
	if r.NZB != nil {
		d := *r.NZB
		d.Meta = map[string][]string{}
		for k, v := range r.NZB.Meta {
			d.Meta[k] = slices.Clone(v)
		}
		d.Files = slices.Clone(d.Files)
		for i, f := range d.Files {
			x := *f
			x.Groups = slices.Clone(f.Groups)
			x.Segments = slices.Clone(f.Segments)
			for j, s := range x.Segments {
				x.Segments[j].Alternates = slices.Clone(s.Alternates)
			}
			d.Files[i] = &x
		}
		c.NZB = &d
	}
	if r.Torrent != nil {
		t := *r.Torrent
		t.Files = slices.Clone(t.Files)
		t.Info = slices.Clone(t.Info)
		c.Torrent = &t
	}
	if r.Layout != nil {
		l := *r.Layout
		l.Members = slices.Clone(l.Members)
		for i, m := range l.Members {
			l.Members[i].Extents = slices.Clone(m.Extents)
		}
		l.Volumes = slices.Clone(l.Volumes)
		l.FirstArticles = map[int][]byte{}
		for i, b := range r.Layout.FirstArticles {
			l.FirstArticles[i] = slices.Clone(b)
		}
		c.Layout = &l
	}
	return &c
}
func first(a []int) int {
	if len(a) > 0 {
		return a[0]
	}
	return 0
}
func (v *library) Put(ctx context.Context, p PutRequest) (Key, error) {
	r := Record{Name: p.Release.Name, Kind: KindUsenet}
	var size int64
	segments := 0
	if p.Torrent != nil {
		t := *p.Torrent
		t.InfoHash = strings.ToLower(t.InfoHash)
		if t.InfoHash == "" {
			return "", invalid
		}
		r.Kind = KindTorrent
		r.Torrent = &t
		for _, f := range t.Files {
			size += f.Size
		}
	} else {
		d, e := nzb.Parse(p.NZB)
		if e != nil {
			return "", e
		}
		r.NZB = d
		for _, f := range d.Files {
			size += f.Bytes()
			segments += len(f.Segments)
		}
		if r.Name == "" {
			for _, name := range d.Meta["name"] {
				if name != "" {
					r.Name = name
					break
				}
			}
			if r.Name == "" {
				r.Name = d.Files[0].Name
			}
		}
	}
	k := recordKey(&r)
	r.Key = k
	if p.Release.Size > 0 {
		size = p.Release.Size
	}
	if e := v.passwords(&r, true); e != nil {
		return "", e
	}
	info := releaseparse.Parse(r.Name)
	ranking := p.Release.Info
	if ranking.Title == "" {
		ranking = info
	}
	metadata, err := json.Marshal(ranking)
	if err != nil {
		return "", err
	}
	source := p.Source
	if source == "" {
		source = p.Release.IndexerID
	}
	var written bool
	var writtenLoc location
	var quotaUsed int64
	var quotaCount int
	titleKey := releaseparse.NormalizeTitle(info.Title)
	e := v.write(ctx, func(tx *sql.Tx) error {
		l, e := v.loc(ctx, tx, k)
		write := true
		if e == nil {
			if b, readErr := v.read(l); readErr == nil {
				write = false
				if r.Torrent != nil && (len(r.Torrent.Files) > 0 || len(r.Torrent.Info) > 0) {
					old, e := Decode(b[12:])
					if e != nil {
						return e
					}
					if old.Torrent == nil {
						return invalid
					}
					if len(r.Torrent.Files) == 0 {
						r.Torrent.Files = old.Torrent.Files
					}
					if len(r.Torrent.Info) == 0 {
						r.Torrent.Info = old.Torrent.Info
					}
					if r.Torrent.Magnet == "" {
						r.Torrent.Magnet = old.Torrent.Magnet
					}
					r.Layout = old.Layout
					write = true
				}
				// The same post from another indexer can carry the archive
				// password the stored copy lacks; without it the release is
				// unplayable, so merge it into the stored record.
				if r.NZB != nil {
					if merged, ok := mergePasswords(b[12:], r.NZB.Meta); ok {
						r = *merged
						r.Key = k
						write = true
					}
				}
			}
		} else if !errors.Is(e, ErrNotFound) && !errors.Is(e, ErrPayloadMissing) {
			return e
		}
		if write {
			b, e := Encode(&r)
			if e != nil {
				return e
			}
			l, e = v.appendFrame(frame(b))
			if e != nil {
				return e
			}
			written = true
			writtenLoc = l
			now := v.Now().Unix()
			_, e = tx.ExecContext(ctx, `INSERT INTO vault_releases(key,name,kind,title_key,tmdb,year,season,episode,size,segments,pack,off,len,crc,holders,source,added,last_used,private,profile_fp,info) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(key) DO UPDATE SET pack=excluded.pack,off=excluded.off,len=excluded.len,crc=excluded.crc,holders=excluded.holders,dead=0`, k, r.Name, r.Kind, titleKey, p.Release.TitleIDs.TMDB, info.Year, first(info.Seasons), first(info.Episodes), size, segments, l.pack, l.off, l.length, l.crc, v.node(), source, now, now, flag(p.Private || p.Release.Private), p.ProfileFP, string(metadata))
			if e != nil {
				return e
			}
			if r.Torrent != nil && len(r.Torrent.Files) > 0 && r.Layout == nil {
				var m []Member
				for _, f := range r.Torrent.Files {
					m = append(m, Member{Size: f.Size, Season: f.Season, Episode: f.Episode})
				}
				if e = members(ctx, tx, k, m); e != nil {
					return e
				}
			}
		}
		_, e = tx.ExecContext(ctx, `UPDATE vault_releases SET private=CASE WHEN ?=1 THEN 1 ELSE private END,profile_fp=CASE WHEN ?<>'' THEN ? ELSE profile_fp END,tmdb=CASE WHEN tmdb=0 THEN ? ELSE tmdb END WHERE key=?`, flag(p.Private || p.Release.Private), p.ProfileFP, p.ProfileFP, p.Release.TitleIDs.TMDB, k)
		if e != nil {
			return e
		}
		if p.Release.ID != "" {
			ids := []string{p.Release.ID}
			cacheID := p.Release.ID
			if t := p.Release.Target; t != nil && t.PackID != "" && t.PackID != cacheID {
				cacheID = t.PackID
				ids = append(ids, cacheID)
			}
			for _, id := range ids {
				_, e = tx.ExecContext(ctx, `INSERT INTO vault_aliases(indexer_id,release_id,key) VALUES(?,?,?) ON CONFLICT(indexer_id,release_id) DO UPDATE SET key=excluded.key`, p.Release.IndexerID, id, k)
				if e != nil {
					return e
				}
			}
			if r.Kind == KindUsenet && p.Release.IndexerID != LegacyAliasIndexer && p.Release.IndexerID != IndexerID {
				_, e = tx.ExecContext(ctx, `UPDATE vault_releases SET cache_id=? WHERE key=? AND cache_id=''`, cacheID, k)
				if e != nil {
					return e
				}
			}
		}
		if write {
			if v.Events != nil && v.capBytes.Load() > 0 {
				_ = tx.QueryRowContext(ctx, `SELECT COALESCE(sum(len),0), count(*) FROM vault_releases WHERE dead=0`).Scan(&quotaUsed, &quotaCount)
			}
			if err := v.evict(ctx, tx); err != nil {
				return err
			}
		}
		return nil
	})
	if e == nil && written && v.Events != nil {
		v.publishEvent(events.EventVaultRecordStored, events.VaultRecordStoredPayload{
			VaultKey:        string(k),
			Kind:            string(r.Kind),
			TitleKey:        titleKey,
			ReleaseName:     r.Name,
			SizeBytes:       size,
			CompressedBytes: int64(writtenLoc.length),
			PackID:          writtenLoc.pack,
		}, events.WithCorrelation(events.Correlation{TitleID: titleKey}))
		if capBytes := v.capBytes.Load(); capBytes > 0 {
			v.checkQuotaWarning(quotaUsed, capBytes, quotaCount)
		}
	}
	return k, e
}

func (v *library) emitPayloadMissing(ctx context.Context, k Key, packID string) {
	if v == nil || v.Events == nil {
		return
	}
	var titleKey string
	if v.DB != nil {
		_ = v.DB.QueryRowContext(ctx, `SELECT title_key FROM vault_releases WHERE key=?`, k).Scan(&titleKey)
	}
	v.publishEvent(events.EventVaultPayloadMissing, events.VaultPayloadMissingPayload{
		VaultKey: string(k),
		PackID:   packID,
		NodeID:   v.node(),
		TitleKey: titleKey,
	}, events.WithCorrelation(events.Correlation{TitleID: titleKey}))
}
func (v *library) evict(ctx context.Context, tx *sql.Tx) error {
	var total int64
	if e := tx.QueryRowContext(ctx, `SELECT COALESCE(sum(len),0) FROM vault_releases WHERE dead=0`).Scan(&total); e != nil {
		return e
	}
	limit := v.capBytes.Load()
	if limit <= 0 || total <= limit {
		return nil
	}
	_, e := tx.ExecContext(ctx, `UPDATE vault_releases SET dead=1 WHERE key IN (SELECT key FROM (SELECT key,sum(len) OVER(ORDER BY CASE WHEN uses<=1 THEN 0 ELSE 1 END,last_used,key) - len AS prior FROM vault_releases WHERE dead=0) v WHERE prior<?)`, total-limit)
	return e
}
func members(ctx context.Context, tx *sql.Tx, k Key, m []Member) error {
	if _, e := tx.ExecContext(ctx, "DELETE FROM vault_members WHERE key=?", k); e != nil {
		return e
	}
	for i, f := range m {
		_, e := tx.ExecContext(ctx, "INSERT INTO vault_members(key,member,season,episode,size) VALUES(?,?,?,?,?)", k, i, f.Season, f.Episode, f.Size)
		if e != nil {
			return e
		}
	}
	return nil
}
func (v *library) SetLayout(ctx context.Context, k Key, l Layout) error {
	first := map[int][]byte{}
	remaining := 2 << 20
	var ids []int
	for i := range l.FirstArticles {
		ids = append(ids, i)
	}
	slices.Sort(ids)
	for _, i := range ids {
		b := l.FirstArticles[i]
		if len(b) <= remaining {
			first[i] = b
			remaining -= len(b)
		}
	}
	l.FirstArticles = first
	return v.write(ctx, func(tx *sql.Tx) error {
		loc, e := v.loc(ctx, tx, k)
		if e != nil {
			return e
		}
		b, e := v.read(loc)
		if e != nil {
			return e
		}
		r, e := Decode(b[12:])
		if e != nil {
			return e
		}
		r.Layout = &l
		b, e = Encode(r)
		if e != nil {
			return e
		}
		loc, e = v.appendFrame(frame(b))
		if e != nil {
			return e
		}
		_, e = tx.ExecContext(ctx, "UPDATE vault_releases SET pack=?,off=?,len=?,crc=?,holders=? WHERE key=?", loc.pack, loc.off, loc.length, loc.crc, v.node(), k)
		if e != nil {
			return e
		}
		if e = members(ctx, tx, k, l.Members); e != nil {
			return e
		}
		return v.evict(ctx, tx)
	})
}

var passwordFields = []string{"password", "pass", "pwd"}

// mergePasswords returns the stored record (payload) with the sealed
// password fields of incoming that it lacks, or false when nothing is
// missing. Both sides are sealed with the same labels.
func mergePasswords(payload []byte, incoming map[string][]string) (*Record, bool) {
	var add map[string][]string
	for _, f := range passwordFields {
		if len(incoming[f]) > 0 && incoming[f][0] != "" {
			if add == nil {
				add = map[string][]string{}
			}
			add[f] = incoming[f]
		}
	}
	if add == nil {
		return nil, false
	}
	old, e := Decode(payload)
	if e != nil || old.NZB == nil {
		return nil, false
	}
	for _, f := range passwordFields {
		if len(old.NZB.Meta[f]) > 0 && old.NZB.Meta[f][0] != "" {
			return nil, false // the stored copy already has a password
		}
	}
	if old.NZB.Meta == nil {
		old.NZB.Meta = map[string][]string{}
	}
	for f, v := range add {
		old.NZB.Meta[f] = v
	}
	return old, true
}
