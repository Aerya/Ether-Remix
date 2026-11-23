package plex

import (
	"context"
	"encoding/xml"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend/kit/clientnet"
)

func TestSourceIPDecidesLocalityOverDecisionLocationParam(t *testing.T) {
	restore := clientnet.OverrideHostPrefixes([]netip.Prefix{
		netip.MustParsePrefix("2001:db8:abcd:1::/64"),
	})
	t.Cleanup(restore)

	cfg := config.Defaults(t.TempDir())
	cfg.Server.TrustedProxyCIDRs = []string{"172.16.50.5"}
	cfg.Transcode.RemoteMaxBitrateKbps = 8000

	uploadCapCalls := 0
	f := newFixture(func(d *testServerDeps) {
		setDepsConfig(d, func() config.Config { return cfg })
	})
	defer f.srv.Close()
	f.srv.host.RemoteUploadCap = func(context.Context, string, bool, bool, int, int) (int, bool) {
		uploadCapCalls++
		return 6000, true
	}

	sendDecision := func(remoteAddr, xff, clientID, sessionID, loc string) {
		t.Helper()
		q := "/video/:/transcode/universal/decision?path=/library/metadata/" + fightClub.Key.String() +
			"&directPlay=1&directStream=1&protocol=hls&session=" + sessionID + "&location=" + loc
		req := httptest.NewRequest(http.MethodGet, q, nil)
		req.RemoteAddr = remoteAddr
		if xff != "" {
			req.Header.Set("X-Forwarded-For", xff)
		}
		req.Header.Set("X-Plex-Token", ownerToken)
		req.Header.Set("X-Plex-Client-Identifier", clientID)
		req.Header.Set("X-Plex-Session-Identifier", sessionID)
		req.Header.Set("X-Plex-Product", "Plex for Apple TV")
		req.Header.Set("X-Plex-Platform", "tvOS")
		rec := httptest.NewRecorder()
		f.srv.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("decision %s (%s): %d %s", clientID, loc, rec.Code, rec.Body.String())
		}
	}

	sendTimeline := func(remoteAddr, xff, clientID, sessionID string) {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/:/timeline?ratingKey="+fightClub.Key.String()+"&state=playing&time=1000&duration=60000", nil)
		req.RemoteAddr = remoteAddr
		if xff != "" {
			req.Header.Set("X-Forwarded-For", xff)
		}
		req.Header.Set("X-Plex-Token", ownerToken)
		req.Header.Set("X-Plex-Client-Identifier", clientID)
		req.Header.Set("X-Plex-Session-Identifier", sessionID)
		req.Header.Set("X-Plex-Product", "Plex for Apple TV")
		req.Header.Set("X-Plex-Platform", "tvOS")
		rec := httptest.NewRecorder()
		f.srv.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("timeline %s: %d %s", clientID, rec.Code, rec.Body.String())
		}
	}

	// 1. Private LAN client behind trusted reverse proxy claiming location=wan stays local:
	// no remote bitrate cap, no upload budget cap, no Remote flag.
	sendDecision("172.16.50.5:41000", "192.168.3.10", "lan-wan-claim", "sess-lan-wan", "wan")
	if f.trans.lastCaps.Remote || f.trans.lastCaps.MaxBitrateKbps != 0 || f.trans.lastCaps.ClientIP != "192.168.3.10" {
		t.Fatalf("LAN behind proxy + location=wan was treated remote: %+v", f.trans.lastCaps)
	}
	if uploadCapCalls != 0 {
		t.Fatalf("RemoteUploadCap called %d times for LAN client claiming location=wan", uploadCapCalls)
	}
	sendTimeline("172.16.50.5:41000", "192.168.3.10", "lan-wan-claim", "sess-lan-wan")

	// 2. Global IPv6 client inside host /64 behind trusted reverse proxy claiming location=cellular stays local.
	sendDecision("172.16.50.5:41001", "2001:db8:abcd:1::42", "lan-v6-claim", "sess-lan-v6", "cellular")
	if f.trans.lastCaps.Remote || f.trans.lastCaps.MaxBitrateKbps != 0 || f.trans.lastCaps.ClientIP != "2001:db8:abcd:1::42" {
		t.Fatalf("host /64 IPv6 behind proxy + location=cellular was treated remote: %+v", f.trans.lastCaps)
	}
	if uploadCapCalls != 0 {
		t.Fatalf("RemoteUploadCap called %d times for host /64 IPv6 client", uploadCapCalls)
	}
	sendTimeline("172.16.50.5:41001", "2001:db8:abcd:1::42", "lan-v6-claim", "sess-lan-v6")

	// 3. Public IPv4 client behind trusted reverse proxy claiming location=lan stays remote:
	// remote bitrate cap and upload budget cap BOTH apply.
	sendDecision("172.16.50.5:41002", "203.0.113.10", "wan-lan-claim", "sess-wan-lan", "lan")
	if !f.trans.lastCaps.Remote || f.trans.lastCaps.MaxBitrateKbps != 6000 || f.trans.lastCaps.ClientIP != "203.0.113.10" {
		t.Fatalf("public IP + location=lan bypassed remote cap: %+v", f.trans.lastCaps)
	}
	if uploadCapCalls == 0 {
		t.Fatalf("RemoteUploadCap was not consulted for public IP claiming location=lan")
	}
	sendTimeline("172.16.50.5:41002", "203.0.113.10", "wan-lan-claim", "sess-wan-lan")

	// 4. Verify /status/sessions reflects authenticated source locality for all three sessions.
	poll := httptest.NewRequest(http.MethodGet, "/status/sessions", nil)
	poll.RemoteAddr = "192.168.3.2:52000"
	poll.Header.Set("X-Plex-Token", ownerToken)
	poll.Header.Set("X-Plex-Client-Identifier", "owner-poll")
	rec := httptest.NewRecorder()
	f.srv.ServeHTTP(rec, poll)
	if rec.Code != http.StatusOK {
		t.Fatalf("/status/sessions: %d %s", rec.Code, rec.Body.String())
	}
	var mc struct {
		Videos []struct {
			Player struct {
				MachineID string `xml:"machineIdentifier,attr"`
				Address   string `xml:"address,attr"`
				Local     string `xml:"local,attr"`
			} `xml:"Player"`
			Session struct {
				Location string `xml:"location,attr"`
			} `xml:"Session"`
		} `xml:"Video"`
	}
	if err := xml.Unmarshal(rec.Body.Bytes(), &mc); err != nil {
		t.Fatalf("unmarshal /status/sessions: %v", err)
	}
	got := map[string]struct{ addr, local, loc string }{}
	for _, v := range mc.Videos {
		got[v.Player.MachineID] = struct{ addr, local, loc string }{v.Player.Address, v.Player.Local, v.Session.Location}
	}
	for id, want := range map[string]struct{ addr, local, loc string }{
		"lan-wan-claim": {"192.168.3.10", "1", "lan"},
		"lan-v6-claim":  {"2001:db8:abcd:1::42", "1", "lan"},
		"wan-lan-claim": {"203.0.113.10", "0", "wan"},
	} {
		if g, ok := got[id]; !ok || g != want {
			t.Errorf("/status/sessions[%s] = %+v (ok=%v), want %+v", id, g, ok, want)
		}
	}

	// Ensure unused import check isn't triggered if core is only used via fixture.
	_ = core.DecisionDirectPlay
}
