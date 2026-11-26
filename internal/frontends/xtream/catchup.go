package xtream

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"

	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/kit/caps"
	"github.com/braedonsaunders/ether/internal/frontend/kit/xtreamplayback"
)

func (i *instance) catchUp(w http.ResponseWriter, r *http.Request, l login) {
	host, ok := i.host.LiveTV.(frontend.LiveCatchUp)
	if !ok || i.host.Capabilities == nil {
		http.Error(w, "Unavailable", 503)
		return
	}
	id, at, duration, status := xtreamplayback.ArchiveRange(r)
	if status != 0 {
		http.Error(w, http.StatusText(status), status)
		return
	}
	channel, ok := i.findChannel(r, l, id)
	if !ok {
		http.NotFound(w, r)
		return
	}
	ip, ok := i.host.ClientIP(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	sum := sha256.Sum256([]byte(l.device.ID + "/" + ip.String() + "/timeshift/" + id))
	device := l.device
	device.ID += "-" + hex.EncodeToString(sum[:12])
	ctx := frontend.WithClientIP(r.Context(), ip)
	profile, err := i.host.Capabilities.Build(ctx, frontend.CapabilityRequest{Viewer: l.viewer, Device: caps.DeviceInfo{
		Protocol: "xtream", ClientID: device.ID, Product: device.Product, MaxHeight: 4320, HDR10: true,
		VideoCodecs: []string{"h264", "hevc"}, AudioCodecs: []string{"aac", "ac3", "eac3", "mp3"}, Containers: []string{"mpegts", "ts"},
	}, UserAgent: r.UserAgent()})
	if err != nil {
		http.Error(w, "Unavailable", 503)
		return
	}
	client := profile.Client
	client.HLSContainers = []string{"mpegts"}
	req := frontend.CatchUpRequest{ChannelID: channel.ID, Start: at, Duration: duration}
	admission := frontend.PlanRequest{Viewer: l.viewer, Device: device, Protocol: "xtream", Channel: "archive/" + channel.ID, Caps: client}
	if primary := !xtreamplayback.ArchiveHLS(r) || !r.URL.Query().Has("session"); primary && !i.admit(w, r.WithContext(ctx), l, admission) {
		return
	}
	xtreamplayback.ServeArchive(w, r.WithContext(ctx), i.host, host, l.credential, req, admission)
}
