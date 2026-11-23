package mediabrowser

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/kit/ids"
	"github.com/braedonsaunders/ether/internal/frontend/kit/route"
	"github.com/braedonsaunders/ether/internal/webclient"
)

type mirrorCounter interface {
	MirrorCounts(context.Context, *frontend.Viewer) (episodes, collections int, err error)
}

type favoriteCounter interface {
	FavoriteCounts(context.Context, *frontend.Viewer) (movies, series, episodes int, err error)
}

func (i *instance) dashboardRoutes() error {
	routes := []struct {
		method, path string
		h            http.HandlerFunc
	}{
		{"GET", "/web/ConfigurationPages", i.configurationPages},
		{"GET", "/System/ActivityLog/Entries", i.activityLogEntries},
		{"GET", "/ScheduledTasks", i.scheduledTasks},
		{"GET", "/Items/Counts", i.itemCounts},
	}
	if i.protocol == "jellyfin" {
		routes = append(routes, struct {
			method, path string
			h            http.HandlerFunc
		}{"GET", "/System/Info/Storage", i.systemStorage})
	}
	for _, rt := range routes {
		if err := i.router.Handle(rt.method, rt.path, rt.h); err != nil {
			return err
		}
	}
	return nil
}

// requireAdmin authenticates the caller and enforces Jellyfin/Emby elevation.
// When jellyfinOnly is true, non-admin authenticated callers remain allowed on
// Emby where the corresponding endpoint uses standard user authentication.
func (i *instance) requireAdmin(w http.ResponseWriter, r *http.Request, jellyfinOnly bool) (*frontend.Viewer, bool) {
	viewer, _, err := i.viewer(r)
	if errors.Is(err, errViewerScope) {
		w.WriteHeader(http.StatusForbidden)
		return nil, false
	}
	if err != nil || viewer == nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return nil, false
	}
	if (!jellyfinOnly || i.protocol == "jellyfin") && (viewer.Delegated || !viewer.IsOwner) {
		w.WriteHeader(http.StatusForbidden)
		return nil, false
	}
	return viewer, true
}

// configurationPages answers GET /web/ConfigurationPages with an empty list:
// Ether hosts no Jellyfin/Emby server plugins with custom HTML pages.
func (i *instance) configurationPages(w http.ResponseWriter, r *http.Request) {
	if _, ok := i.requireAdmin(w, r, true); !ok {
		return
	}
	jsonReply(w, []any{})
}

// scheduledTasks answers GET /ScheduledTasks with an empty TaskInfo list,
// matching the ScheduledTasksInfo WebSocket frame.
func (i *instance) scheduledTasks(w http.ResponseWriter, r *http.Request) {
	if _, ok := i.requireAdmin(w, r, false); !ok {
		return
	}
	jsonReply(w, []any{})
}

