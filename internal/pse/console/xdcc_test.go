package console

import "testing"

func TestXDCCFieldsAndValidation(t *testing.T) {
	var found bool
	for _, f := range Features {
		if f.ID != "xdcc" {
			continue
		}
		found = true
		for _, fd := range f.Fields {
			switch fd.Key {
			case "PSE_XDCC":
				if fd.Default != "off" {
					t.Fatal("la source XDCC doit être désactivée par défaut")
				}
			case "PSE_XDCC_KEY":
				if fd.Kind != KindKey {
					t.Fatal("la clé doit être masquée")
				}
			}
		}
	}
	if !found {
		t.Fatal("réglages XDCC absents")
	}
	for _, c := range []struct {
		key, v string
		ok     bool
	}{
		{"PSE_XDCC_URL", "http://xdcc-grabscher:5556/", true},
		{"PSE_XDCC_URL", "http://user:pass@xdcc:5556", false},
		{"PSE_XDCC_URL", "http://xdcc:5556/?apikey=x", false},
		{"PSE_XDCC_URL", "ftp://xdcc", false},
		{"PSE_XDCC_PATH_MAP", "/config/.config/XG=/xdcc", true},
		{"PSE_XDCC_PATH_MAP", "relatif=/xdcc", false},
		{"PSE_XDCC_TEMP_DIR", "tmp", false},
		{"PSE_XDCC_WAIT", "10s", true},
		{"PSE_XDCC_WAIT", "30s", false},
		{"PSE_XDCC_WAIT", "1s", false},
	} {
		out, handled, err := validateXDCC(c.key, c.v)
		if !handled || (err == nil) != c.ok {
			t.Errorf("%s=%q : %q %v", c.key, c.v, out, err)
		}
	}
	if out, _, _ := validateXDCC("PSE_XDCC_URL", "http://xdcc-grabscher:5556/"); out != "http://xdcc-grabscher:5556" {
		t.Fatal(out)
	}
}
