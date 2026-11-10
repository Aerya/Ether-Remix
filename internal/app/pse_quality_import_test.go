package app

import "testing"

func TestAIOConfigEndpoint(t *testing.T) {
	for _, tc := range []struct {
		url, want   string
		variant, ok bool
	}{
		{"https://aio.example/stremio/id/encrypted/manifest.json", "https://aio.example/api/v1/user?raw=true", false, true},
		{"https://aio.example/prefix/stremio/id/encrypted/v/remix/manifest.json", "https://aio.example/prefix/api/v1/user?raw=true", true, true},
		{"https://aio.example/stremio/id/encrypted/manifest.json?variant=remix", "https://aio.example/api/v1/user?raw=true", true, true},
		{"http://aio.example/stremio/id/key/manifest.json", "", false, false},
		{"https://user:pass@aio.example/stremio/id/key/manifest.json", "", false, false},
		{"https://aio.example/not-an-addon", "", false, false},
	} {
		got, id, v, ok := aioConfigEndpoint(tc.url)
		if got != tc.want || ok != tc.ok || v != tc.variant || (ok && id != "id") {
			t.Fatalf("unexpected endpoint: %s %s %v %v", got, id, v, ok)
		}
	}
}
