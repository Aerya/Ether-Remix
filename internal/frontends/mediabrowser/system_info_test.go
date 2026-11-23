package mediabrowser

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/conform/openapi/spectest"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/frontendtest"
	"github.com/braedonsaunders/ether/internal/frontend/kit/ids"
)

type dashboardTestExperience struct {
	frontendtest.Experience
	mirrorFunc   func(context.Context, *frontend.Viewer) (int, int, error)
	favoriteFunc func(context.Context, *frontend.Viewer) (int, int, int, error)
}

func (d dashboardTestExperience) MirrorCounts(ctx context.Context, v *frontend.Viewer) (int, int, error) {
	if d.mirrorFunc != nil {
		return d.mirrorFunc(ctx, v)
	}
	return 0, 0, nil
}

func (d dashboardTestExperience) FavoriteCounts(ctx context.Context, v *frontend.Viewer) (int, int, int, error) {
	if d.favoriteFunc != nil {
		return d.favoriteFunc(ctx, v)
	}
	return 0, 0, 0, nil
}

type offlineTransport struct{}

func (offlineTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("offline")
}

// TestSystemInfoShape pins the extra reference fields and proves no real
// filesystem path is ever exposed on either protocol.
func TestSystemInfoShape(t *testing.T) {
	viewer := &frontend.Viewer{ID: 1, Username: "viewer"}
	identity := frontendtest.Identity{ResolveFunc: func(context.Context, string) (*frontend.Viewer, error) { return viewer, nil }}
	cases := []struct {
		name      string
		front     frontend.Frontend
		castArray bool
		spec      string
	}{
		{name: "jellyfin", front: Jellyfin{MachineID: "test"}, castArray: true, spec: "jellyfin"},
		{name: "emby", front: Emby{MachineID: "test"}, spec: "emby"},
	}
	forbidden := []string{"ProgramDataPath", "CachePath", "LogPath", "TranscodingTempPath", "InternalMetadataPath", "ItemsByNamePath", "WebPath", "EncoderLocation"}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			inst, err := c.front.Mount(context.Background(), frontend.Host{Identity: identity, Sessions: frontendtest.Sessions{}})
			if err != nil {
				t.Fatal(err)
			}
			w := httptest.NewRecorder()
			r := httptest.NewRequest("GET", "/System/Info", nil)
			r.Header.Set("X-Emby-Token", "grant")
			inst.Handler().ServeHTTP(w, r)
			if w.Code != 200 {
				t.Fatal(w.Code, w.Body.String())
			}
			var got map[string]any
			if json.Unmarshal(w.Body.Bytes(), &got) != nil {
				t.Fatal(w.Body.String())
			}
			for _, key := range forbidden {
				if _, ok := got[key]; ok {
					t.Fatalf("exposed %s", key)
				}
			}
			if got["HasPendingRestart"] != false || got["IsShuttingDown"] != false || got["SupportsLibraryMonitor"] != false {
				t.Fatal(got)
			}
			if _, ok := got["OperatingSystem"].(string); !ok || got["OperatingSystem"] == "" {
				t.Fatal("OperatingSystem", got["OperatingSystem"])
			}
			if c.castArray {
				if _, ok := got["CastReceiverApplications"].([]any); !ok {
					t.Fatal("CastReceiverApplications", got["CastReceiverApplications"])
				}
				if _, ok := got["SystemArchitecture"].(string); !ok {
					t.Fatal("SystemArchitecture", got["SystemArchitecture"])
				}
			} else if _, ok := got["LocalAddresses"].([]any); !ok {
				t.Fatal("LocalAddresses", got["LocalAddresses"])
			}
			if doc := spectest.Document(t, c.spec); doc != nil {
				if err := doc.Validate("GET", "/System/Info", w.Code, w.Header().Get("Content-Type"), w.Body.Bytes()); err != nil {
					t.Fatal(err)
				}
			}
			if strings.Contains(w.Body.String(), "/Users/") || strings.Contains(w.Body.String(), "/home/") {
				t.Fatal("leaked path", w.Body.String())
			}
		})
	}
}

