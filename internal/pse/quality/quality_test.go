package quality

import (
	"context"
	"encoding/json"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/releaseparse"
	"github.com/braedonsaunders/ether/internal/store"
	"strings"
	"testing"
)

func rule(field, value, action string) core.ReleaseRule {
	return core.ReleaseRule{ID: "r", Name: "rule", Action: action, Match: "any", Conditions: []core.ReleaseCondition{{Field: field, Operator: "equals", Value: value}}}
}
func service(t *testing.T) *Service {
	t.Helper()
	s, e := New(context.Background(), store.OpenTest(t))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(s.Close)
	return s
}
func release(name string) core.Release {
	return core.Release{Name: name, Info: releaseparse.Parse(name)}
}
func TestScopePrecedencePersistenceAndIsolation(t *testing.T) {
	s := service(t)
	other := service(t)
	ctx := context.Background()
	c := Config{Profiles: []Profile{{ID: "global", Name: "HEVC", Scope: "global", Rules: []core.ReleaseRule{rule("video", "hevc", "require")}}, {ID: "lib", Name: "AVC", Scope: "library", Target: "1", Replace: true, Rules: []core.ReleaseRule{rule("video", "h264", "require")}}, {ID: "col", Name: "AV1", Scope: "collection", Target: "c", Replace: true, Rules: []core.ReleaseRule{rule("video", "av1", "require")}}}}
	if e := s.Save(ctx, c); e != nil {
		t.Fatal(e)
	}
	before := Signature(s.db)
	s.SetMembers(map[string][]core.RatingKey{"library:1": {10, 11}, "collection:c": {11}})
	if before == Signature(s.db) {
		t.Fatal("membership must invalidate decisions")
	}
	avc := release("Film.1080p.x264.WEB-DL")
	if s.Allow(9, core.TypeMovie, avc) || !s.Allow(10, core.TypeMovie, avc) || s.Allow(11, core.TypeMovie, avc) {
		t.Fatal("scope precedence")
	}
	if !other.Allow(9, core.TypeMovie, avc) {
		t.Fatal("cross-instance leak")
	}
	c.Profiles[0].Rules[0].Conditions[0].Value = "h264"
	if s.Allow(9, core.TypeMovie, avc) {
		t.Fatal("caller mutated snapshot")
	}
	fresh, e := New(ctx, s.db)
	if e != nil {
		t.Fatal(e)
	}
	defer fresh.Close()
	if fresh.Allow(9, core.TypeMovie, avc) {
		t.Fatal("saved filter not restored")
	}
}
func TestExclusionUnknownAndInvalidRegex(t *testing.T) {
	s := service(t)
	p := Profile{ID: "p", Name: "No DV", Scope: "global", Rules: []core.ReleaseRule{rule("hdr", "dv", "avoid")}}
	if e := s.Save(context.Background(), Config{Profiles: []Profile{p}}); e != nil {
		t.Fatal(e)
	}
	if !s.Allow(1, core.TypeMovie, release("Film.1080p.x264")) {
		t.Fatal("unknown HDR is not proof of DV")
	}
	if s.Allow(1, core.TypeMovie, release("Film.2160p.DV.x265")) {
		t.Fatal("DV accepted")
	}
	p.Rules[0].Conditions[0] = core.ReleaseCondition{Field: "name", Operator: "regex", Value: "(?<=bad)"}
	if e := s.Save(context.Background(), Config{Profiles: []Profile{p}}); e == nil {
		t.Fatal("unsupported regex accepted")
	}
}
func TestAIOImportPreviewDoesNotCopySecrets(t *testing.T) {
	r, e := ImportAIO(json.RawMessage(`{"apiKey":"private-secret","requiredEncodes":["HEVC","AV1"],"requiredQualities":["WEB-DL","Unknown"],"includedLanguages":["French"],"excludedRegexPatterns":["/CAM/i"]}`))
	if e != nil {
		t.Fatal(e)
	}
	if len(r.Rules) != 2 {
		t.Fatalf("partial whitelist must be skipped: %+v", r)
	}
	b, _ := json.Marshal(r)
	if strings.Contains(string(b), "private-secret") {
		t.Fatal("secret copied")
	}
	ps, e := compile(Config{Profiles: []Profile{{ID: "p", Name: "import", Scope: "global", Rules: r.Rules}}})
	if e != nil {
		t.Fatal(e)
	}
	if !evaluate(ps[0].policy, release("Film.CAM.x265")).Rejected {
		t.Fatal("JS regex flags lost")
	}
	if evaluate(ps[0].policy, release("Film.WEB-DL.x265")).Rejected {
		t.Fatal("valid HEVC rejected")
	}
}
