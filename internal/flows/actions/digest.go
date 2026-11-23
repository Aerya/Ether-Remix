package actions

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"html/template"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/events"
	"github.com/braedonsaunders/ether/internal/flows"
)

const (
	defaultDigestWindow   = 7 * 24 * time.Hour
	maxDigestWindow       = 90 * 24 * time.Hour
	defaultDigestMaxItems = 50
	hardDigestMaxItems    = 200
)

// NewDigestGenerate constructs the built-in digest.generate action handler.
// It produces a grouped "Recently Added" and/or activity newsletter digest in
// both responsive HTML (auto-escaped) and plain text, collapsing multiple
// episodes into seasons and shows, with poster thumbnails and optional
// shareable self-hosted HTML output.
func NewDigestGenerate(deps Deps) flows.ActionHandler {
	spec := flows.ActionSpec{
		Type:        "digest.generate",
		Owner:       events.OwnerBuiltin,
		Group:       "notifications",
		Label:       "Generate digest newsletter",
		Description: "Build a grouped Recently Added or activity newsletter digest for a time window (HTML + plain text with poster links and optional shareable URL).",
		Icon:        "newspaper",
		Idempotent:  true,
		SideEffect:  false,
		Params: []flows.ParamSchema{
			{Key: "window", Type: events.FieldString, Label: "Time window", Description: "Lookback window (for example 24h, 7d, 14d, 1w; max 90d).", Default: "7d", Template: true},
			{
				Key:     "kind",
				Type:    events.FieldEnum,
				Label:   "Digest kind",
				Default: "recently_added",
				Enum: []events.EnumOption{
					{Value: "recently_added", Label: "Recently added"},
					{Value: "activity", Label: "Playback activity"},
					{Value: "combined", Label: "Recently added + activity"},
				},
			},
			{Key: "libraryIds", Type: events.FieldStringList, Label: "Libraries", Description: "Optional list of library IDs to include.", Picker: flows.PickerLibrary},
			{Key: "mediaTypes", Type: events.FieldStringList, Label: "Media types", Description: "Optional filter (movie, show, episode)."},
			{Key: "groupEpisodes", Type: events.FieldBool, Label: "Group episodes by show & season", Description: "Collapse multiple new episodes from the same show and season into one compact summary.", Default: true},
			{Key: "includeActivity", Type: events.FieldBool, Label: "Include activity stats", Description: "Include play counts, unique viewers, and top titles when kind is activity or combined.", Default: true},
			{Key: "title", Type: events.FieldString, Label: "Digest title", Description: "Optional custom newsletter headline.", Template: true},
			{Key: "maxItems", Type: events.FieldInt, Label: "Max items", Description: "Maximum recently added items to include (1–200).", Default: defaultDigestMaxItems},
			{Key: "saveShareable", Type: events.FieldBool, Label: "Save shareable HTML", Description: "Persist rendered HTML via the export store and output shareUrl.", Default: false},
		},
		Outputs: []events.FieldSchema{
			{Name: "subject", Type: events.FieldString, Label: "Subject / title"},
			{Name: "text", Type: events.FieldString, Label: "Plain text digest"},
			{Name: "html", Type: events.FieldString, Label: "HTML newsletter"},
			{Name: "shareUrl", Type: events.FieldString, Label: "Shareable URL"},
			{Name: "movieCount", Type: events.FieldInt, Label: "New movies"},
			{Name: "showCount", Type: events.FieldInt, Label: "Shows with updates"},
			{Name: "episodeCount", Type: events.FieldInt, Label: "New episodes"},
			{Name: "totalItems", Type: events.FieldInt, Label: "Total items"},
			{Name: "totalPlays", Type: events.FieldInt, Label: "Total plays"},
			{Name: "windowStart", Type: events.FieldTimestamp, Label: "Window start"},
			{Name: "windowEnd", Type: events.FieldTimestamp, Label: "Window end"},
		},
	}

	return flows.ActionFunc{ActionSpec: spec, Run: func(ctx context.Context, actx flows.ActionContext, params map[string]any) (flows.ActionResult, error) {
		windowRaw := strings.TrimSpace(paramString(params, "window", "7d"))
		windowDur, err := parseHumanDuration(windowRaw)
		if err != nil {
			return flows.ActionResult{}, fmt.Errorf("digest.generate: invalid window: %w", err)
		}
		if windowDur > maxDigestWindow {
			return flows.ActionResult{}, errors.New("digest.generate: window cannot exceed 90d")
		}
		kind := strings.ToLower(strings.TrimSpace(paramString(params, "kind", "recently_added")))
		switch kind {
		case "recently_added", "activity", "combined":
		default:
			return flows.ActionResult{}, fmt.Errorf("digest.generate: unsupported kind %q", kind)
		}
		groupEpisodes, _, err := paramBool(params, "groupEpisodes", true)
		if err != nil {
			return flows.ActionResult{}, fmt.Errorf("digest.generate: %w", err)
		}
		includeActivity, _, err := paramBool(params, "includeActivity", true)
		if err != nil {
			return flows.ActionResult{}, fmt.Errorf("digest.generate: %w", err)
		}
		saveShareable, _, err := paramBool(params, "saveShareable", false)
		if err != nil {
			return flows.ActionResult{}, fmt.Errorf("digest.generate: %w", err)
		}
		maxItems, _, err := paramInt(params, "maxItems", defaultDigestMaxItems)
		if err != nil || maxItems < 1 {
			return flows.ActionResult{}, errors.New("digest.generate: maxItems must be >= 1")
		}
		if maxItems > hardDigestMaxItems {
			maxItems = hardDigestMaxItems
		}
		libraryIDs := paramStringList(params, "libraryIds")
		mediaTypes := paramStringList(params, "mediaTypes")

		until := deps.now().UTC()
		since := until.Add(-windowDur)

		var (
			items    []DigestItem
			activity DigestActivitySummary
			hasAct   bool
		)
		if deps.Digests != nil {
			if kind == "recently_added" || kind == "combined" {
				items, err = deps.Digests.RecentlyAdded(ctx, DigestQuery{
					Since:      since,
					Until:      until,
					LibraryIDs: libraryIDs,
					MediaTypes: mediaTypes,
					MaxItems:   maxItems,
				})
				if err != nil {
					return flows.ActionResult{}, fmt.Errorf("digest.generate: recently added: %w", err)
				}
				if len(items) > maxItems {
					items = items[:maxItems]
				}
			}
			if kind == "activity" || (kind == "combined" && includeActivity) {
				activity, err = deps.Digests.ActivitySummary(ctx, since, until)
				if err != nil {
					return flows.ActionResult{}, fmt.Errorf("digest.generate: activity summary: %w", err)
				}
				hasAct = true
			}
		} else if !actx.DryRun {
			return flows.ActionResult{}, errors.New("digest.generate: digest source unavailable")
		}

		customTitle := strings.TrimSpace(paramString(params, "title", ""))
		model := buildDigestModel(customTitle, kind, windowRaw, since, until, items, groupEpisodes, hasAct, activity)
		textOut := renderDigestText(model)
		htmlOut, err := renderDigestHTML(model)
		if err != nil {
			return flows.ActionResult{}, fmt.Errorf("digest.generate: render html: %w", err)
		}

		shareURL := ""
		if saveShareable && !actx.DryRun && deps.Exports != nil {
			name := fmt.Sprintf("digest_%s.html", until.Format("20060102_150405"))
			art, err := deps.Exports.Save(ctx, name, []byte(htmlOut))
			if err != nil {
				return flows.ActionResult{}, fmt.Errorf("digest.generate: save shareable html: %w", err)
			}
			shareURL = art.DownloadURL
		}

		return flows.ActionResult{
			Output: map[string]any{
				"subject":      model.Subject,
				"text":         textOut,
				"html":         htmlOut,
				"shareUrl":     shareURL,
				"movieCount":   model.MovieCount,
				"showCount":    model.ShowCount,
				"episodeCount": model.EpisodeCount,
				"totalItems":   len(items),
				"totalPlays":   activity.TotalPlays,
				"windowStart":  since.Format(time.RFC3339),
				"windowEnd":    until.Format(time.RFC3339),
				"dryRun":       actx.DryRun,
			},
		}, nil
	}}
}