// activityLogEntries projects active broker sessions into ActivityLogEntry
// records for the admin dashboard, or returns an empty page when none match.
func (i *instance) activityLogEntries(w http.ResponseWriter, r *http.Request) {
	viewer, ok := i.requireAdmin(w, r, false)
	if !ok {
		return
	}
	startIndex := max(queryInt(r, "StartIndex", 0), 0)
	limitValues := route.Query(r.URL.Query(), "Limit")
	hasLimit := len(limitValues) > 0
	limit := max(queryInt(r, "Limit", 0), 0)

	var hasUserID *bool
	if vals := route.Query(r.URL.Query(), "HasUserId"); len(vals) == 1 && strings.TrimSpace(vals[0]) != "" {
		b, err := strconv.ParseBool(strings.TrimSpace(vals[0]))
		if err != nil {
			http.Error(w, "Bad Request", http.StatusBadRequest)
			return
		}
		hasUserID = &b
	}
	var minDate time.Time
	if vals := route.Query(r.URL.Query(), "MinDate"); len(vals) == 1 && strings.TrimSpace(vals[0]) != "" {
		parsed, ok := parseActivityTime(vals[0])
		if !ok {
			http.Error(w, "Bad Request", http.StatusBadRequest)
			return
		}
		minDate = parsed
	}
	if (hasUserID != nil && !*hasUserID) || i.host.Sessions == nil {
		jsonReply(w, i.queryResult([]any{}, 0, startIndex))
		return
	}
	sessions, err := i.host.Sessions.Sessions(r.Context(), frontend.SessionAccess{
		Viewer:      viewer,
		Device:      device(r),
		Protocol:    i.protocol,
		Owner:       true,
		VisibleOnly: true,
	})
	if err != nil {
		http.Error(w, "Unavailable", http.StatusServiceUnavailable)
		return
	}
	filtered := make([]frontend.Session, 0, len(sessions))
	for _, s := range sessions {
		if hasUserID != nil && *hasUserID && s.ViewerID <= 0 {
			continue
		}
		if !minDate.IsZero() && (s.StartedAt.IsZero() || s.StartedAt.Before(minDate)) {
			continue
		}
		filtered = append(filtered, s)
	}
	sort.SliceStable(filtered, func(a, b int) bool {
		if !filtered[a].StartedAt.Equal(filtered[b].StartedAt) {
			return filtered[a].StartedAt.After(filtered[b].StartedAt)
		}
		return filtered[a].ID < filtered[b].ID
	})
	severity := "Information"
	if i.protocol == "emby" {
		severity = "Info"
	}
	items := make([]any, 0, len(filtered))
	for idx, s := range filtered {
		when := s.StartedAt
		if when.IsZero() {
			if i.host.Now != nil {
				when = i.host.Now()
			} else {
				when = time.Now()
			}
		}
		entry := map[string]any{
			"Id":       int64(idx + 1),
			"Name":     activitySessionName(s),
			"Type":     "SessionStarted",
			"Date":     wireTime(when),
			"Severity": severity,
		}
		if s.ViewerID > 0 {
			if userID, err := ids.GUID(ids.User, s.ViewerID); err == nil {
				entry["UserId"] = userID
			}
		}
		if s.Item != 0 {
			entry["ItemId"] = ids.RatingKeyGUID(s.Item)
		}
		if s.PlayMethod != "" {
			entry["ShortOverview"] = s.PlayMethod
		}
		items = append(items, entry)
	}
	start := min(startIndex, len(items))
	end := len(items)
	if hasLimit {
		end = min(start+limit, len(items))
	}
	jsonReply(w, i.queryResult(items[start:end], len(items), startIndex))
}

func activitySessionName(s frontend.Session) string {
	client := strings.TrimSpace(s.Device.Client)
	dev := strings.TrimSpace(s.Device.DeviceName)
	switch {
	case client != "" && dev != "":
		return client + " on " + dev
	case client != "":
		return client
	case dev != "":
		return dev
	default:
		return "Active session"
	}
}

func parseActivityTime(raw string) (time.Time, bool) {
	raw = strings.TrimSpace(raw)
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05.9999999", "2006-01-02T15:04:05", "2006-01-02"} {
		if tm, err := time.Parse(layout, raw); err == nil {
			return tm.UTC(), true
		}
	}
	return time.Time{}, false
}

// systemStorage answers Jellyfin's admin-only GET /System/Info/Storage with
// Ether's data/cache directories and underlying volume space.
func (i *instance) systemStorage(w http.ResponseWriter, r *http.Request) {
	viewer, ok := i.requireAdmin(w, r, false)
	if !ok {
		return
	}
	dataDir := i.dataDir()
	cacheDir := ""
	if i.host.HostConfig != nil {
		cacheDir = strings.TrimSpace(i.host.HostConfig().EffectiveCache().Dir)
	}
	if cacheDir == "" && dataDir != "" {
		cacheDir = filepath.Join(dataDir, "cache")
	} else if cacheDir == "" {
		cacheDir = i.host.CacheDir
	}
	webDir := ""
	if i.host.ClientsDir != "" {
		webDir = filepath.Join(i.host.ClientsDir, "jellyfin-web", webclient.Version)
	}
	imageCacheDir := cacheDir
	logDir := ""
	if dataDir != "" {
		imageCacheDir = filepath.Join(dataDir, "cache", "title-posters")
		logDir = filepath.Join(dataDir, "logs")
	}

	seen := map[string]map[string]any{}
	folder := func(p string) map[string]any {
		if p != "" {
			p = filepath.Clean(p)
		}
		if dto, exists := seen[p]; exists {
			return dto
		}
		freeSpace, usedSpace := int64(-1), int64(-1)
		if p != "" {
			if total, free, err := frontend.DiskUsage(p); err == nil && total >= 0 && free >= 0 {
				freeSpace = free
				usedSpace = max(total-free, 0)
			}
		}
		dto := map[string]any{
			"Path":      p,
			"FreeSpace": freeSpace,
			"UsedSpace": usedSpace,
		}
		seen[p] = dto
		return dto
	}

	libraries := []any{}
	if i.host.Experience != nil {
		views, err := i.host.Experience.Views(r.Context(), viewer)
		if err != nil {
			http.Error(w, "Unavailable", http.StatusServiceUnavailable)
			return
		}
		for _, view := range views {
			viewNum, err := ids.ParseDecimal(view.ID)
			if err != nil {
				continue
			}
			viewID, err := ids.GUID(ids.View, viewNum)
			if err != nil {
				continue
			}
			libraries = append(libraries, map[string]any{
				"Id":      viewID,
				"Name":    view.Name,
				"Folders": []any{},
			})
		}
	}

	jsonReply(w, map[string]any{
		"ProgramDataFolder":      folder(dataDir),
		"WebFolder":              folder(webDir),
		"ImageCacheFolder":       folder(imageCacheDir),
		"CacheFolder":            folder(cacheDir),
		"LogFolder":              folder(logDir),
		"InternalMetadataFolder": folder(dataDir),
		"TranscodingTempFolder":  folder(cacheDir),
		"Libraries":              libraries,
	})
}

