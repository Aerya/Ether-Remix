package extras

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
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

const themeEntryLimit = 16 << 20

// Theme reads one durable cache entry, scheduling at most one bounded fetch on
// a detail miss. No provider, directory scan or worker exists on browse paths.
func (s *Service) Theme(t core.Title, schedule bool) bool {
	if !s.ThemeEnabled() {
		return false
	}
	id, name := themeShow(t)
	if id == 0 || name == "" {
		return false
	}
	path := s.themePath(t)
	if f, err := os.Stat(path); err == nil && f.Size() > 0 && f.Size() <= themeEntryLimit {
		return true
	}
	if !schedule || !s.themeWork.TryLock() {
		return false
	}
	s.themeMu.Lock()
	if s.themeRetry == nil {
		s.themeRetry = map[string]time.Time{}
	}
	if s.now().Before(s.themeRetry[path]) {
		s.themeMu.Unlock()
		s.themeWork.Unlock()
		return false
	}
	if len(s.themeRetry) >= 1024 {
		clear(s.themeRetry)
	}
	s.themeRetry[path] = s.now().Add(6 * time.Hour)
	s.themeMu.Unlock()
	go func() {
		defer s.themeWork.Unlock()
		ctx, cancel := context.WithTimeout(s.themeCtx, 90*time.Second)
		defer cancel()
		if err := s.fetchTheme(ctx, t, name); err != nil {
			s.log.Debug("theme unavailable", "show", id, "error", err)
		}
	}()
	return false
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

func (s *Service) ThemeEnabled() bool {
	c := s.cfg()
	return s.themeCtx.Err() == nil && c.Enabled && (c.ThemeMusic == nil || *c.ThemeMusic) && s.cacheDir != ""
}

func (s *Service) themePath(t core.Title) string {
	id, _ := themeShow(t)
	c := s.cfg()
	b, _ := json.Marshal([]any{id, c.ResolverCommand, c.ResolverArgs})
	hash := sha256.Sum256(b)
	return filepath.Join(s.cacheDir, "themes", hex.EncodeToString(hash[:])+".m4a")
}

func (s *Service) ServeTheme(w http.ResponseWriter, r *http.Request, t core.Title) {
	if !s.Theme(t, false) {
		http.NotFound(w, r)
		return
	}
	s.themeMu.Lock()
	f, err := os.Open(s.themePath(t))
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
	w.Header().Set("Content-Type", "audio/mp4")
	w.Header().Set("Cache-Control", "private, no-store")
	http.ServeContent(w, r, "theme.m4a", info.ModTime(), f)
}

func (s *Service) fetchTheme(ctx context.Context, t core.Title, name string) error {
	target := s.themePath(t)
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
	dir := filepath.Dir(s.themePath(t))
	if err = os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	// Reserve room for both staging files before any download. The bounded
	// directory is inspected only by this single demand-driven worker.
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	var files []os.FileInfo
	var used int64
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".part") {
			_ = os.Remove(filepath.Join(dir, entry.Name()))
			continue
		}
		if entry.Type().IsRegular() {
			info, e := entry.Info()
			if e != nil {
				return e
			}
			files = append(files, info)
			used += info.Size()
		}
	}
	sort.Slice(files, func(a, b int) bool { return files[a].ModTime().Before(files[b].ModTime()) })
	for len(files) > 0 && (used > (64<<20)-2*themeEntryLimit || len(files) >= 256) {
		f := files[0]
		files = files[1:]
		s.themeMu.Lock()
		err = os.Remove(filepath.Join(dir, f.Name()))
		s.themeMu.Unlock()
		if err != nil {
			return err
		}
		used -= f.Size()
	}
	req, err := http.NewRequestWithContext(ctx, "GET", resolved.URL, nil)
	if err != nil {
		return err
	}
	for k, v := range resolved.Headers {
		req.Header.Set(k, v)
	}
	client := s.httpClient(60 * time.Second)
	resp, err := client.Do(req)
	if err != nil {
		return scrubProxyErr(err, egress.Current().Proxy)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 || resp.ContentLength > themeEntryLimit {
		return errors.New("theme source exceeds bound or unavailable")
	}
	input := filepath.Join(dir, "input.part")
	output := filepath.Join(dir, "output.part")
	defer os.Remove(input)
	defer os.Remove(output)
	f, err := os.OpenFile(input, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	n, err := io.Copy(f, io.LimitReader(resp.Body, themeEntryLimit+1))
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
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
	// Single-thread audio only, capped duration and bytes. One-pass loudness
	// normalization is cheap for this 60-second clip; no video decode/output.
	process := mediaproc.CommandContext(ctx, 30*time.Second, ff, "-nostdin", "-v", "error", "-y", "-threads", "1", "-protocol_whitelist", "file,pipe", "-format_whitelist", "mov,matroska,webm,aac,mp3,ogg,wav", "-i", input, "-t", "60", "-vn", "-sn", "-dn", "-ac", "2", "-ar", "48000", "-map", "0:a:0", "-af", "loudnorm=I=-23:TP=-2:LRA=7", "-c:a", "aac", "-b:a", "128k", "-threads", "1", "-fs", "16777216", "-f", "ipod", output)
	if err = process.Run(); err != nil {
		return err
	}
	info, err := os.Stat(output)
	if err != nil {
		return err
	}
	if info.Size() == 0 || info.Size() > themeEntryLimit {
		return errors.New("invalid theme output")
	}
	if !s.ThemeEnabled() || s.themePath(t) != target {
		return errors.New("theme settings changed")
	}
	return os.Rename(output, target)
}

// Close cancels and joins any on-demand theme worker and its media subprocess.
func (s *Service) Close() {
	s.mu.Lock()
	s.listClosed = true
	s.themeCancel()
	s.mu.Unlock()
	s.listWG.Wait()
	// Join barrier: the worker holds themeWork while it runs.
	s.themeWork.Lock()
	s.themeWork.Unlock() //nolint:staticcheck // SA2001: intentional join barrier
}
