package admin_test

import (
	"testing"

	"github.com/braedonsaunders/ether/internal/admin"
	"github.com/braedonsaunders/ether/internal/config"
)

func TestVerifiedFilesValidation(t *testing.T) {
	if got := config.Defaults(t.TempDir()).Resolver.VerifiedFiles; got != "first" {
		t.Fatalf("défaut verifiedFiles = %q, attendu first", got)
	}
	for _, tc := range []struct {
		value string
		valid bool
	}{{"", true}, {"off", true}, {"tie", true}, {"first", true}, {"always", false}, {"FIRST", true}, {" Tie ", true}, {"  ", true}, {"fi rst", false}} {
		cfg := config.Defaults(t.TempDir())
		cfg.Resolver.VerifiedFiles = tc.value
		rejected := false
		for _, e := range admin.ValidateConfig(cfg) {
			if e.Field == "resolver.verifiedFiles" {
				rejected = true
			}
		}
		if rejected == tc.valid {
			t.Errorf("verifiedFiles %q : rejeté=%v, attendu valide=%v", tc.value, rejected, tc.valid)
		}
	}
}
