package debrid

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/archive"
	"github.com/braedonsaunders/ether/internal/httpclient"
)

const (
	torBoxZipCDTTL        = 15 * time.Minute
	torBoxZipCDMaxEntries = 8
	torBoxZipInspectLimit = 20 * time.Second
)

type torBoxZipCD struct {
	entries []archive.ZIPEntry
	at      time.Time
}

func (p *torBox) zipCDKey(torrentID, fileID string, size int64) string {
	return torrentID + "\x00" + fileID + "\x00" + strconv.FormatInt(size, 10)
}

func (p *torBox) getZipCD(torrentID, fileID string, size int64) ([]archive.ZIPEntry, bool) {
	now := p.opts.Now()
	p.zipMu.Lock()
	defer p.zipMu.Unlock()
	if p.zipCDs == nil {
		return nil, false
	}
	k := p.zipCDKey(torrentID, fileID, size)
	v, ok := p.zipCDs[k]
	if !ok || now.Sub(v.at) > torBoxZipCDTTL {
		delete(p.zipCDs, k)
		return nil, false
	}
	return v.entries, true
}

func (p *torBox) putZipCD(torrentID, fileID string, size int64, entries []archive.ZIPEntry) {
	now := p.opts.Now()
	p.zipMu.Lock()
	defer p.zipMu.Unlock()
	if p.zipCDs == nil {
		p.zipCDs = make(map[string]torBoxZipCD)
	}
	for k, v := range p.zipCDs {
		if now.Sub(v.at) > torBoxZipCDTTL {
			delete(p.zipCDs, k)
		}
	}
	if len(p.zipCDs) >= torBoxZipCDMaxEntries {
		var oldestKey string
		var oldestAt time.Time
		for k, v := range p.zipCDs {
			if oldestKey == "" || v.at.Before(oldestAt) {
				oldestKey, oldestAt = k, v.at
			}
		}
		if oldestKey != "" {
			delete(p.zipCDs, oldestKey)
		}
	}
	p.zipCDs[p.zipCDKey(torrentID, fileID, size)] = torBoxZipCD{entries: entries, at: now}
}

func (p *torBox) forgetZipCD(torrentID string) {
	if torrentID == "" {
		return
	}
	prefix := torrentID + "\x00"
	p.zipMu.Lock()
	defer p.zipMu.Unlock()
	for k := range p.zipCDs {
		if strings.HasPrefix(k, prefix) {
			delete(p.zipCDs, k)
		}
	}
}

