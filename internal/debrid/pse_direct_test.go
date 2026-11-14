package debrid

import (
	"context"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
)

// Ether-Remix : plusieurs sources locales (XDCC, FTP) préparent chacune
// leurs propres releases ; retirer l'une ne retire pas l'autre.
func TestPSEDirectPrepareSeveralSources(t *testing.T) {
	mk := func(prefix string) PSEDirectPrepare {
		return func(_ context.Context, rel core.Release) (core.Release, bool, error) {
			if !strings.HasPrefix(rel.GUID, prefix) {
				return rel, false, nil
			}
			rel.StreamURL = "http://127.0.0.1/" + prefix
			return rel, true, nil
		}
	}
	SetPSEDirectPrepare(mk("xdcc:"))
	SetPSEDirectPrepareFor("ftp", mk("ftp:"))
	defer SetPSEDirectPrepare(nil)
	defer SetPSEDirectPrepareFor("ftp", nil)
	for guid, want := range map[string]string{"xdcc:1": "http://127.0.0.1/xdcc:", "ftp:1": "http://127.0.0.1/ftp:", "autre": "http://cdn"} {
		out, err := psePrepareDirect(context.Background(), core.Release{GUID: guid, StreamURL: "http://cdn"})
		if err != nil || out.StreamURL != want {
			t.Fatalf("%s : %q %v", guid, out.StreamURL, err)
		}
	}
	SetPSEDirectPrepare(nil)
	if out, _ := psePrepareDirect(context.Background(), core.Release{GUID: "ftp:1"}); out.StreamURL != "http://127.0.0.1/ftp:" {
		t.Fatal("la source FTP a disparu avec XDCC")
	}
}
