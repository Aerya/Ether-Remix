package console

import "testing"

func TestAlkoFieldsAndValidation(t *testing.T) {
	var found bool
	for _, f := range Features {
		if f.ID != "alko" {
			continue
		}
		found = true
		for _, fd := range f.Fields {
			switch fd.Key {
			case "PSE_ALKO":
				if fd.Default != "off" {
					t.Fatal("la source Alkodiques doit être désactivée par défaut")
				}
			case "PSE_ALKO_KEY":
				if fd.Kind != KindKey {
					t.Fatal("la clé doit être masquée")
				}
			case "PSE_ALKO_LANG_FR":
				if fd.Default != "on" {
					t.Fatal("le filtre FR / MULTi / VOSTFR est actif par défaut")
				}
			case "PSE_ALKO_URL":
				if fd.Default != "http://stream-fusion:8080" {
					t.Fatalf("adresse par défaut %q", fd.Default)
				}
			case "PSE_ALKO_INTERVAL":
				if fd.Default != "10m" {
					t.Fatalf("intervalle par défaut %q", fd.Default)
				}
			}
		}
	}
	if !found {
		t.Fatal("réglages Alkodiques absents")
	}
	for _, c := range []struct {
		key, v string
		ok     bool
	}{
		{"PSE_ALKO_URL", "http://stream-fusion:8080/", true},
		{"PSE_ALKO_URL", "https://sfr.exemple.net", true},
		{"PSE_ALKO_URL", "http://user:pass@stream-fusion:8080", false},
		{"PSE_ALKO_URL", "http://stream-fusion:8080/?k=x", false},
		{"PSE_ALKO_URL", "ftp://stream-fusion", false},
		{"PSE_ALKO_INTERVAL", "10m", true},
		{"PSE_ALKO_INTERVAL", "2m", true},
		{"PSE_ALKO_INTERVAL", "1m", false},
		{"PSE_ALKO_INTERVAL", "dix minutes", false},
	} {
		out, handled, err := validateAlko(c.key, c.v)
		if !handled || (err == nil) != c.ok {
			t.Errorf("%s=%q : %q %v", c.key, c.v, out, err)
		}
	}
	if out, _, _ := validateAlko("PSE_ALKO_URL", "http://stream-fusion:8080/"); out != "http://stream-fusion:8080" {
		t.Fatal(out)
	}
}
