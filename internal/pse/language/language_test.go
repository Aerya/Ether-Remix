package language

import "testing"

func TestChoicesUseEtherNames(t *testing.T) {
	seen := map[string]bool{}
	for _, c := range Choices() {
		if seen[c.Code] || c.FR == "" || c.EN == "" || Name(c.Code) == c.Code {
			t.Errorf("invalid or duplicate language choice: %+v", c)
		}
		seen[c.Code] = true
	}
	for _, code := range []string{"fr", "en", "ja", "ar", "hi", "es-419", "pt-br"} {
		if !seen[code] {
			t.Errorf("missing %s", code)
		}
	}
}