type digestSeasonGroup struct {
	Season  int
	Summary string
	Count   int
}

type digestShowGroup struct {
	ShowTitle string
	Year      int
	PosterURL string
	LinkURL   string
	Summary   string
	Genres    string
	Seasons   []digestSeasonGroup
	Episodes  int
}

type digestMovieCard struct {
	Title     string
	Year      int
	Rating    string
	Genres    string
	Summary   string
	PosterURL string
	LinkURL   string
}

type digestModel struct {
	Subject       string
	WindowLabel   string
	PeriodRange   string
	Movies        []digestMovieCard
	Shows         []digestShowGroup
	HasActivity   bool
	Activity      DigestActivitySummary
	MovieCount    int
	ShowCount     int
	EpisodeCount  int
	HasAnyContent bool
}

func buildDigestModel(
	customTitle, kind, windowLabel string,
	since, until time.Time,
	items []DigestItem,
	groupEpisodes bool,
	hasActivity bool,
	activity DigestActivitySummary,
) digestModel {
	m := digestModel{
		WindowLabel: windowLabel,
		PeriodRange: fmt.Sprintf("%s – %s", since.Format("Jan 2"), until.Format("Jan 2, 2006")),
		HasActivity: hasActivity,
		Activity:    activity,
	}

	type seasonBucket struct {
		episodes []DigestItem
	}
	type showBucket struct {
		title     string
		year      int
		posterURL string
		linkURL   string
		summary   string
		genres    []string
		seasons   map[int]*seasonBucket
		loose     []DigestItem
		latest    time.Time
	}

	showsByKey := map[string]*showBucket{}
	var ungroupedEpisodes []digestShowGroup

	for _, it := range items {
		switch strings.ToLower(strings.TrimSpace(it.MediaType)) {
		case "movie":
			m.MovieCount++
			rating := ""
			if it.Rating > 0 {
				rating = fmt.Sprintf("%.1f", it.Rating)
			}
			m.Movies = append(m.Movies, digestMovieCard{
				Title:     it.Title,
				Year:      it.Year,
				Rating:    rating,
				Genres:    strings.Join(it.Genres, " · "),
				Summary:   clipDigestText(it.Summary, 240),
				PosterURL: sanitizeDigestURL(it.PosterURL),
				LinkURL:   sanitizeDigestURL(it.LinkURL),
			})

		case "episode":
			m.EpisodeCount++
			showName := strings.TrimSpace(it.ShowTitle)
			if showName == "" {
				showName = it.Title
			}
			if !groupEpisodes {
				label := fmt.Sprintf("S%02dE%02d", it.Season, it.Episode)
				if it.Title != "" && it.Title != showName {
					label += " — " + it.Title
				}
				ungroupedEpisodes = append(ungroupedEpisodes, digestShowGroup{
					ShowTitle: showName,
					Year:      it.Year,
					PosterURL: sanitizeDigestURL(it.PosterURL),
					LinkURL:   sanitizeDigestURL(it.LinkURL),
					Summary:   clipDigestText(it.Summary, 200),
					Genres:    strings.Join(it.Genres, " · "),
					Seasons:   []digestSeasonGroup{{Season: it.Season, Summary: label, Count: 1}},
					Episodes:  1,
				})
				continue
			}
			sKey := strings.TrimSpace(it.ShowKey)
			if sKey == "" {
				sKey = strings.ToLower(showName)
			}
			sb, ok := showsByKey[sKey]
			if !ok {
				sb = &showBucket{
					title:     showName,
					year:      it.Year,
					posterURL: sanitizeDigestURL(it.PosterURL),
					linkURL:   sanitizeDigestURL(it.LinkURL),
					summary:   clipDigestText(it.Summary, 220),
					genres:    it.Genres,
					seasons:   map[int]*seasonBucket{},
					latest:    it.AddedAt,
				}
				showsByKey[sKey] = sb
			} else {
				if sb.posterURL == "" {
					sb.posterURL = sanitizeDigestURL(it.PosterURL)
				}
				if sb.linkURL == "" {
					sb.linkURL = sanitizeDigestURL(it.LinkURL)
				}
				if it.AddedAt.After(sb.latest) {
					sb.latest = it.AddedAt
				}
			}
			if it.Season > 0 {
				sec := sb.seasons[it.Season]
				if sec == nil {
					sec = &seasonBucket{}
					sb.seasons[it.Season] = sec
				}
				sec.episodes = append(sec.episodes, it)
			} else {
				sb.loose = append(sb.loose, it)
			}

		case "show":
			sKey := strings.TrimSpace(it.ShowKey)
			if sKey == "" {
				sKey = strings.TrimSpace(it.TitleKey)
			}
			if sKey == "" {
				sKey = strings.ToLower(it.Title)
			}
			if _, exists := showsByKey[sKey]; !exists {
				showsByKey[sKey] = &showBucket{
					title:     it.Title,
					year:      it.Year,
					posterURL: sanitizeDigestURL(it.PosterURL),
					linkURL:   sanitizeDigestURL(it.LinkURL),
					summary:   clipDigestText(it.Summary, 220),
					genres:    it.Genres,
					seasons:   map[int]*seasonBucket{},
					latest:    it.AddedAt,
				}
			}
		}
	}

	if groupEpisodes {
		buckets := make([]*showBucket, 0, len(showsByKey))
		for _, b := range showsByKey {
			buckets = append(buckets, b)
		}
		sort.Slice(buckets, func(i, j int) bool {
			if !buckets[i].latest.Equal(buckets[j].latest) {
				return buckets[i].latest.After(buckets[j].latest)
			}
			return buckets[i].title < buckets[j].title
		})
		for _, b := range buckets {
			sg := digestShowGroup{
				ShowTitle: b.title,
				Year:      b.year,
				PosterURL: b.posterURL,
				LinkURL:   b.linkURL,
				Summary:   b.summary,
				Genres:    strings.Join(b.genres, " · "),
			}
			seasonNums := make([]int, 0, len(b.seasons))
			for num := range b.seasons {
				seasonNums = append(seasonNums, num)
			}
			sort.Ints(seasonNums)
			for _, num := range seasonNums {
				eps := b.seasons[num].episodes
				sg.Episodes += len(eps)
				sg.Seasons = append(sg.Seasons, digestSeasonGroup{
					Season:  num,
					Summary: formatSeasonEpisodes(num, eps),
					Count:   len(eps),
				})
			}
			if len(b.loose) > 0 {
				sg.Episodes += len(b.loose)
				sg.Seasons = append(sg.Seasons, digestSeasonGroup{
					Season:  0,
					Summary: fmt.Sprintf("%d new episode(s)", len(b.loose)),
					Count:   len(b.loose),
				})
			}
			m.Shows = append(m.Shows, sg)
		}
	} else {
		m.Shows = ungroupedEpisodes
	}

	m.ShowCount = len(m.Shows)
	m.HasAnyContent = len(m.Movies) > 0 || len(m.Shows) > 0 || (hasActivity && activity.TotalPlays > 0)

	if customTitle != "" {
		m.Subject = customTitle
	} else {
		switch kind {
		case "activity":
			m.Subject = fmt.Sprintf("Ether Activity Digest (%s)", m.PeriodRange)
		case "combined":
			m.Subject = fmt.Sprintf("Ether Weekly Digest (%s)", m.PeriodRange)
		default:
			m.Subject = fmt.Sprintf("Recently Added to Ether (%s)", m.PeriodRange)
		}
	}
	return m
}