func TestDashboardRoutes(t *testing.T) {
	owner := &frontend.Viewer{ID: 1, Username: "owner", IsOwner: true}
	member := &frontend.Viewer{ID: 2, Username: "member", AllowedLibraries: []frontend.LibraryID{1}}
	delegated := &frontend.Viewer{ID: 1, Username: "owner", IsOwner: true, Delegated: true}

	identity := frontendtest.Identity{ResolveFunc: func(_ context.Context, token string) (*frontend.Viewer, error) {
		switch token {
		case "owner":
			return owner, nil
		case "member":
			return member, nil
		case "delegated":
			return delegated, nil
		default:
			return nil, errors.New("invalid")
		}
	}}

	started := time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)
	sessions := frontendtest.Sessions{
		SessionsFunc: func(_ context.Context, access frontend.SessionAccess) ([]frontend.Session, error) {
			if !access.Owner {
				return nil, errors.New("expected owner session scope")
			}
			return []frontend.Session{
				{
					ID:         "s1",
					Protocol:   "jellyfin",
					ViewerID:   2,
					Item:       frontend.MovieKey(550),
					StartedAt:  started,
					PlayMethod: "DirectPlay",
					Device:     frontend.Device{ID: "d1", Client: "Jellyfin Web", DeviceName: "Mac"},
				},
			}, nil
		},
	}

	exp := dashboardTestExperience{
		Experience: frontendtest.Experience{
			ViewsFunc: func(_ context.Context, v *frontend.Viewer) ([]frontend.View, error) {
				if v.ID == member.ID {
					return []frontend.View{{ID: "1", Name: "Movies", Type: "movie", Virtual: true}}, nil
				}
				return []frontend.View{
					{ID: "1", Name: "Movies", Type: "movie", Virtual: true},
					{ID: "2", Name: "TV Shows", Type: "show", Virtual: true},
				}, nil
			},
			LibraryCountFunc: func(_ context.Context, v *frontend.Viewer, viewID string) (int, error) {
				if viewID == "1" {
					if v.ID == member.ID {
						return 12, nil
					}
					return 40, nil
				}
				if viewID == "2" {
					return 15, nil
				}
				return 0, nil
			},
		},
		mirrorFunc: func(_ context.Context, v *frontend.Viewer) (int, int, error) {
			return 200, 7, nil
		},
		favoriteFunc: func(_ context.Context, v *frontend.Viewer) (int, int, int, error) {
			return 3, 2, 5, nil
		},
	}

	dataDir := t.TempDir()
	clientsDir := filepath.Join(dataDir, "clients")
	cacheDir := filepath.Join(dataDir, "cache", "frontends", "jellyfin")
	host := frontend.Host{
		Identity:     identity,
		Sessions:     sessions,
		Experience:   exp,
		ClientsDir:   clientsDir,
		CacheDir:     cacheDir,
		MetadataHTTP: &http.Client{Transport: offlineTransport{}},
	}

	inst, err := (Jellyfin{MachineID: "test"}).Mount(context.Background(), host)
	if err != nil {
		t.Fatal(err)
	}
	defer inst.Close(context.Background())

	doc := spectest.Document(t, "jellyfin")

	call := func(path, token string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", path, nil)
		if token != "" {
			r.Header.Set("X-Emby-Token", token)
		}
		inst.Handler().ServeHTTP(w, r)
		return w
	}

	// Admin-only routes reject unauthenticated (401), regular members (403), and delegated grants (403).
	for _, path := range []string{
		"/web/ConfigurationPages",
		"/System/ActivityLog/Entries",
		"/System/Info/Storage",
		"/ScheduledTasks",
	} {
		if w := call(path, ""); w.Code != http.StatusUnauthorized {
			t.Fatalf("%s unauthenticated: got %d", path, w.Code)
		}
		if w := call(path, "member"); w.Code != http.StatusForbidden {
			t.Fatalf("%s member: got %d", path, w.Code)
		}
		if w := call(path, "delegated"); w.Code != http.StatusForbidden {
			t.Fatalf("%s delegated: got %d", path, w.Code)
		}
	}

	// /web/ConfigurationPages beats /web/{path...} even when webClient is mounted.
	w := call("/web/ConfigurationPages?EnableInMainMenu=true", "owner")
	if w.Code != http.StatusOK || strings.TrimSpace(w.Body.String()) != "[]" {
		t.Fatalf("ConfigurationPages: %d %s", w.Code, w.Body.String())
	}
	if doc != nil {
		if err := doc.Validate("GET", "/web/ConfigurationPages", w.Code, w.Header().Get("Content-Type"), w.Body.Bytes()); err != nil {
			t.Fatal(err)
		}
	}

	// /ScheduledTasks returns empty TaskInfo[].
	w = call("/ScheduledTasks?isHidden=false", "owner")
	if w.Code != http.StatusOK || strings.TrimSpace(w.Body.String()) != "[]" {
		t.Fatalf("ScheduledTasks: %d %s", w.Code, w.Body.String())
	}
	if doc != nil {
		if err := doc.Validate("GET", "/ScheduledTasks", w.Code, w.Header().Get("Content-Type"), w.Body.Bytes()); err != nil {
			t.Fatal(err)
		}
	}

	// /System/ActivityLog/Entries returns active session activity for hasUserId=true and empty for hasUserId=false.
	w = call("/System/ActivityLog/Entries?startIndex=0&limit=7&hasUserId=true", "owner")
	if w.Code != http.StatusOK {
		t.Fatalf("ActivityLog: %d %s", w.Code, w.Body.String())
	}
	if doc != nil {
		if err := doc.Validate("GET", "/System/ActivityLog/Entries", w.Code, w.Header().Get("Content-Type"), w.Body.Bytes()); err != nil {
			t.Fatal(err)
		}
	}
	var activity struct {
		Items            []map[string]any
		TotalRecordCount int
		StartIndex       int
	}
	if err := json.Unmarshal(w.Body.Bytes(), &activity); err != nil {
		t.Fatal(err)
	}
	if activity.TotalRecordCount != 1 || len(activity.Items) != 1 || activity.Items[0]["Name"] != "Jellyfin Web on Mac" {
		t.Fatalf("unexpected activity: %+v", activity)
	}
	if wAlerts := call("/System/ActivityLog/Entries?startIndex=0&limit=4&hasUserId=false", "owner"); wAlerts.Code != http.StatusOK || !strings.Contains(wAlerts.Body.String(), `"TotalRecordCount":0`) {
		t.Fatalf("ActivityLog alerts: %d %s", wAlerts.Code, wAlerts.Body.String())
	}

	// /System/Info/Storage returns real paths and volume space on Jellyfin, and 404 on Emby.
	w = call("/System/Info/Storage", "owner")
	if w.Code != http.StatusOK {
		t.Fatalf("Storage: %d %s", w.Code, w.Body.String())
	}
	if doc != nil {
		if err := doc.Validate("GET", "/System/Info/Storage", w.Code, w.Header().Get("Content-Type"), w.Body.Bytes()); err != nil {
			t.Fatal(err)
		}
	}
	var storage struct {
		ProgramDataFolder struct {
			Path      string
			FreeSpace int64
			UsedSpace int64
		}
		CacheFolder struct {
			Path      string
			FreeSpace int64
			UsedSpace int64
		}
		Libraries []struct {
			Id   string
			Name string
		}
	}
	if err := json.Unmarshal(w.Body.Bytes(), &storage); err != nil {
		t.Fatal(err)
	}
	if storage.ProgramDataFolder.Path != filepath.Clean(dataDir) || storage.ProgramDataFolder.FreeSpace <= 0 || storage.CacheFolder.Path != filepath.Join(dataDir, "cache") || len(storage.Libraries) != 2 {
		t.Fatalf("unexpected storage: %+v", storage)
	}

	// /Items/Counts beats /Items/{id}, scopes to the caller's libraries, and enforces ?userId= scope.
	ownerGUID, _ := ids.GUID(ids.User, owner.ID)
	w = call("/Items/Counts?userId="+ownerGUID, "owner")
	if w.Code != http.StatusOK {
		t.Fatalf("ItemCounts owner: %d %s", w.Code, w.Body.String())
	}
	if doc != nil {
		if err := doc.Validate("GET", "/Items/Counts", w.Code, w.Header().Get("Content-Type"), w.Body.Bytes()); err != nil {
			t.Fatal(err)
		}
	}
	var counts map[string]int
	if err := json.Unmarshal(w.Body.Bytes(), &counts); err != nil {
		t.Fatal(err)
	}
	if counts["MovieCount"] != 40 || counts["SeriesCount"] != 15 || counts["EpisodeCount"] != 200 || counts["BoxSetCount"] != 7 || counts["ItemCount"] != 262 {
		t.Fatalf("unexpected owner counts: %+v", counts)
	}

	w = call("/Items/Counts", "member")
	if err := json.Unmarshal(w.Body.Bytes(), &counts); err != nil || w.Code != http.StatusOK {
		t.Fatalf("ItemCounts member: %d %v", w.Code, err)
	}
	if counts["MovieCount"] != 12 || counts["SeriesCount"] != 0 || counts["EpisodeCount"] != 0 || counts["BoxSetCount"] != 7 || counts["ItemCount"] != 19 {
		t.Fatalf("unexpected member counts: %+v", counts)
	}

	if wScope := call("/Items/Counts?userId="+ownerGUID, "member"); wScope.Code != http.StatusForbidden {
		t.Fatalf("cross-viewer ItemCounts: got %d", wScope.Code)
	}

	w = call("/Items/Counts?isFavorite=true", "owner")
	if err := json.Unmarshal(w.Body.Bytes(), &counts); err != nil || w.Code != http.StatusOK {
		t.Fatalf("ItemCounts favorites: %d %v", w.Code, err)
	}
	if counts["MovieCount"] != 3 || counts["SeriesCount"] != 2 || counts["EpisodeCount"] != 5 || counts["ItemCount"] != 10 {
		t.Fatalf("unexpected favorite counts: %+v", counts)
	}

	// Emby validates shared routes and leaves Jellyfin-only /System/Info/Storage at 404.
	embyInst, err := (Emby{MachineID: "test"}).Mount(context.Background(), host)
	if err != nil {
		t.Fatal(err)
	}
	defer embyInst.Close(context.Background())
	embyDoc := spectest.Document(t, "emby")
	for _, path := range []string{"/web/ConfigurationPages", "/ScheduledTasks", "/System/ActivityLog/Entries", "/Items/Counts"} {
		ew := httptest.NewRecorder()
		er := httptest.NewRequest("GET", path, nil)
		er.Header.Set("X-Emby-Token", "owner")
		embyInst.Handler().ServeHTTP(ew, er)
		if ew.Code != http.StatusOK {
			t.Fatalf("emby %s: %d %s", path, ew.Code, ew.Body.String())
		}
		if embyDoc != nil {
			if err := embyDoc.Validate("GET", path, ew.Code, ew.Header().Get("Content-Type"), ew.Body.Bytes()); err != nil {
				t.Fatalf("emby %s validate: %v", path, err)
			}
		}
	}
	ew := httptest.NewRecorder()
	er := httptest.NewRequest("GET", "/System/Info/Storage", nil)
	er.Header.Set("X-Emby-Token", "owner")
	embyInst.Handler().ServeHTTP(ew, er)
	if ew.Code != http.StatusNotFound {
		t.Fatalf("emby /System/Info/Storage: got %d, want 404", ew.Code)
	}
}
