package extras

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/egress"
	"github.com/braedonsaunders/ether/internal/mediaproc"
)

// Theme containers. Plex's TV theme service answers MP3, stored byte for
// byte as PMS does; the YouTube fallback is normalised into AAC/M4A.
const (
	ThemeMP3 = "mp3"
	ThemeM4A = "m4a"
)

const (
	themeEntryLimit = 16 << 20
	// themeFirstOpenWait bounds how long a show/season detail open waits for a
	// fetch it started or joined. A Plex theme is one ~0.1 s download.
	themeFirstOpenWait = 1500 * time.Millisecond
	themeMaxFlights    = 4
	themeRetryEntries  = 1024

	themeRetryTransient = 15 * time.Minute // network or 5xx from the theme service
	themeRetryAbsent    = 24 * time.Hour   // no theme anywhere for this show
	themeRetryFailed    = 6 * time.Hour    // fallback resolver/encoder failure
	themeBotFirst       = time.Hour        // first YouTube bot-check pause, doubling
	themeBotMax         = 24 * time.Hour
)

// plexThemeBase is Plex's public TV theme service, keyed by TVDB series id.
// It is the source PMS uses for show themes.
var plexThemeBase = "https://tvthemes.plexapp.com"

var (
	errPlexNoTheme = errors.New("plex theme service has no theme for this series")
	errBotCheck    = errors.New("YouTube asked the resolver to sign in (bot check)")
)

type themeFlight struct{ done chan struct{} }

// Theme reports the cached theme container ("" when none) for a show, or the
// show of a season/episode. With schedule (detail opens only) a miss starts or
// joins one bounded fetch, and show/season opens wait up to themeFirstOpenWait
// for it. Nothing here scans directories or prefetches unopened shows.
func (s *Service) Theme(t core.Title, schedule bool) string {
	if !s.ThemeEnabled() {
		return ""
	}
	id, name := themeShow(t)
	if id == 0 || name == "" {
		return ""
	}
	cached := s.cachedTheme(id)
	if cached == ThemeMP3 || !schedule {
		return cached
	}
	if cached == ThemeM4A {
		// A YouTube guess is cached; replace it with the canonical Plex theme
		// when the series has a TVDB id. The guess keeps playing meanwhile.
		if themeTVDB(t) > 0 {
			s.startTheme(t, true)
		}
		return cached
	}
	f := s.startTheme(t, false)
	if f == nil || s.themeWait <= 0 {
		return ""
	}
	switch t.Key.Kind() {
	case core.KindShow, core.KindSeason:
	default:
		// Episode metadata is also fetched on the way to playback; never hold it.
		return ""
	}
	timer := time.NewTimer(s.themeWait)
	defer timer.Stop()
	select {
	case <-f.done:
	case <-timer.C:
	case <-s.themeCtx.Done():
	}
	return s.cachedTheme(id)
}

// startTheme starts (or joins) the fetch for t's show. It returns nil when the
// show is cooling down after a failure, or the concurrency bound is reached.
func (s *Service) startTheme(t core.Title, plexOnly bool) *themeFlight {
	id, _ := themeShow(t)
	s.themeMu.Lock()
	defer s.themeMu.Unlock()
	if f := s.themeFlights[id]; f != nil {
		return f
	}
	if s.themeCtx.Err() != nil || s.now().Before(s.themeRetry[id]) || len(s.themeFlights) >= themeMaxFlights {
		return nil
	}
	if s.themeFlights == nil {
		s.themeFlights = map[core.RatingKey]*themeFlight{}
	}
	f := &themeFlight{done: make(chan struct{})}
	s.themeFlights[id] = f
	s.themeWG.Add(1)
	go func() {
		defer s.themeWG.Done()
		ctx, cancel := context.WithTimeout(s.themeCtx, 90*time.Second)
		retry, err := s.fetchTheme(ctx, t, plexOnly)
		cancel()
		s.themeMu.Lock()
		delete(s.themeFlights, id)
		if retry > 0 {
			if s.themeRetry == nil || len(s.themeRetry) >= themeRetryEntries {
				s.themeRetry = map[core.RatingKey]time.Time{}
			}
			s.themeRetry[id] = s.now().Add(retry)
		}
		s.themeMu.Unlock()
		close(f.done)
		switch {
		case err == nil, errors.Is(err, context.Canceled), errors.Is(err, errBotCheck):
		case errors.Is(err, errPlexNoTheme):
			s.log.Debug("no theme for show", "show", id, "tvdb", themeTVDB(t), "reason", err)
		default:
			s.log.Info("theme fetch failed", "show", id, "tvdb", themeTVDB(t), "retryIn", retry.Round(time.Minute).String(), "error", err)
		}
	}()
	return f
}