func formatSeasonEpisodes(season int, eps []DigestItem) string {
	if len(eps) == 1 {
		ep := eps[0]
		base := fmt.Sprintf("Season %d · E%02d", season, ep.Episode)
		if ep.Title != "" && ep.Title != ep.ShowTitle {
			return base + " — " + ep.Title
		}
		return base
	}
	nums := make([]int, 0, len(eps))
	seen := map[int]bool{}
	for _, ep := range eps {
		if ep.Episode > 0 && !seen[ep.Episode] {
			seen[ep.Episode] = true
			nums = append(nums, ep.Episode)
		}
	}
	sort.Ints(nums)
	if len(nums) == 0 {
		return fmt.Sprintf("Season %d (%d episodes)", season, len(eps))
	}
	ranges := formatEpisodeRanges(nums)
	return fmt.Sprintf("Season %d (%s · %d episodes)", season, ranges, len(nums))
}

func formatEpisodeRanges(nums []int) string {
	var parts []string
	start, prev := nums[0], nums[0]
	flush := func() {
		if start == prev {
			parts = append(parts, fmt.Sprintf("E%02d", start))
		} else {
			parts = append(parts, fmt.Sprintf("E%02d–E%02d", start, prev))
		}
	}
	for _, n := range nums[1:] {
		if n == prev+1 {
			prev = n
			continue
		}
		flush()
		start, prev = n, n
	}
	flush()
	return strings.Join(parts, ", ")
}

