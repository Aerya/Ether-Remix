package mediabrowser

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/kit/ids"
	"github.com/braedonsaunders/ether/internal/frontend/kit/route"
)

// ticksPerMillisecond converts milliseconds to .NET ticks (100 ns), the unit
// Jellyfin's lyric timings use.
const ticksPerMillisecond = 10_000

// audioLyrics serves Jellyfin's GET /Audio/{itemId}/Lyrics (10.9+) from the
// title's lyrics source, as a LyricDto: Metadata plus Lyrics lines. A synced
// line carries Start (ticks) and, with word timing, Cues; an unsynced line
// carries Text only, because Jellyfin Web treats any line with a Start
// property as synced.
func (i *instance) audioLyrics(w http.ResponseWriter, r *http.Request) {
	v, _, err := i.viewer(r)
	if err != nil || v == nil {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	source, ok := i.host.Experience.(frontend.TitleLyrics)
	if !ok {
		http.NotFound(w, r)
		return
	}
	key, err := ids.ParseRatingKeyGUID(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	lyrics, err := source.TitleLyrics(i.identityContext(r), v, key)
	if errors.Is(err, frontend.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	jsonReply(w, jellyfinLyricDTO(lyrics))
}

func jellyfinLyricDTO(l frontend.Lyrics) map[string]any {
	lines := make([]map[string]any, 0, len(l.Lines))
	synced := len(l.Lines) > 0
	for _, line := range l.Lines {
		wire := map[string]any{"Text": line.Text, "Start": line.Start.Milliseconds() * ticksPerMillisecond}
		if cues := jellyfinLyricCues(line); len(cues) > 0 {
			wire["Cues"] = cues
		}
		lines = append(lines, wire)
	}
	if !synced {
		for _, text := range strings.Split(strings.ReplaceAll(l.Plain, "\r\n", "\n"), "\n") {
			lines = append(lines, map[string]any{"Text": text})
		}
		for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]["Text"].(string)) == "" {
			lines = lines[:len(lines)-1]
		}
	}
	if len(lines) == 0 && l.Instrumental {
		lines = append(lines, map[string]any{"Text": "♪"})
	}
	return map[string]any{"Metadata": map[string]any{"IsSynced": synced}, "Lyrics": lines}
}

// jellyfinLyricCues maps word timing to Jellyfin cues: each word's character
// range in the line's text and its start (and end) in ticks.
func jellyfinLyricCues(line frontend.LyricLine) []map[string]any {
	if len(line.Words) == 0 {
		return nil
	}
	cues := make([]map[string]any, 0, len(line.Words))
	byteOffset, utf16Offset := 0, 0
	for _, word := range line.Words {
		text := word.Text
		at := strings.Index(line.Text[byteOffset:], text)
		if text == "" || at < 0 {
			return nil
		}
		// Jellyfin and its web client index strings in UTF-16 code units.
		start := utf16Offset + utf16Length(line.Text[byteOffset:byteOffset+at])
		end := start + utf16Length(text)
		byteOffset += at + len(text)
		utf16Offset = end
		cue := map[string]any{"Position": start, "EndPosition": end, "Start": word.Start.Milliseconds() * ticksPerMillisecond}
		if word.End > word.Start {
			cue["End"] = word.End.Milliseconds() * ticksPerMillisecond
		}
		cues = append(cues, cue)
	}
	return cues
}

func utf16Length(text string) int {
	n := 0
	for _, r := range text {
		n++
		if r > 0xffff {
			n++
		}
	}
	return n
}

// lyricsLikely is the cheap HasLyrics hint for an audio item: what Ether
// already knows, never a call to the plugin.
func (i *instance) lyricsLikely(t frontend.Title) bool {
	if t.MBItemType != "Audio" || !t.Key.IsPluginKey() {
		return false
	}
	hint, ok := i.host.Experience.(frontend.TitleLyricsHint)
	return ok && hint.TitleLyricsAvailability(t) != frontend.LyricsAbsent
}

// Emby has no lyrics endpoint: a track's lyrics are a text subtitle stream
// (Emby Server lists a sidecar .lrc that way). Emby Web shows its lyrics
// button when the item's first media source lists a Subtitle stream, and
// reads Items/{id}/{source}/Subtitles/{DefaultSubtitleStreamIndex}/Stream.js.
// The stream goes on the item detail only, never on PlaybackInfo, so a
// track's playback keeps no subtitle selection.
func (i *instance) embyLyricStream(dto map[string]any, t frontend.Title) {
	if i.protocol != "emby" || !i.lyricsLikely(t) {
		return
	}
	sources, _ := dto["MediaSources"].([]any)
	if len(sources) == 0 {
		id, _ := dto["Id"].(string)
		if id == "" {
			return
		}
		top, _ := dto["MediaStreams"].([]any)
		source := map[string]any{"Id": id, "Name": t.Title, "Protocol": "File", "Type": "Default", "IsRemote": false, "MediaStreams": append([]any(nil), top...)}
		for _, key := range []string{"RunTimeTicks", "Container", "Size", "Bitrate"} {
			if value, ok := dto[key]; ok {
				source[key] = value
			}
		}
		sources = []any{source}
		dto["MediaSources"] = sources
	}
	// One index for the lyric stream everywhere: a source may list fewer
	// streams than the item, and apps read either list.
	top, _ := dto["MediaStreams"].([]any)
	index := nextMediaStreamIndex(top)
	for _, entry := range sources {
		if source, ok := entry.(map[string]any); ok {
			streams, _ := source["MediaStreams"].([]any)
			index = max(index, nextMediaStreamIndex(streams))
		}
	}
	for _, entry := range sources {
		source, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		streams, _ := source["MediaStreams"].([]any)
		source["MediaStreams"] = append(append([]any(nil), streams...), embyLyricStreamDTO(index))
		source["DefaultSubtitleStreamIndex"] = index
	}
	dto["MediaStreams"] = append(append([]any(nil), top...), embyLyricStreamDTO(index))
	dto["HasSubtitles"] = true
}

func nextMediaStreamIndex(streams []any) int {
	next := 0
	for _, entry := range streams {
		if stream, ok := entry.(map[string]any); ok {
			if index, ok := stream["Index"].(int); ok && index >= next {
				next = index + 1
			}
		}
	}
	return next
}

// embyLyricStreamDTO is the stream Emby Server lists for an external .lrc.
func embyLyricStreamDTO(index int) map[string]any {
	return map[string]any{
		"Type":                   "Subtitle",
		"Index":                  index,
		"Codec":                  "lrc",
		"IsExternal":             true,
		"IsTextSubtitleStream":   true,
		"SupportsExternalStream": true,
		"IsDefault":              true,
		"IsForced":               false,
		"Protocol":               "File",
		"DeliveryMethod":         "External",
		"ExtendedVideoType":      "None",
		"AttachmentSize":         0,
	}
}

// embyItemSubtitle reads item-level lyrics. Session requests are playback
// subtitles and go through the existing session authorization path.
func (i *instance) embyItemSubtitle(w http.ResponseWriter, r *http.Request) {
	if len(route.Query(r.URL.Query(), "PlaySessionId")) != 0 || !strings.EqualFold(r.PathValue("name"), "Stream.js") {
		i.subtitle(w, r)
		return
	}

	v, _, err := i.viewer(r)
	if err != nil || v == nil {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	key := i.embyLyricItemKey(r, v, r.PathValue("id"))
	index, err := strconv.Atoi(r.PathValue("index"))
	if !key.IsPluginKey() || err != nil || index < 0 ||
		i.embyLyricItemKey(r, v, r.PathValue("source")) != key {
		http.NotFound(w, r)
		return
	}
	source, ok := i.host.Experience.(frontend.TitleLyrics)
	if !ok {
		http.NotFound(w, r)
		return
	}
	lyrics, err := source.TitleLyrics(i.identityContext(r), v, key)
	if errors.Is(err, frontend.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	if r.Method == http.MethodHead {
		w.Header().Set("Content-Type", "application/json")
		return
	}
	jsonReply(w, map[string]any{"TrackEvents": embyLyricTrackEvents(lyrics)})
}

// Each version of an item shares one lyric resource. The index is local to
// the detail response and may change as media stream facts become available.
func (i *instance) embyLyricItemKey(r *http.Request, viewer *frontend.Viewer, id string) frontend.RatingKey {
	if key, err := ids.ParseRatingKeyGUID(id); err == nil {
		return key
	}
	if i.host.VersionHandles == nil {
		return 0
	}
	scope, err := i.versionScope(r, viewer)
	if err != nil {
		return 0
	}
	version, err := i.host.VersionHandles.Resolve(i.identityContext(r), scope, id)
	if err != nil {
		return 0
	}
	return version.Item
}

// embyLyricTrackEvents renders lyrics as Emby subtitle events. Synced lines
// carry StartPositionTicks (Emby Web highlights by it) and end where the next
// line starts; plain lines carry text only.
func embyLyricTrackEvents(l frontend.Lyrics) []map[string]any {
	events := make([]map[string]any, 0, len(l.Lines))
	for n, line := range l.Lines {
		start := line.Start.Milliseconds() * ticksPerMillisecond
		end := start
		if n+1 < len(l.Lines) {
			end = l.Lines[n+1].Start.Milliseconds() * ticksPerMillisecond
		}
		events = append(events, map[string]any{"Id": strconv.Itoa(n + 1), "Text": line.Text, "StartPositionTicks": start, "EndPositionTicks": end})
	}
	if len(events) == 0 {
		for _, text := range strings.Split(strings.TrimRight(strings.ReplaceAll(l.Plain, "\r\n", "\n"), "\n \t"), "\n") {
			if text == "" && len(events) == 0 {
				continue
			}
			events = append(events, map[string]any{"Id": strconv.Itoa(len(events) + 1), "Text": text})
		}
	}
	if len(events) == 0 && l.Instrumental {
		events = append(events, map[string]any{"Id": "1", "Text": "♪"})
	}
	return events
}
