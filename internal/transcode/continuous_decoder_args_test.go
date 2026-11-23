package transcode

import (
	"net/url"
	"slices"
	"testing"
)

func TestLiveDecoderInputArgsSetsHLSFormatBeforeLiveStartIndex(t *testing.T) {
	u, err := url.Parse("http://127.0.0.1:8080/relay/stream")
	if err != nil {
		t.Fatal(err)
	}
	args := liveDecoderInputArgs(DecoderRequest{InputFormat: "hls"}, u)
	idx := slices.Index(args, "-live_start_index")
	if idx < 2 || args[idx-2] != "-f" || args[idx-1] != "hls" {
		t.Fatalf("expected -f hls immediately before -live_start_index, got %v", args)
	}
	rw := slices.Index(args, "-rw_timeout")
	if rw < 0 || rw+1 >= len(args) || args[rw+1] != "5000000" {
		t.Fatalf("expected 5s rw_timeout for HLS, got %v", args)
	}

	tsArgs := liveDecoderInputArgs(DecoderRequest{InputFormat: "mpegts"}, u)
	tsRW := slices.Index(tsArgs, "-rw_timeout")
	if tsRW < 0 || tsRW+1 >= len(tsArgs) || tsArgs[tsRW+1] != "30000000" {
		t.Fatalf("expected 30s rw_timeout for tapped MPEG-TS loopback stream, got %v", tsArgs)
	}
}