func renderDigestText(m digestModel) string {
	var b strings.Builder
	b.WriteString(m.Subject)
	b.WriteString("\n")
	if !m.HasAnyContent {
		b.WriteString("No new items or playback activity during this window.")
		return b.String()
	}
	if len(m.Movies) > 0 {
		fmt.Fprintf(&b, "\nMovies (%d):\n", len(m.Movies))
		for _, mv := range m.Movies {
			if mv.Year > 0 {
				fmt.Fprintf(&b, "- %s (%d)", mv.Title, mv.Year)
			} else {
				fmt.Fprintf(&b, "- %s", mv.Title)
			}
			if mv.Genres != "" {
				fmt.Fprintf(&b, " [%s]", mv.Genres)
			}
			b.WriteByte('\n')
		}
	}
	if len(m.Shows) > 0 {
		fmt.Fprintf(&b, "\nTV Shows (%d):\n", len(m.Shows))
		for _, sh := range m.Shows {
			if len(sh.Seasons) == 0 {
				fmt.Fprintf(&b, "- %s\n", sh.ShowTitle)
				continue
			}
			parts := make([]string, 0, len(sh.Seasons))
			for _, sec := range sh.Seasons {
				parts = append(parts, sec.Summary)
			}
			fmt.Fprintf(&b, "- %s: %s\n", sh.ShowTitle, strings.Join(parts, "; "))
		}
	}
	if m.HasActivity {
		fmt.Fprintf(&b, "\nActivity:\n- Plays: %d\n- Active viewers: %d\n- Watch time: %.1f hours\n",
			m.Activity.TotalPlays, m.Activity.UniqueViewers, m.Activity.HoursWatched)
		if len(m.Activity.TopTitles) > 0 {
			tops := make([]string, 0, len(m.Activity.TopTitles))
			for _, t := range m.Activity.TopTitles {
				tops = append(tops, fmt.Sprintf("%s (%d)", t.Title, t.Plays))
			}
			fmt.Fprintf(&b, "- Top titles: %s\n", strings.Join(tops, ", "))
		}
	}
	return strings.TrimSpace(b.String())
}

