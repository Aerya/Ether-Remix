package admin_test

import (
	"testing"

	"github.com/braedonsaunders/ether/internal/admin"
	"github.com/braedonsaunders/ether/internal/config"
)

func TestPlaybackRelinkValidation(t *testing.T) {
	for v, invalid := range map[string]bool{
		"": false, "refresh": false, "replay": false, "Replay": false, " REFRESH ": false,
		"bogus": true, "replays": true,
	} {
		c := config.Defaults(t.TempDir())
		c.Debrid.PlaybackRelink = v
		found := false
		for _, e := range admin.ValidateConfig(c) {
			if e.Field == "debrid.playbackRelink" {
				found = true
			}
		}
		if found != invalid {
			t.Fatalf("%q: invalid=%v", v, found)
		}
	}
}
