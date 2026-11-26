package livetv

import (
	"context"
	"errors"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
)

var errArchiveURL = errors.New("invalid channel archive URL")

// URL builds a playback URL from a channel whose inputs have been opened.
// For a fixed range its end is the reference time for relative placeholders.
// Service.ArchiveURL supplies the request clock for provider playback.
func (a ChannelArchive) URL(ch Channel, start time.Time, dur time.Duration) (string, error) {
	return a.urlAt(ch, start, dur, start.Add(dur))
}

func (a ChannelArchive) urlAt(ch Channel, start time.Time, dur time.Duration, now time.Time) (string, error) {
	if a.Days < 1 || a.Days > 30 || dur <= 0 || config.IsSealed(a.Source) {
		return "", errArchiveURL
	}
	live := ch.StreamURL
	playlist := stableID(ch.ID, "playlist")
	for _, input := range ch.StreamSources {
		if input.ID == playlist {
			live = input.URL
			break
		}
	}

	var raw string
	switch a.Mode {
	case "xtream", "xc":
		base, user, pass, id := xtreamLiveParts(live)
		if base == "" {
			if a.Mode == "xc" && a.Source != "" {
				raw = archiveTemplate(a.Source, start, dur, now)
				break
			}
			return "", errArchiveURL
		}
		minutes := 1 + (dur-1)/time.Minute
		stamp := start.In(zoneOf(ch.archiveTimezone)).Format("2006-01-02:15-04")
		raw = base + "/timeshift/" + user + "/" + pass + "/" + strconv.FormatInt(int64(minutes), 10) + "/" + stamp + "/" + id + ".ts"
	case "default":
		raw = archiveTemplate(a.Source, start, dur, now)
	case "append", "shift", "flussonic":
		u, err := url.Parse(live)
		if err != nil || u.Host == "" || u.Scheme != "http" && u.Scheme != "https" {
			return "", errArchiveURL
		}
		switch a.Mode {
		case "append":
			if a.Source == "" {
				return "", errArchiveURL
			}
			fragment := ""
			if at := strings.IndexByte(live, '#'); at >= 0 {
				live, fragment = live[:at], live[at:]
			}
			tail := archiveTemplate(a.Source, start, dur, now)
			if strings.HasPrefix(tail, "?") {
				switch {
				case u.RawQuery != "":
					tail = "&" + tail[1:]
				case u.ForceQuery:
					tail = tail[1:]
				}
			}
			raw = live + tail + fragment
		case "shift":
			if u.RawQuery != "" {
				u.RawQuery += "&"
			}
			u.RawQuery += "utc=" + strconv.FormatInt(start.Unix(), 10) + "&lutc=" + strconv.FormatInt(now.Unix(), 10)
			raw = u.String()
		case "flussonic":
			path := u.EscapedPath()
			slash := strings.LastIndexByte(path, '/')
			prefix, file := path[:max(slash, 0)], path[slash+1:]
			stamp := strconv.FormatInt(start.Unix(), 10)
			switch {
			case strings.HasSuffix(strings.ToLower(file), ".m3u8"):
				file = "index-" + stamp + "-" + strconv.FormatInt(int64(dur/time.Second), 10) + ".m3u8"
			default:
				if file != "" && file != "mpegts" && !strings.HasSuffix(strings.ToLower(file), ".ts") {
					prefix += "/" + file
				}
				file = "timeshift_abs-" + stamp + ".ts"
			}
			u.RawPath = prefix + "/" + file
			u.Path, _ = url.PathUnescape(u.RawPath)
			raw = u.String()
		}
	default:
		return "", errArchiveURL
	}
	if !validHTTP(raw) || strings.ContainsAny(raw, "{}\r\n\x00") {
		return "", errArchiveURL
	}
	return raw, nil
}

// xtreamLiveParts keeps percent escapes in the credentials and any panel path
// prefix. Queries and fragments on a live URL are separate from its login.
func xtreamLiveParts(raw string) (base, user, pass, id string) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.Scheme != "http" && u.Scheme != "https" {
		return "", "", "", ""
	}
	parts := strings.Split(u.EscapedPath(), "/")
	n := len(parts)
	if n < 5 || !strings.EqualFold(parts[n-4], "live") || parts[n-3] == "" || parts[n-2] == "" {
		return "", "", "", ""
	}
	match := trailingStreamID.FindStringSubmatch("/" + parts[n-1])
	if match == nil {
		return "", "", "", ""
	}
	u.RawPath = strings.Join(parts[:n-4], "/")
	u.Path, _ = url.PathUnescape(u.RawPath)
	u.RawQuery, u.Fragment, u.RawFragment, u.ForceQuery = "", "", "", false
	return u.String(), parts[n-3], parts[n-2], match[1]
}

var archivePlaceholder = regexp.MustCompile(`\$?\{([^{}]+)\}`)
var archiveDateLayout = strings.NewReplacer("Y", "2006", "m", "01", "d", "02", "H", "15", "M", "04", "S", "05")

func archiveTemplate(template string, start time.Time, dur time.Duration, now time.Time) string {
	start = start.UTC()
	end := start.Add(dur)
	now = now.UTC()
	return archivePlaceholder.ReplaceAllStringFunc(template, func(token string) string {
		key := strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(token, "$"), "{"), "}")
		if layout, ok := strings.CutPrefix(key, "(b)"); ok {
			return start.Format(archiveDateLayout.Replace(layout))
		}
		key, format, _ := strings.Cut(key, ":")
		var at time.Time
		switch key {
		case "utc", "start":
			at = start
		case "utcend", "end":
			at = end
		case "lutc", "now", "timestamp":
			at = now
		case "Y", "m", "d", "H", "M", "S":
			return start.Format(archiveDateLayout.Replace(key))
		case "duration", "offset":
			seconds := int64(dur / time.Second)
			if key == "offset" {
				seconds = int64(now.Sub(start) / time.Second)
			}
			if format != "" {
				divisor, err := strconv.ParseInt(format, 10, 64)
				if err != nil || divisor < 1 {
					return token
				}
				seconds /= divisor
			}
			return strconv.FormatInt(seconds, 10)
		default:
			return token
		}
		if format != "" {
			return at.Format(archiveDateLayout.Replace(format))
		}
		return strconv.FormatInt(at.Unix(), 10)
	})
}

// ArchiveURL resolves the archive for a full, internal channel record. The
// caller must apply viewer access and validate the requested time range.
func (s *Service) ArchiveURL(ctx context.Context, ch Channel, start time.Time, dur time.Duration) (string, error) {
	if ch.Archive == nil {
		return "", errArchiveURL
	}
	a := *ch.Archive
	if a.Source != "" {
		opened, _, err := config.OpenWith(s.o.Sealer, a.Source, archiveSourceContext(ch)...)
		if err != nil {
			return "", errArchiveURL
		}
		a.Source = opened
	}
	if a.Mode == "xtream" && ch.SourceID != "" {
		src, err := s.storedSourceByID(ctx, ch.SourceID)
		if err != nil {
			return "", errArchiveURL
		}
		ch.archiveTimezone = src.ArchiveTimezone
	}
	return a.urlAt(ch, start, dur, s.o.Now())
}

// ArchiveDays returns the public archive window in days.
func (ch Channel) ArchiveDays() int {
	if ch.Archive == nil {
		return 0
	}
	return ch.Archive.Days
}

func (ch *Channel) redactArchiveSource() {
	if ch.Archive != nil && ch.Archive.Source != "" {
		a := *ch.Archive
		a.Source = ""
		ch.Archive = &a
	}
}