var digestHTMLTemplate = template.Must(template.New("digest").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>{{ .Subject }}</title>
</head>
<body style="margin:0;padding:24px;background:#0f1115;color:#eef1f5;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,sans-serif;line-height:1.5;">
  <div style="max-width:680px;margin:0 auto;background:#171a21;border:1px solid #262b36;border-radius:14px;overflow:hidden;">
    <div style="padding:24px 28px;border-bottom:1px solid #262b36;">
      <div style="font-size:12px;letter-spacing:.08em;text-transform:uppercase;color:#93a0b4;">Ether Digest · {{ .PeriodRange }}</div>
      <h1 style="margin:6px 0 0;font-size:22px;font-weight:600;color:#ffffff;">{{ .Subject }}</h1>
    </div>
    <div style="padding:24px 28px;">
      {{- if not .HasAnyContent }}
      <p style="margin:0;color:#93a0b4;">No new titles or playback activity recorded during this window.</p>
      {{- end }}
      {{- if .Movies }}
      <h2 style="margin:0 0 14px;font-size:16px;color:#ffffff;">Recently Added Movies ({{ len .Movies }})</h2>
      {{- range .Movies }}
      <table role="presentation" width="100%" cellspacing="0" cellpadding="0" style="margin-bottom:14px;background:#1e232d;border-radius:10px;overflow:hidden;">
        <tr>
          {{- if .PosterURL }}
          <td width="76" valign="top" style="padding:10px 0 10px 10px;">
            <img src="{{ .PosterURL }}" alt="" width="64" style="display:block;border-radius:6px;">
          </td>
          {{- end }}
          <td valign="top" style="padding:12px 14px;">
            <div style="font-size:15px;font-weight:600;color:#ffffff;">
              {{- if .LinkURL }}<a href="{{ .LinkURL }}" style="color:#ffffff;text-decoration:none;">{{ .Title }}</a>{{- else }}{{ .Title }}{{- end }}
              {{- if .Year }} <span style="color:#93a0b4;font-weight:400;">({{ .Year }})</span>{{- end }}
              {{- if .Rating }} <span style="color:#fbbf24;font-size:13px;">★ {{ .Rating }}</span>{{- end }}
            </div>
            {{- if .Genres }}<div style="font-size:12px;color:#93a0b4;margin-top:2px;">{{ .Genres }}</div>{{- end }}
            {{- if .Summary }}<p style="margin:6px 0 0;font-size:13px;color:#cbd5e1;">{{ .Summary }}</p>{{- end }}
          </td>
        </tr>
      </table>
      {{- end }}
      {{- end }}
      {{- if .Shows }}
      <h2 style="margin:22px 0 14px;font-size:16px;color:#ffffff;">Recently Added TV ({{ len .Shows }})</h2>
      {{- range .Shows }}
      <table role="presentation" width="100%" cellspacing="0" cellpadding="0" style="margin-bottom:14px;background:#1e232d;border-radius:10px;overflow:hidden;">
        <tr>
          {{- if .PosterURL }}
          <td width="76" valign="top" style="padding:10px 0 10px 10px;">
            <img src="{{ .PosterURL }}" alt="" width="64" style="display:block;border-radius:6px;">
          </td>
          {{- end }}
          <td valign="top" style="padding:12px 14px;">
            <div style="font-size:15px;font-weight:600;color:#ffffff;">
              {{- if .LinkURL }}<a href="{{ .LinkURL }}" style="color:#ffffff;text-decoration:none;">{{ .ShowTitle }}</a>{{- else }}{{ .ShowTitle }}{{- end }}
              {{- if .Year }} <span style="color:#93a0b4;font-weight:400;">({{ .Year }})</span>{{- end }}
            </div>
            {{- range .Seasons }}
            <div style="font-size:13px;color:#93c5fd;margin-top:4px;">{{ .Summary }}</div>
            {{- end }}
            {{- if .Summary }}<p style="margin:6px 0 0;font-size:13px;color:#cbd5e1;">{{ .Summary }}</p>{{- end }}
          </td>
        </tr>
      </table>
      {{- end }}
      {{- end }}
      {{- if .HasActivity }}
      <h2 style="margin:22px 0 12px;font-size:16px;color:#ffffff;">Playback Activity</h2>
      <div style="background:#1e232d;border-radius:10px;padding:14px 16px;font-size:13px;color:#cbd5e1;">
        <div><strong>{{ .Activity.TotalPlays }}</strong> plays · <strong>{{ .Activity.UniqueViewers }}</strong> viewers · <strong>{{ printf "%.1f" .Activity.HoursWatched }}</strong> hours watched</div>
        {{- if .Activity.TopTitles }}
        <ul style="margin:10px 0 0;padding-left:18px;">
          {{- range .Activity.TopTitles }}
          <li>{{ .Title }} ({{ .Plays }} plays)</li>
          {{- end }}
        </ul>
        {{- end }}
      </div>
      {{- end }}
    </div>
  </div>
</body>
</html>`))

func renderDigestHTML(m digestModel) (string, error) {
	var buf bytes.Buffer
	if err := digestHTMLTemplate.Execute(&buf, m); err != nil {
		return "", err
	}
	return buf.String(), nil
}

func sanitizeDigestURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.ContainsAny(raw, "\r\n\x00") {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return ""
	}
	return raw
}

func clipDigestText(s string, maxRunes int) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) <= maxRunes {
		return s
	}
	return strings.TrimSpace(string(r[:maxRunes-1])) + "…"
}
