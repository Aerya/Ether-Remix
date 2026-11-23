package flows

import (
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/events"
)

// TestPickerConstantsMatchWeb verifies that Go's canonical Picker* constants,
// web/src/pages/flows/types.ts (FLOW_PICKER), and sdk/typescript/src/types.ts
// (FlowActionParamMeta.picker) all declare the exact same picker ID set so
// Go and TypeScript cannot drift.
func TestPickerConstantsMatchWeb(t *testing.T) {
	want := slices.Clone(AllPickers)
	slices.Sort(want)

	t.Run("web/src/pages/flows/types.ts", func(t *testing.T) {
		raw, err := os.ReadFile("../../web/src/pages/flows/types.ts")
		if err != nil {
			t.Fatalf("read web types: %v", err)
		}
		blockRe := regexp.MustCompile(`(?s)export const FLOW_PICKER\s*=\s*\{(.*?)\}\s*as const`)
		m := blockRe.FindSubmatch(raw)
		if m == nil {
			t.Fatal("FLOW_PICKER constant block not found in web/src/pages/flows/types.ts")
		}
		valRe := regexp.MustCompile(`:\s*"([^"]+)"`)
		matches := valRe.FindAllSubmatch(m[1], -1)
		var got []string
		for _, mm := range matches {
			got = append(got, string(mm[1]))
		}
		slices.Sort(got)
		if !slices.Equal(got, want) {
			t.Fatalf("FLOW_PICKER in web/src/pages/flows/types.ts = %v, want %v", got, want)
		}
	})

	t.Run("sdk/typescript/src/types.ts", func(t *testing.T) {
		raw, err := os.ReadFile("../../sdk/typescript/src/types.ts")
		if err != nil {
			t.Fatalf("read sdk types: %v", err)
		}
		lineRe := regexp.MustCompile(`picker\?:\s*([^;]+);`)
		m := lineRe.FindSubmatch(raw)
		if m == nil {
			t.Fatal("FlowActionParamMeta.picker not found in sdk/typescript/src/types.ts")
		}
		litRe := regexp.MustCompile(`"([^"]+)"`)
		matches := litRe.FindAllSubmatch(m[1], -1)
		var got []string
		for _, mm := range matches {
			got = append(got, string(mm[1]))
		}
		slices.Sort(got)
		if !slices.Equal(got, want) {
			t.Fatalf("FlowActionParamMeta.picker in sdk/typescript/src/types.ts = %v, want %v", got, want)
		}
	})
}

func TestActionSpecRejectsUnknownPicker(t *testing.T) {
	for _, bad := range []string{"notify.channels", "notifyChannels", "mqtt.brokers"} {
		spec := ActionSpec{
			Type:  "notify.send",
			Label: "Notify",
			Params: []ParamSchema{
				{Key: "channelIds", Type: events.FieldStringList, Label: "Channels", Picker: bad},
			},
		}
		err := spec.Validate()
		if err == nil || !strings.Contains(err.Error(), "unknown picker") {
			t.Fatalf("picker %q: expected unknown picker error, got %v", bad, err)
		}
	}
}
