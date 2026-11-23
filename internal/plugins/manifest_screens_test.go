package plugins

import "testing"

func TestValidateScreens(t *testing.T) {
	valid := func() Manifest {
		m := baseManifest()
		m.Ether.Contributes.Screens = []ScreenMeta{
			{
				ID:    "scores",
				Title: "Live scores",
				Icon:  "trophy",
				Feed:  "scores",
				Sections: []ScreenSection{
					{ID: "featured", Type: "hero"},
					{ID: "leagues", Type: "leagues"},
					{
						ID:    "live-now",
						Type:  "events",
						Title: "Live now",
						Query: &ScreenEventQuery{State: []string{"in"}, Window: "today"},
					},
					{
						ID:    "your-teams",
						Type:  "events",
						Title: "Your teams",
						Query: &ScreenEventQuery{Favorites: true, Window: "week"},
					},
					{ID: "schedule", Type: "schedule", Title: "Schedule"},
					{ID: "standings", Type: "table", Title: "Standings", Feed: "scores"},
					{ID: "picker", Type: "entities", Title: "Follow teams", EntityKind: "team"},
				},
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
			name: "missing sections",
			mut: func(m *Manifest) {
				m.Ether.Contributes.Screens[0].Sections = nil
			},
			field: "ether.contributes.screens[0].sections",
		},
		{
			name: "unknown section type",
			mut: func(m *Manifest) {
				m.Ether.Contributes.Screens[0].Sections[0].Type = "carousel"
			},
			field: "ether.contributes.screens[0].sections[0].type",
		},
		{
			name: "duplicate section id",
			mut: func(m *Manifest) {
				m.Ether.Contributes.Screens[0].Sections[1].ID = "featured"
			},
			field: "ether.contributes.screens[0].sections[1].id",
		},
		{
			name: "query on table rejected",
			mut: func(m *Manifest) {
				m.Ether.Contributes.Screens[0].Sections[5].Query = &ScreenEventQuery{Window: "today"}
			},
			field: "ether.contributes.screens[0].sections[5].query",
		},
		{
			name: "invalid event state",
			mut: func(m *Manifest) {
				m.Ether.Contributes.Screens[0].Sections[2].Query.State = []string{"live"}
			},
			field: "ether.contributes.screens[0].sections[2].query.state[0]",
		},
		{
			name: "duplicate event state",
			mut: func(m *Manifest) {
				m.Ether.Contributes.Screens[0].Sections[2].Query.State = []string{"in", "in"}
			},
			field: "ether.contributes.screens[0].sections[2].query.state[1]",
		},
		{
			name: "invalid window",
			mut: func(m *Manifest) {
				m.Ether.Contributes.Screens[0].Sections[2].Query.Window = "month"
			},
			field: "ether.contributes.screens[0].sections[2].query.window",
		},
		{
			name: "leagues on non-leagues section",
			mut: func(m *Manifest) {
				m.Ether.Contributes.Screens[0].Sections[0].Leagues = []ScreenLeague{{ID: "l1", Name: "League 1"}}
			},
			field: "ether.contributes.screens[0].sections[0].leagues",
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