func (i *instance) dataDir() string {
	if i.host.ClientsDir != "" {
		return filepath.Dir(filepath.Clean(i.host.ClientsDir))
	}
	if i.host.CacheDir != "" {
		clean := filepath.Clean(i.host.CacheDir)
		frontendsDir := filepath.Dir(clean)
		cacheDir := filepath.Dir(frontendsDir)
		if filepath.Base(frontendsDir) == "frontends" && filepath.Base(cacheDir) == "cache" {
			return filepath.Dir(cacheDir)
		}
	}
	return ""
}

// itemCounts answers GET /Items/Counts from the caller's accessible libraries
// using indexed library and mirror tallies without scanning any library.
func (i *instance) itemCounts(w http.ResponseWriter, r *http.Request) {
	viewer, _, err := i.viewer(r)
	if errors.Is(err, errViewerScope) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	if err != nil || viewer == nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	var isFavorite bool
	if vals := route.Query(r.URL.Query(), "IsFavorite"); len(vals) == 1 && strings.TrimSpace(vals[0]) != "" {
		b, err := strconv.ParseBool(strings.TrimSpace(vals[0]))
		if err != nil {
			http.Error(w, "Bad Request", http.StatusBadRequest)
			return
		}
		isFavorite = b
	}

	var movieCount, seriesCount, episodeCount, artistCount, programCount, trailerCount, songCount, albumCount, musicVideoCount, boxSetCount, bookCount, otherCount int
	if i.host.Experience != nil {
		if isFavorite {
			if fav, ok := i.host.Experience.(favoriteCounter); ok {
				movieCount, seriesCount, episodeCount, err = fav.FavoriteCounts(r.Context(), viewer)
				if err != nil {
					http.Error(w, "Unavailable", http.StatusServiceUnavailable)
					return
				}
			}
		} else {
			views, err := i.host.Experience.Views(r.Context(), viewer)
			if err != nil {
				http.Error(w, "Unavailable", http.StatusServiceUnavailable)
				return
			}
			var builtinMovies, builtinShows bool
			var builtinMovieCount, customMovieCount, pluginMovieCount int
			var builtinSeriesCount, customSeriesCount, pluginSeriesCount int
			for _, view := range views {
				if view.ID == "1" {
					builtinMovies = true
				}
				if view.ID == "2" {
					builtinShows = true
				}
				n, ok := i.cachedLibraryCount(r.Context(), viewer, view.ID)
				if !ok || n <= 0 {
					continue
				}
				switch mediaBrowserCollectionType(view) {
				case "movies":
					switch {
					case view.ID == "1":
						builtinMovieCount = n
					case view.PluginID != "":
						pluginMovieCount += n
					default:
						customMovieCount += n
					}
				case "tvshows":
					switch {
					case view.ID == "2":
						builtinSeriesCount = n
					case view.PluginID != "":
						pluginSeriesCount += n
					default:
						customSeriesCount += n
					}
				case "music":
					if view.Type == "album" || view.Type == "show" {
						albumCount += n
					} else {
						songCount += n
					}
				case "books", "audiobooks":
					bookCount += n
				case "musicvideos":
					musicVideoCount += n
				case "boxsets":
					boxSetCount += n
				default:
					otherCount += n
				}
			}
			if builtinMovies {
				movieCount = builtinMovieCount + pluginMovieCount
			} else {
				movieCount = customMovieCount + pluginMovieCount
			}
			if builtinShows {
				seriesCount = builtinSeriesCount + pluginSeriesCount
			} else {
				seriesCount = customSeriesCount + pluginSeriesCount
			}
			if builtinMovies || builtinShows {
				if mc, ok := i.host.Experience.(mirrorCounter); ok {
					if episodes, collections, err := mc.MirrorCounts(r.Context(), viewer); err == nil {
						if builtinShows {
							episodeCount = episodes
						}
						if boxSetCount == 0 {
							boxSetCount = collections
						}
					}
				}
			}
		}
	}
	itemCount := movieCount + seriesCount + episodeCount + artistCount + programCount + trailerCount + songCount + albumCount + musicVideoCount + boxSetCount + bookCount + otherCount
	result := map[string]any{
		"MovieCount":      movieCount,
		"SeriesCount":     seriesCount,
		"EpisodeCount":    episodeCount,
		"ArtistCount":     artistCount,
		"ProgramCount":    programCount,
		"TrailerCount":    trailerCount,
		"SongCount":       songCount,
		"AlbumCount":      albumCount,
		"MusicVideoCount": musicVideoCount,
		"BoxSetCount":     boxSetCount,
		"BookCount":       bookCount,
		"ItemCount":       itemCount,
	}
	if i.protocol == "emby" {
		result["GameCount"] = 0
		result["GameSystemCount"] = 0
	}
	jsonReply(w, result)
}

