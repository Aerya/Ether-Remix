package console

import "testing"

func TestPeerFieldsAndValidation(t *testing.T) {
	for _, kind := range []string{"amule", "slskd"} {
		f := peerFeature(kind)
		if f.Fields[0].Default != "off" {
			t.Fatal("enabled by default")
		}
		if f.Fields[2].Kind != KindSecret {
			t.Fatal("unmasked secret")
		}
	}
	for _, tc := range []struct {
		key, value string
		ok         bool
	}{{"PSE_AMULE_URL", "http://amule:4713", true}, {"PSE_SLSKD_URL", "http://user:password@slskd", false}, {"PSE_AMULE_URL", "https://amule/?key=secret", false}, {"PSE_AMULE_ROOT", "relative", false}, {"PSE_SLSKD_ROOT", "/downloads", true}} {
		_, handled, err := validatePeer(tc.key, tc.value)
		if !handled || (err == nil) != tc.ok {
			t.Errorf("%s: %v", tc.key, err)
		}
	}
}
