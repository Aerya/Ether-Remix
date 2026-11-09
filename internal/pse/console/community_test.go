package console

import (
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/pse/music"
)

func TestCommunityManifestsAreValidatedAndMasked(t *testing.T) {
	for _, a := range music.CommunityAddons() {
		fd, ok := fieldByKey[a.Key]
		if !ok || !fd.Community || !fd.Secret {
			t.Fatalf("missing protected field %s", a.Key)
		}
		raw := "https://example.org/personal-token/manifest.json?key=private"
		shown := display(fd, raw)
		if strings.Contains(shown, "personal-token") || strings.Contains(shown, "private") {
			t.Fatal(shown)
		}
		restored := restore(fd, shown, raw)
		if restored != raw {
			t.Fatal("masked value lost")
		}
		if _, err := validate(fd, restored, nil); err != nil {
			t.Fatal(err)
		}
		for _, bad := range []string{a.SetupURL, "https://example.org/manifest.json https://other.org/manifest.json"} {
			if _, err := validate(fd, bad, nil); err == nil {
				t.Fatalf("accepted %s", bad)
			}
		}
		if v, err := validate(fd, "", nil); err != nil || v != "" {
			t.Fatal("cannot disable")
		}
	}
}