func themeShow(t core.Title) (core.RatingKey, string) {
	switch t.Key.Kind() {
	case core.KindShow:
		return t.Key, t.Title
	case core.KindSeason, core.KindEpisode:
		return t.ShowKey, t.ShowTitle
	}
	return 0, ""
}

func themeTVDB(t core.Title) int64 {
	if t.Key.Kind() == core.KindShow {
		return t.IDs.TVDB
	}
	return t.ShowIDs.TVDB
}

// ThemeShowKey is the show whose theme t plays (0 when t has none).
func ThemeShowKey(t core.Title) core.RatingKey {
	id, _ := themeShow(t)
	return id
}

func (s *Service) ThemeEnabled() bool {
	c := s.cfg()
	return s.themeCtx.Err() == nil && c.Enabled && (c.ThemeMusic == nil || *c.ThemeMusic) && s.cacheDir != ""
}

func (s *Service) themeDir() string { return filepath.Join(s.cacheDir, "themes") }

// plexThemePath depends only on the show, so a cached Plex theme survives
// resolver changes.
func (s *Service) plexThemePath(id core.RatingKey) string {
	hash := sha256.Sum256([]byte("plex-tvthemes:" + id.String()))
	return filepath.Join(s.themeDir(), hex.EncodeToString(hash[:])+".mp3")
}

// resolverThemePath is keyed by the resolver, so changing it refetches.
func (s *Service) resolverThemePath(id core.RatingKey) string {
	c := s.cfg()
	b, _ := json.Marshal([]any{id, c.ResolverCommand, c.ResolverArgs})
	hash := sha256.Sum256(b)
	return filepath.Join(s.themeDir(), hex.EncodeToString(hash[:])+".m4a")
}

func themeFileOK(path string) bool {
	f, err := os.Stat(path)
	return err == nil && f.Mode().IsRegular() && f.Size() > 0 && f.Size() <= themeEntryLimit
}

// cachedTheme is at most two stats; the Plex theme wins over a YouTube guess.
func (s *Service) cachedTheme(id core.RatingKey) string {
	if themeFileOK(s.plexThemePath(id)) {
		return ThemeMP3
	}
	if themeFileOK(s.resolverThemePath(id)) {
		return ThemeM4A
	}
	return ""
}

func (s *Service) ServeTheme(w http.ResponseWriter, r *http.Request, t core.Title) {
	id, _ := themeShow(t)
	container := s.Theme(t, false)
	path, contentType := s.plexThemePath(id), "audio/mpeg"
	switch container {
	case ThemeMP3:
	case ThemeM4A:
		path, contentType = s.resolverThemePath(id), "audio/mp4"
	default:
		http.NotFound(w, r)
		return
	}
	s.themeMu.Lock()
	f, err := os.Open(path)
	s.themeMu.Unlock()
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "private, no-store")
	http.ServeContent(w, r, "theme."+container, info.ModTime(), f)
}

// fetchTheme tries Plex's theme service by TVDB id, then (only when Plex has
// no theme, or the series has no TVDB id) the owner's YouTube resolver. It
// returns how long to wait before trying this show again (0 on success).
func (s *Service) fetchTheme(ctx context.Context, t core.Title, plexOnly bool) (time.Duration, error) {
	id, name := themeShow(t)
	if tvdb := themeTVDB(t); tvdb > 0 {
		err := s.fetchPlexTheme(ctx, id, tvdb)
		switch {
		case err == nil:
			// The canonical theme replaces any cached YouTube guess.
			s.themeMu.Lock()
			_ = os.Remove(s.resolverThemePath(id))
			s.themeMu.Unlock()
			return 0, nil
		case !errors.Is(err, errPlexNoTheme):
			return themeRetryTransient, err
		}
		if plexOnly {
			return themeRetryAbsent, err
		}
	}
	if _, ok := s.effectiveCommand(s.cfg()); !ok {
		return themeRetryAbsent, errPlexNoTheme
	}
	s.themeMu.Lock()
	paused := s.themeBotUntil
	s.themeMu.Unlock()
	if s.now().Before(paused) {
		return paused.Sub(s.now()), errBotCheck
	}
	err := s.fetchResolverTheme(ctx, t, name)
	switch {
	case err == nil:
		s.themeMu.Lock()
		s.themeBotStep = 0
		s.themeMu.Unlock()
		return 0, nil
	case isBotCheck(err):
		return s.pauseForBotCheck(), errBotCheck
	}
	return themeRetryFailed, err
}

