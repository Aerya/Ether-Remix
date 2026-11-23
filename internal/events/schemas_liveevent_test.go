package events

import "testing"

func TestLiveEventSchemasRegisteredAndLegacyAliasesResolve(t *testing.T) {
	r := NewRegistry()
	RegisterBuiltins(r)

	cases := []struct {
		canonical string
		legacy    string
	}{
		{EventLiveEventStarted, "liveevent.started"},
		{EventLiveEventClose, "liveevent.close"},
		{EventLiveEventFinal, "liveevent.final"},
	}

	for _, tc := range cases {
		if err := ValidateType(tc.canonical); err != nil {
			t.Fatalf("ValidateType(%q) = %v, want nil", tc.canonical, err)
		}
		schema, ok := r.Get(tc.canonical)
		if !ok {
			t.Fatalf("Get(%q) not found", tc.canonical)
		}
		if schema.Domain != "liveevent" {
			t.Fatalf("schema %q domain = %q, want liveevent", tc.canonical, schema.Domain)
		}
		legacySchema, ok := r.GetByLegacy(tc.legacy)
		if !ok {
			t.Fatalf("GetByLegacy(%q) not found", tc.legacy)
		}
		if legacySchema.Type != tc.canonical {
			t.Fatalf("GetByLegacy(%q).Type = %q, want %q", tc.legacy, legacySchema.Type, tc.canonical)
		}
	}
}