// acquireFromZip inspects a TorBox server-side ZIP archive over HTTP Range
// requests (Central Directory + chosen member's 30-byte Local File Header)
// and returns an Acquired pointing at the chosen member inside that zip.
// Unreadable zips, non-rangeable links, and unstreamable members fail with
// errProviderZipped so Service fails over cleanly to other providers.
func (p *torBox) acquireFromZip(
	ctx context.Context,
	req AcquireRequest,
	id, torrentName string,
	outer tbFile,
	outerFiles []File,
	added bool,
	cleanup func() bool,
) (*Acquired, error) {
	zipErr := func(detail string, cause error) error {
		cleanup()
		if cause != nil {
			return fmt.Errorf("torbox: torrent cached as zipped archive (%s): %s (%v): %w", torrentFileSummary(outerFiles), detail, cause, errProviderZipped)
		}
		return fmt.Errorf("torbox: torrent cached as zipped archive (%s): %s: %w", torrentFileSummary(outerFiles), detail, errProviderZipped)
	}

	inspectCtx, cancel := context.WithTimeout(ctx, torBoxZipInspectLimit)
	defer cancel()

	outerID := outer.ID.String()
	dl, err := p.requestDL(inspectCtx, id, outerID, outer.Size)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if errors.Is(err, ErrAuth) {
			return nil, err
		}
		return nil, zipErr("could not link zip", err)
	}

	zr, err := newHTTPZipReaderAt(inspectCtx, p.opts.HTTPClient, dl.URL, outer.Size)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, zipErr("zip not range-readable", err)
	}
	vols := []archive.Volume{{R: zr, Size: zr.size}}

	entries, fromCache := p.getZipCD(id, outerID, zr.size)
	if !fromCache {
		entries, err = archive.ReadZIPDirectory(vols)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, zipErr("unreadable zip directory", err)
		}
		p.putZipCD(id, outerID, zr.size, entries)
	}

	chooseEntry := func(all []archive.ZIPEntry) (archive.ZIPEntry, File, error) {
		nonDirs := make([]archive.ZIPEntry, 0, len(all))
		for _, e := range all {
			if !e.Dir {
				nonDirs = append(nonDirs, e)
			}
		}
		if len(nonDirs) == 0 {
			return archive.ZIPEntry{}, File{}, zipErr("zip has no files", nil)
		}
		zipFiles := make([]File, len(nonDirs))
		for i, e := range nonDirs {
			zipFiles[i] = File{ID: strconv.Itoa(i), Path: e.Name, Size: e.Size}
		}
		chosen, cerr := req.Choose(zipFiles)
		if cerr != nil && torrentName != "" {
			// If TorBox omitted the torrent's top-level folder inside the zip,
			// retry once with the torrent name prefixed so callers naming
			// "TorrentName/path/file" still match uniquely.
			prefix := strings.Trim(strings.ReplaceAll(torrentName, "\\", "/"), "/") + "/"
			hasPrefix := false
			for _, e := range nonDirs {
				if strings.HasPrefix(strings.TrimPrefix(e.Name, "/"), prefix) {
					hasPrefix = true
					break
				}
			}
			if !hasPrefix {
				prefixed := make([]File, len(nonDirs))
				for i, e := range nonDirs {
					prefixed[i] = File{ID: strconv.Itoa(i), Path: prefix + strings.TrimPrefix(e.Name, "/"), Size: e.Size}
				}
				if c2, err2 := req.Choose(prefixed); err2 == nil {
					chosen, cerr = c2, nil
				}
			}
		}
		if cerr != nil {
			cleanup()
			return archive.ZIPEntry{}, File{}, cerr
		}
		idx, perr := strconv.Atoi(chosen.ID)
		if perr != nil || idx < 0 || idx >= len(nonDirs) {
			return archive.ZIPEntry{}, File{}, zipErr("selected entry index invalid", perr)
		}
		return nonDirs[idx], chosen, nil
	}

	entry, chosen, err := chooseEntry(entries)
	if err != nil {
		return nil, err
	}

	validateEntry := func(e archive.ZIPEntry) error {
		if e.Encrypted {
			return zipErr(fmt.Sprintf("member %q is encrypted", e.Name), nil)
		}
		switch e.MethodCode {
		case archive.ZIPMethodStore:
		case archive.ZIPMethodDeflate:
			if !req.AllowCompressedZip {
				return zipErr(fmt.Sprintf("member %q is deflate-compressed", e.Name), nil)
			}
		default:
			return zipErr(fmt.Sprintf("member %q uses unsupported method %s", e.Name, e.Method), nil)
		}
		if e.Size <= 0 || e.CompSize <= 0 {
			return zipErr(fmt.Sprintf("member %q is empty", e.Name), nil)
		}
		return nil
	}

	if err := validateEntry(entry); err != nil {
		return nil, err
	}

	_, dataOff, err := archive.ResolveZIPEntry(vols, entry)
	if err != nil && fromCache {
		// Cached CD disagreed with live zip: re-read once before giving up.
		p.forgetZipCD(id)
		entries, err = archive.ReadZIPDirectory(vols)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, zipErr("unreadable zip directory", err)
		}
		p.putZipCD(id, outerID, zr.size, entries)
		entry, chosen, err = chooseEntry(entries)
		if err != nil {
			return nil, err
		}
		if err := validateEntry(entry); err != nil {
			return nil, err
		}
		_, dataOff, err = archive.ResolveZIPEntry(vols, entry)
	}
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, zipErr(fmt.Sprintf("unreadable local header for %q", entry.Name), err)
	}

	p.recent.put(id, outerID, dl.URL, p.opts.Now())
	p.rememberKnown(req.InfoHash, id)

	chosen.ID = outerID
	chosen.Path = entry.Name
	chosen.Size = entry.Size
	chosen.Zip = &ZipMember{
		OuterSize:  zr.size,
		DataOffset: dataOff,
		CompSize:   entry.CompSize,
		Method:     entry.MethodCode,
		CRC32:      entry.CRC32,
	}
	return &Acquired{TorrentID: id, File: chosen, Added: added}, nil
}

// httpZipReaderAt performs bounded HTTP Range reads for inspecting a remote
// ZIP archive's Central Directory and a single member's Local File Header.
// Its constructor prefetches the EOCD tail in one 206 request and verifies
// range support and exact CDN resource length.
type httpZipReaderAt struct {
	ctx     context.Context
	hc      *http.Client
	rawURL  string
	size    int64
	tailOff int64
	tail    []byte
}