// pauseForBotCheck stops every YouTube theme lookup for a doubling period and
// logs the reason once per pause instead of retrying each show silently.
func (s *Service) pauseForBotCheck() time.Duration {
	s.themeMu.Lock()
	defer s.themeMu.Unlock()
	if now := s.now(); now.Before(s.themeBotUntil) {
		return s.themeBotUntil.Sub(now)
	}
	if s.themeBotStep == 0 {
		s.themeBotStep = themeBotFirst
	} else {
		s.themeBotStep = min(2*s.themeBotStep, themeBotMax)
	}
	s.themeBotUntil = s.now().Add(s.themeBotStep)
	s.log.Info("theme fallback paused: YouTube asked the resolver to sign in (bot check); Plex themes are unaffected",
		"retryIn", s.themeBotStep.String())
	return s.themeBotStep
}

func isBotCheck(err error) bool {
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "not a bot") || strings.Contains(msg, "confirm you're not") || strings.Contains(msg, "confirm you’re not")
}

func (s *Service) fetchPlexTheme(ctx context.Context, id core.RatingKey, tvdb int64) error {
	target := s.plexThemePath(id)
	src := fmt.Sprintf("%s/%d.mp3", plexThemeBase, tvdb)
	if _, _, _, err := extrasProxyFor(src); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, "GET", src, nil)
	if err != nil {
		return err
	}
	resp, err := s.httpClient(30 * time.Second).Do(req)
	if err != nil {
		return scrubProxyErr(err, egress.Current().Proxy)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusForbidden:
		return errPlexNoTheme
	case resp.StatusCode != http.StatusOK:
		return fmt.Errorf("plex theme service: HTTP %d", resp.StatusCode)
	case resp.ContentLength > themeEntryLimit:
		return errors.New("plex theme exceeds bound")
	}
	if mt, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type")); mt != "" && !strings.HasPrefix(mt, "audio/") && mt != "application/octet-stream" {
		return errPlexNoTheme
	}
	dir, err := s.prepareThemeDir()
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "plex-*.part")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	n, err := io.Copy(tmp, io.LimitReader(resp.Body, themeEntryLimit+1))
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if n == 0 || n > themeEntryLimit {
		return errors.New("plex theme exceeds bound")
	}
	if !looksLikeMP3(tmp.Name()) {
		return errPlexNoTheme
	}
	if !s.ThemeEnabled() {
		return errors.New("theme settings changed")
	}
	return os.Rename(tmp.Name(), target)
}

// looksLikeMP3 accepts an ID3 tag or an MPEG audio frame sync.
func looksLikeMP3(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	var b [3]byte
	if _, err = io.ReadFull(f, b[:]); err != nil {
		return false
	}
	return bytes.Equal(b[:], []byte("ID3")) || (b[0] == 0xFF && b[1]&0xE0 == 0xE0)
}

// prepareThemeDir keeps the cache within 64 MiB / 256 entries before a
// download. Fetches run concurrently, so eviction is serialised and only
// abandoned staging files (older than any live fetch) are swept.
func (s *Service) prepareThemeDir() (string, error) {
	dir := s.themeDir()
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	s.themeEvictMu.Lock()
	defer s.themeEvictMu.Unlock()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	var files []os.FileInfo
	var used int64
	for _, entry := range entries {
		if !entry.Type().IsRegular() {
			continue
		}
		info, e := entry.Info()
		if e != nil {
			continue
		}
		if strings.HasSuffix(entry.Name(), ".part") {
			if s.now().Sub(info.ModTime()) > 10*time.Minute {
				_ = os.Remove(filepath.Join(dir, entry.Name()))
			}
			continue
		}
		files = append(files, info)
		used += info.Size()
	}
	sort.Slice(files, func(a, b int) bool { return files[a].ModTime().Before(files[b].ModTime()) })
	for len(files) > 0 && (used > (64<<20)-2*themeEntryLimit || len(files) >= 256) {
		f := files[0]
		files = files[1:]
		s.themeMu.Lock()
		err = os.Remove(filepath.Join(dir, f.Name()))
		s.themeMu.Unlock()
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		used -= f.Size()
	}
	return dir, nil
}

