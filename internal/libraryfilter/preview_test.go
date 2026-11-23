package libraryfilter

import (
	"encoding/json"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
)

func TestPreviewableRules(t *testing.T) {
	specs := []core.LibraryExclusion{
		{
			Name:      "Unfinished genre",
			Action:    "hide",
			MediaType: "movie",
			Match:     "all",
			Rules:     []core.CollectionRule{{Field: "genre", Op: "in", Value: json.RawMessage(`[]`)}},
		},
		{
			Name:      "Mixed complete and unfinished",
			Action:    "hide",
			MediaType: "show",
			Match:     "all",
			Rules: []core.CollectionRule{
				{Field: "language", Op: "is", Value: json.RawMessage(`"ja"`)},
				{Field: "browse:year", Op: "is", Value: json.RawMessage(`""`)},
			},
		},
		{
			Name:      "Empty group",
			Action:    "hide",
			MediaType: "mixed",
			Match:     "all",
			Rules:     []core.CollectionRule{{Field: "group", Op: "is", Value: json.RawMessage(`[]`)}},
		},
		{
			Name:      "Unknown op needs no value",
			Action:    "hide",
			MediaType: "movie",
			Match:     "all",
			Rules:     []core.CollectionRule{{Field: "country", Op: "unknown"}},
		},
	}

	kept, indices := PreviewableRules(specs)
	if len(kept) != 2 || len(indices) != 2 || indices[0] != 1 || indices[1] != 3 {
		t.Fatalf("PreviewableRules kept=%d indices=%v, want [1 3]", len(kept), indices)
	}
	if len(kept[0].Rules) != 1 || kept[0].Rules[0].Field != "language" {
		t.Fatalf("kept[0].Rules = %+v, want only language condition", kept[0].Rules)
	}
	if _, err := CompilePolicy(kept, "show"); err != nil {
		t.Fatalf("CompilePolicy(kept) error = %v", err)
	}

	// Malformed rules remain so the compiler rejects them as invalid filters.
	bad, _ := PreviewableRules([]core.LibraryExclusion{{
		Action:    "hide",
		MediaType: "movie",
		Match:     "all",
		Rules:     []core.CollectionRule{{Field: "bogus", Op: "is", Value: json.RawMessage(`""`)}},
	}})
	if _, err := CompilePolicy(bad, "show"); err == nil {
		t.Fatal("expected CompilePolicy to reject unknown field")
	}
}
