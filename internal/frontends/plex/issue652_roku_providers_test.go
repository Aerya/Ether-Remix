package plex

import "testing"

// #652: Plex for Roku reads Preferences.Setting on each library Directory
// from /media/providers?includePreferences=1 unguarded and crashes opening
// a library whose entry has none. PMS repeats the section prefs there.
func TestProvidersIncludePreferences(t *testing.T) {
	f := newFixture()
	defer f.srv.Close()

	sectionDirs := func(path string) []map[string]any {
		t.Helper()
		_, lib := libraryProvider(t, f.get(t, path, jsonAccept...))
		features, _ := lib["Feature"].([]any)
		for _, feat := range features {
			fm := feat.(map[string]any)
			if fm["type"] != "content" {
				continue
			}
			var out []map[string]any
			for _, d := range fm["Directory"].([]any) {
				dm := d.(map[string]any)
				if dm["key"] == nil || dm["id"] == "playlists" {
					continue // Home and Playlists carry no prefs on PMS either
				}
				out = append(out, dm)
			}
			return out
		}
		t.Fatal("no content feature")
		return nil
	}

	dirs := sectionDirs("/media/providers?includePreferences=1")
	if len(dirs) == 0 {
		t.Fatal("no section directories")
	}
	for _, d := range dirs {
		prefs, ok := d["Preferences"].(map[string]any)
		if !ok {
			t.Fatalf("section %v has no Preferences with includePreferences=1", d["title"])
		}
		settings, _ := prefs["Setting"].([]any)
		if len(settings) == 0 {
			t.Fatalf("section %v Preferences.Setting empty", d["title"])
		}
		seen := map[string]bool{}
		for _, s := range settings {
			sm := s.(map[string]any)
			seen[sm["id"].(string)] = true
			for _, field := range []string{"label", "summary", "type", "default", "value", "hidden", "advanced", "group"} {
				if _, ok := sm[field]; !ok {
					t.Fatalf("section %v setting %v missing %q (PMS shape)", d["title"], sm["id"], field)
				}
			}
		}
		if !seen["hidden"] || !seen["collectionMode"] {
			t.Fatalf("section %v settings missing visibility/collectionMode: %v", d["title"], seen)
		}
	}

	for _, d := range sectionDirs("/media/providers") {
		if _, ok := d["Preferences"]; ok {
			t.Fatalf("section %v has Preferences without includePreferences=1", d["title"])
		}
	}
}
