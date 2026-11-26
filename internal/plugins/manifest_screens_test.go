package plugins

import "testing"

func TestValidateScreens(t *testing.T) {
	valid := func() Manifest {
		m := baseManifest()
		m.Ether.Capabilities = append(m.Ether.Capabilities, CapScreens)
		m.Ether.Contributes.Screens = []ScreenMeta{
			{
				ID:    "scores",
				Title: "Live scores",
				Icon:  "trophy",
				Pages: map[string]DocumentPaths{"index": {"screens/scores.json"}, "game": {"screens/game.json"}},
			},
		}
		return m
	}
	if err := valid().Validate(); err != nil {
		t.Fatalf("valid screens manifest rejected: %v", err)
	}

	cases := []struct {
		name  string
		mut   func(*Manifest)
		field string
	}{
		{
			name: "bad screen id",
			mut: func(m *Manifest) {
				m.Ether.Contributes.Screens[0].ID = "Bad_ID"
			},
			field: "ether.contributes.screens[0].id",
		},
		{
			name: "duplicate screen id",
			mut: func(m *Manifest) {
				cp := m.Ether.Contributes.Screens[0]
				m.Ether.Contributes.Screens = append(m.Ether.Contributes.Screens, cp)
			},
			field: "ether.contributes.screens[1].id",
		},
		{
			name: "missing pages",
			mut: func(m *Manifest) {
				m.Ether.Contributes.Screens[0].Pages = nil
			},
			field: "ether.contributes.screens[0].pages",
		},
		{
			name: "missing index page",
			mut: func(m *Manifest) {
				delete(m.Ether.Contributes.Screens[0].Pages, "index")
			},
			field: "ether.contributes.screens[0].pages",
		},
		{
			name: "0.3.19 sections refused",
			mut: func(m *Manifest) {
				m.Ether.Contributes.Screens[0].Sections = []byte(`[{"type":"hero"}]`)
			},
			field: "ether.contributes.screens[0].sections",
		},
		{
			name: "pages need the screens capability",
			mut: func(m *Manifest) {
				m.Ether.Capabilities = nil
			},
			field: "ether.contributes.screens[0].pages",
		},
		{
			name: "unknown frontend",
			mut: func(m *Manifest) {
				m.Ether.Contributes.Screens[0].Frontends = []string{"unknown"}
			},
			field: "ether.contributes.screens[0].frontends[0]",
		},
		{
			name: "screen title uses $t without strings",
			mut: func(m *Manifest) {
				m.Ether.Contributes.Screens[0].Title = "$t:screen_title"
			},
			field: "ether.strings",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := valid()
			tc.mut(&m)
			err := m.Validate()
			if err == nil {
				t.Fatalf("expected error on %s, got nil", tc.field)
			}
			if !hasField(err, tc.field) {
				t.Fatalf("expected field %s, got %v", tc.field, err)
			}
		})
	}

	t.Run("permission sheet mentions screen", func(t *testing.T) {
		lines, _, _ := PermissionSheet(valid())
		found := false
		for _, line := range lines {
			if line == "Adds a screen: Live scores" {
				found = true
			}
		}
		if !found {
			t.Fatalf("PermissionSheet = %v, want screen line", lines)
		}
	})
}
