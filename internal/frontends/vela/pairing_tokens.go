package vela

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"time"

	"github.com/braedonsaunders/ether/internal/frontend"
)

// tokenResult binds every complete token result, including refresh responses,
// to the validated access grant independently of a poll's idempotency key. Pairing can legitimately replay the
// same access token, but it must not mint another refresh family for that token.
// The result uses the existing encrypted durable replay store.
func (i *instance) tokenResult(w http.ResponseWriter, r *http.Request, access, refresh string, d frontend.Device, v *frontend.Viewer, expires time.Time) {
	key := hash("vela-grant-tokens\x00" + d.ID + "\x00" + access)
	digest := hash("vela-grant-tokens-v1")
	i.replayMu.Lock()
	if old := i.replays[key]; old != nil {
		i.replayMu.Unlock()
		select {
		case <-old.done:
		case <-r.Context().Done():
			return
		case <-i.ctx.Done():
			return
		}
		i.writeTokenResult(w, r, access, d, savedResponse{Status: old.status, Header: old.header, Body: old.body})
		return
	}
	if len(i.replays) >= 10000 {
		i.replayMu.Unlock()
		problem(w, 503, "capacity", "idempotency_capacity")
		return
	}
	entry := &replay{digest: digest, done: make(chan struct{})}
	i.replays[key] = entry
	i.replayMu.Unlock()
	out := httptest.NewRecorder()
	pctx, pcancel := persistContext(r)
	defer pcancel()
	// Keep the canonical result at least as long as this access grant can be
	// accepted. A repeated app-password sign-in may extend that grant; its
	// next read must not create a second family after a 24h cache TTL. A
	// pending claim keeps its short lease until the result is recorded.
	retainUntil := expires.Add(24 * time.Hour)
	saved, conflict, err := i.claim(pctx, key, digest)
	if err == nil && saved != nil {
		_, err = i.db.ExecContext(pctx, `UPDATE vela_replays SET expires=CASE WHEN expires<? THEN ? ELSE expires END WHERE key=? AND response IS NOT NULL`, retainUntil.UnixMilli(), retainUntil.UnixMilli(), key)
	}
	switch {
	case errors.Is(err, errPending):
		pendingProblem(out, err)
	case err != nil:
		fail(out, err, r)
	case conflict:
		problem(out, 409, "conflict", "idempotency_conflict")
	case saved != nil:
		for k, v := range saved.Header {
			out.Header()[k] = v
		}
		out.WriteHeader(saved.Status)
		_, _ = out.Write(saved.Body)
	default:
		if refresh == "" {
			refresh, err = i.host.Identity.IssueRefresh(r.Context(), access, d)
		}
		if err != nil {
			_ = i.host.Identity.Revoke(pctx, access)
			fail(out, err, r)
		} else {
			send(out, 200, object{"accessToken": access, "refreshToken": refresh, "expiresAt": expires.UTC().Format(time.RFC3339), "viewer": viewerWire(v)})
		}
		// Once issuance committed, preserve its encrypted result even when the
		// caller loses its connection. No token is issued again on a fresh poll
		// key. If recording fails the result is still delivered; the claim
		// lapses after replayLease.
		if err = i.save(pctx, key, savedResponse{Status: out.Code, Header: out.Header(), Body: out.Body.Bytes()}, retainUntil); err != nil {
			slog.Warn("vela token result not recorded", "status", out.Code, "err", err)
		}
	}
	entry.status = out.Code
	entry.header = out.Header().Clone()
	entry.body = append([]byte(nil), out.Body.Bytes()...)
	close(entry.done)
	i.replayMu.Lock()
	delete(i.replays, key)
	i.replayMu.Unlock()
	i.writeTokenResult(w, r, access, d, savedResponse{Status: entry.status, Header: entry.header, Body: entry.body})
}

func (i *instance) writeTokenResult(w http.ResponseWriter, r *http.Request, access string, d frontend.Device, response savedResponse) {
	if response.Status == 200 {
		if _, err := i.grantScope(r.Context(), access, d); err != nil {
			fail(w, err, r)
			return
		}
	}
	if response.Status == http.StatusOK {
		resolver, ok := i.host.Identity.(frontend.DeviceIdentity)
		if !ok {
			fail(w, frontend.ErrUnauthorized)
			return
		}
		v, _, err := resolver.ResolveDevice(r.Context(), access, d)
		if err != nil || v == nil {
			fail(w, frontend.ErrUnauthorized)
			return
		}
		viewer, err := i.viewerPolicyWire(r, v, d, i.viewerAvatar(r, v, access, d))
		if err != nil {
			fail(w, err)
			return
		}
		var result object
		if err := json.Unmarshal(response.Body, &result); err != nil {
			fail(w, err)
			return
		}
		result["viewer"] = viewer
		send(w, http.StatusOK, result)
		return
	}
	for k, v := range response.Header {
		w.Header()[k] = v
	}
	w.WriteHeader(response.Status)
	_, _ = w.Write(response.Body)
}
