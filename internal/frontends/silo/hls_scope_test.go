package silo

import "testing"

// The app only credentials master.m3u8 and segment/{name} under a session;
// every URI a served playlist names must resolve inside that scope and map
// back to the session path it stands for.
func TestSiloHLSScope(t *testing.T) {
	master := "#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=1\nbase/index.m3u8\n#EXT-X-STREAM-INF:BANDWIDTH=2\n720p4m/index.m3u8\n"
	if got := string(siloHLSRewrite([]byte(master), "master.m3u8", "master.m3u8")); got != "#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=1\nsegment/base~index.m3u8\n#EXT-X-STREAM-INF:BANDWIDTH=2\nsegment/720p4m~index.m3u8\n" {
		t.Fatalf("master:\n%s", got)
	}
	media := "#EXTM3U\n#EXT-X-MAP:URI=\"g-1-init.mp4\"\n#EXTINF:4,\ng-1-00000.m4s\n#EXT-X-ENDLIST\n"
	requested := "segment/base~index.m3u8"
	resource := siloHLSResource(requested)
	if resource != "base/index.m3u8" {
		t.Fatalf("resource %q", resource)
	}
	if got := string(siloHLSRewrite([]byte(media), requested, resource)); got != "#EXTM3U\n#EXT-X-MAP:URI=\"base~g-1-init.mp4\"\n#EXTINF:4,\nbase~g-1-00000.m4s\n#EXT-X-ENDLIST\n" {
		t.Fatalf("media:\n%s", got)
	}
	if got := siloHLSResource("segment/base~g-1-00000.m4s"); got != "base/g-1-00000.m4s" {
		t.Fatalf("segment %q", got)
	}
	// Already in scope: left as the server wrote it.
	inScope := "#EXTM3U\n#EXTINF:4,\nsegment/00001.ts\n"
	if got := string(siloHLSRewrite([]byte(inScope), "master.m3u8", "master.m3u8")); got != inScope {
		t.Fatalf("in scope:\n%s", got)
	}
	if got := siloHLSResource("segment/00001.ts"); got != "segment/00001.ts" {
		t.Fatalf("plain segment %q", got)
	}
}