func newHTTPZipReaderAt(ctx context.Context, hc *http.Client, rawURL string, declaredSize int64) (*httpZipReaderAt, error) {
	if hc == nil {
		hc = http.DefaultClient
	}
	r := &httpZipReaderAt{ctx: ctx, hc: hc, rawURL: rawURL}

	fetchTail := func(total int64) error {
		if total <= 0 {
			return errors.New("empty archive")
		}
		tailLen := min(total, int64(archive.ZIPMaxTailSearch))
		start := total - tailLen
		status, cr, body, err := r.fetchRange(start, total-1)
		if err != nil {
			return err
		}
		if status != http.StatusPartialContent {
			return fmt.Errorf("HTTP %d (range requests required)", status)
		}
		gotTotal := contentRangeTotal(cr)
		if gotTotal != total {
			return fmt.Errorf("unexpected Content-Range %q (want total %d)", cr, total)
		}
		if err := checkContentRange(cr, start, total); err != nil {
			return err
		}
		if int64(len(body)) != tailLen {
			return fmt.Errorf("short tail read: got %d, want %d", len(body), tailLen)
		}
		r.size = total
		r.tailOff = start
		r.tail = body
		return nil
	}

	if declaredSize > 0 {
		tailLen := min(declaredSize, int64(archive.ZIPMaxTailSearch))
		start := declaredSize - tailLen
		status, cr, body, err := r.fetchRange(start, declaredSize-1)
		if err != nil {
			return nil, err
		}
		switch status {
		case http.StatusPartialContent:
			total := contentRangeTotal(cr)
			if total <= 0 {
				return nil, fmt.Errorf("missing Content-Range total in %q", cr)
			}
			if total == declaredSize && checkContentRange(cr, start, total) == nil && int64(len(body)) == tailLen {
				r.size = total
				r.tailOff = start
				r.tail = body
				return r, nil
			}
			if err := fetchTail(total); err != nil {
				return nil, err
			}
			return r, nil
		case http.StatusRequestedRangeNotSatisfiable:
			total := contentRangeTotal(cr)
			if total <= 0 {
				return nil, fmt.Errorf("HTTP 416 with Content-Range %q", cr)
			}
			if err := fetchTail(total); err != nil {
				return nil, err
			}
			return r, nil
		default:
			return nil, fmt.Errorf("HTTP %d (range requests required)", status)
		}
	}

	status, cr, _, err := r.fetchRange(0, 0)
	if err != nil {
		return nil, err
	}
	if status != http.StatusPartialContent {
		return nil, fmt.Errorf("HTTP %d (range requests required)", status)
	}
	total := contentRangeTotal(cr)
	if total <= 0 {
		return nil, fmt.Errorf("missing Content-Range total in %q", cr)
	}
	if err := fetchTail(total); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *httpZipReaderAt) fetchRange(start, end int64) (int, string, []byte, error) {
	if start < 0 || end < start {
		return 0, "", nil, fmt.Errorf("invalid range %d-%d", start, end)
	}
	req, err := http.NewRequestWithContext(r.ctx, http.MethodGet, r.rawURL, nil)
	if err != nil {
		return 0, "", nil, err
	}
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", start, end))
	req.Header.Set("User-Agent", httpclient.UserAgent())
	resp, err := r.hc.Do(req)
	if err != nil {
		return 0, "", nil, err
	}
	defer resp.Body.Close()
	cr := resp.Header.Get("Content-Range")
	if resp.StatusCode != http.StatusPartialContent {
		return resp.StatusCode, cr, nil, nil
	}
	if ct := resp.Header.Get("Content-Type"); ct != "" {
		if mt, _, err := mime.ParseMediaType(ct); err == nil {
			if mt == "text/html" || strings.Contains(mt, "json") || strings.Contains(mt, "xml") {
				return resp.StatusCode, cr, nil, fmt.Errorf("unexpected Content-Type %q", mt)
			}
		}
	}
	want := end - start + 1
	body, err := io.ReadAll(io.LimitReader(resp.Body, want+1))
	if err != nil {
		return resp.StatusCode, cr, nil, err
	}
	if int64(len(body)) > want {
		body = body[:want]
	}
	return resp.StatusCode, cr, body, nil
}

func (r *httpZipReaderAt) ReadAt(p []byte, off int64) (int, error) {
	if off < 0 {
		return 0, errors.New("debrid: negative zip offset")
	}
	if off >= r.size {
		return 0, io.EOF
	}
	want := min(int64(len(p)), r.size-off)
	if want == 0 {
		return 0, nil
	}
	if len(r.tail) > 0 && off >= r.tailOff && off+want <= r.tailOff+int64(len(r.tail)) {
		n := copy(p[:want], r.tail[off-r.tailOff:])
		if n < len(p) {
			return n, io.EOF
		}
		return n, nil
	}
	status, cr, body, err := r.fetchRange(off, off+want-1)
	if err != nil {
		return 0, err
	}
	if status != http.StatusPartialContent {
		return 0, fmt.Errorf("debrid: zip range read HTTP %d", status)
	}
	if err := checkContentRange(cr, off, r.size); err != nil {
		return 0, err
	}
	n := copy(p[:want], body)
	if int64(n) < want {
		return n, io.ErrUnexpectedEOF
	}
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}