// systemInfoDTO is the authenticated GET /System/Info body. Each protocol
// receives exactly the fields its own schema declares, with safe Ether
// values. Filesystem paths the reference exposes (ProgramDataPath, CachePath,
// LogPath, TranscodingTempPath, InternalMetadataPath, ItemsByNamePath,
// WebPath, EncoderLocation) are deliberately never sent.
func (i *instance) systemInfoDTO(r *http.Request) map[string]any {
	address := requestAddress(i.info, r, i.host.Origin, i.host.SurfaceURL)
	port := requestPort(r, i.host.Origin)
	if port == 0 {
		port = i.listenerPort()
	}
	osName, osDisplay := operatingSystem()
	m := map[string]any{
		"Id": i.info.ID, "ServerName": i.info.ServerName, "Version": i.info.Version,
		"OperatingSystem": osName, "OperatingSystemDisplayName": osDisplay,
		"HasPendingRestart": false, "IsShuttingDown": false,
		"SupportsLibraryMonitor": false, "WebSocketPortNumber": i.listenerPort(),
		"CompletedInstallations": []any{}, "CanSelfRestart": false,
		"CanLaunchWebBrowser": false, "HasUpdateAvailable": false,
	}
	if i.protocol == "jellyfin" {
		m["ProductName"] = i.info.ProductName
		m["StartupWizardCompleted"] = true
		m["LocalAddress"] = address
		m["SystemArchitecture"] = systemArchitecture()
		m["CastReceiverApplications"] = []any{}
		return m
	}
	m["LocalAddress"] = address
	m["LocalAddresses"] = []string{address}
	m["RemoteAddresses"] = []string{address}
	m["WanAddress"] = address
	m["HttpServerPortNumber"] = port
	m["SupportsHttps"] = false
	m["HttpsPortNumber"] = 8920
	m["IsInMaintenanceMode"] = false
	m["SystemUpdateLevel"] = "Release"
	m["CanSelfUpdate"] = false
	m["WakeOnLanInfo"] = []any{}
	m["SupportsWakeServer"] = false
	m["SupportsAutoRunAtStartup"] = false
	m["SupportsLocalPortConfiguration"] = false
	m["HasImageEnhancers"] = false
	m["HardwareAccelerationRequiresPremiere"] = false
	return m
}

// operatingSystem maps the runtime GOOS to the platform names the reference
// servers report: Jellyfin uses adjectival names, Emby its own set.
func operatingSystem() (name, display string) {
	switch runtime.GOOS {
	case "darwin":
		return "OSX", "Unix"
	case "windows":
		return "Windows", "Windows"
	case "linux":
		return "Linux", "Unix"
	case "freebsd":
		return "FreeBSD", "Unix"
	default:
		return runtime.GOOS, runtime.GOOS
	}
}

// systemArchitecture is Jellyfin's SystemArchitecture enum value.
func systemArchitecture() string {
	switch runtime.GOARCH {
	case "amd64":
		return "X64"
	case "arm64":
		return "Arm64"
	case "386":
		return "X86"
	case "arm":
		return "Arm"
	default:
		return runtime.GOARCH
	}
}