func (s *Service) fetchResolverTheme(ctx context.Context, t core.Title, name string) error {
	id, _ := themeShow(t)
	target := s.resolverThemePath(id)
	c := s.cfg()
	cmd, ok := s.effectiveCommand(c)
	if !ok {
		return errors.New("theme resolver unavailable")
	}
	proxy, policyEnabled, bypass, err := extrasProxyFor("https://www.youtube.com")
	if err != nil {
		return err
	}
	// Use the owner's yt-dlp-compatible resolver and arguments, including its
	// account/network settings. Ether supplies only a bounded search query.
	query := "ytsearch1:" + name + " opening theme song"
	args := c.ResolverArgs
	customArgs := len(args) > 0
	if !customArgs {
		args = []string{"-g", "-f", "bestaudio[ext=m4a]/bestaudio", "--no-playlist", "{url}"}
	}
	sub := strings.NewReplacer("{url}", query, "{site}", "youtube", "{key}", query, "{id}", query)
	out := make([]string, 0, len(args)+2)
	if proxy != nil && !customArgs {
		out = append(out, "--proxy", proxy.String())
	}
	for _, a := range args {
		out = append(out, sub.Replace(a))
	}
	resolveCtx, cancel := context.WithTimeout(ctx, c.ResolverTimeoutOr(20*time.Second))
	resolveCtx = withRunnerProxy(resolveCtx, runnerProxyConfig{proxy: proxy, policyEnabled: policyEnabled, bypass: bypass})
	raw, err := s.runner(resolveCtx, cmd, out)
	cancel()
	if err != nil {
		return scrubProxyErr(err, proxy)
	}
	if len(raw) > maxResolverOutput {
		return errors.New("theme resolver output too large")
	}
	resolved, err := parseResolverOutput(raw, s.now())
	if err != nil {
		return err
	}
	dir, err := s.prepareThemeDir()
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, "GET", resolved.URL, nil)
	if err != nil {
		return err
	}
	for k, v := range resolved.Headers {
		req.Header.Set(k, v)
	}
	resp, err := s.httpClient(60 * time.Second).Do(req)
	if err != nil {
		return scrubProxyErr(err, egress.Current().Proxy)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 || resp.ContentLength > themeEntryLimit {
		return errors.New("theme source exceeds bound or unavailable")
	}
	in, err := os.CreateTemp(dir, "in-*.part")
	if err != nil {
		return err
	}
	input := in.Name()
	defer os.Remove(input)
	n, err := io.Copy(in, io.LimitReader(resp.Body, themeEntryLimit+1))
	if closeErr := in.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if n == 0 || n > themeEntryLimit {
		return errors.New("theme exceeds bound")
	}
	ff := "ffmpeg"
	if s.themeFFmpeg != nil {
		ff = s.themeFFmpeg()
	}
	if ff == "" {
		return errors.New("theme audio encoder unavailable")
	}
	output := strings.TrimSuffix(input, ".part") + ".out.part"
	defer os.Remove(output)
	// Single-thread audio only, capped duration and bytes. One-pass loudness
	// normalization is cheap for this 60-second clip; no video decode/output.
	process := mediaproc.CommandContext(ctx, 30*time.Second, ff, "-nostdin", "-v", "error", "-y", "-threads", "1", "-protocol_whitelist", "file,pipe", "-format_whitelist", "mov,matroska,webm,aac,mp3,ogg,wav", "-i", input, "-t", "60", "-vn", "-sn", "-dn", "-ac", "2", "-ar", "48000", "-map", "0:a:0", "-af", "loudnorm=I=-23:TP=-2:LRA=7", "-c:a", "aac", "-b:a", "128k", "-threads", "1", "-fs", "16777216", "-f", "ipod", output)
	if err = process.Run(); err != nil {
		return err
	}
	if !themeFileOK(output) {
		return errors.New("invalid theme output")
	}
	if !s.ThemeEnabled() || s.resolverThemePath(id) != target {
		return errors.New("theme settings changed")
	}
	return os.Rename(output, target)
}

// Close cancels and joins any on-demand theme fetch and its media subprocess.
func (s *Service) Close() {
	s.mu.Lock()
	s.listClosed = true
	s.themeMu.Lock() // startTheme adds to themeWG only while themeCtx is live
	s.themeCancel()
	s.themeMu.Unlock()
	s.mu.Unlock()
	s.listWG.Wait()
	s.themeWG.Wait()
}
