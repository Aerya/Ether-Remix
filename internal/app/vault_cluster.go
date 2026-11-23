package app

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/nodewire"
	"github.com/braedonsaunders/ether/internal/nodewire/outbound"
	"github.com/braedonsaunders/ether/internal/vault"
)

const vaultFrameLimit = 64 << 20

func (a *App) clusterVault(ctx context.Context, local vault.Store, shared bool) vault.Store {
	r, ok := local.(vault.ReplicaStore)
	if !ok || a.cluster == nil {
		return local
	}
	ctx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	done := make(chan struct{})
	go func() {
		defer close(done)
		a.cluster.RunAsLeader(ctx, "vault compaction", func(ctx context.Context) {
			tick := time.NewTicker(24 * time.Hour)
			defer tick.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-tick.C:
					if err := r.Compact(ctx); err != nil && ctx.Err() == nil {
						a.log.Warn("vault compaction failed", "err", err)
					}
				}
			}
		})
	}()
	a.closers = append(a.closers, func() { cancel(); <-done })
	node := a.clusterNode()
	if node == nil {
		return local
	}
	var peer vault.FramePeer
	if !shared && a.deployment != nil {
		peer = a.vaultExchange
		mux := http.NewServeMux()
		mux.HandleFunc("GET /private/vault/{key}", func(w http.ResponseWriter, req *http.Request) {
			frame, err := r.ReadFrame(req.Context(), vault.Key(req.PathValue("key")))
			if err != nil {
				http.NotFound(w, req)
				return
			}
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("Content-Type", "application/octet-stream")
			_, _ = w.Write(frame)
		})
		mux.HandleFunc("PUT /private/vault/{key}", func(w http.ResponseWriter, req *http.Request) {
			frame, err := io.ReadAll(http.MaxBytesReader(w, req.Body, vaultFrameLimit))
			if err != nil || r.InstallFrame(req.Context(), vault.Key(req.PathValue("key")), frame) != nil {
				w.WriteHeader(400)
				return
			}
			w.WriteHeader(204)
		})
		a.deployment.vaultHandler = mux
	}
	wrapped, closeCluster := vault.WithCluster(ctx, r, node, peer, a.log)
	a.closers = append(a.closers, closeCluster)
	return wrapped
}

func (a *App) vaultExchange(ctx context.Context, id string, key vault.Key, frame []byte) ([]byte, error) {
	nodes, err := a.cluster.Nodes(ctx)
	if err != nil {
		return nil, err
	}
	for _, n := range nodes {
		if n.ID != id || !n.Live || n.Draining {
			continue
		}
		identity := nodewire.Identity{Cluster: a.cfg.Read().Server.MachineIdentifier, NodeID: id, Boot: n.Incarnation}
		endpoint, err := a.privateEndpoint(ctx, identity)
		if err != nil {
			return nil, err
		}
		u, err := url.Parse(endpoint)
		if err != nil {
			return nil, err
		}
		guard := a.deployment.guard
		tc, err := guard.ClientTLS(identity, u.Hostname())
		if err != nil {
			return nil, err
		}
		tr, err := outbound.New(endpoint, tc, 3*time.Second, func(ctx context.Context, s tls.ConnectionState) error {
			return guard.RecheckServer(ctx, s, identity, u.Hostname())
		})
		if err != nil {
			return nil, err
		}
		defer tr.CloseIdleConnections()
		method := "GET"
		if frame != nil {
			method = "PUT"
		}
		req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(endpoint, "/")+"/private/vault/"+url.PathEscape(string(key)), bytes.NewReader(frame))
		if err != nil {
			return nil, err
		}
		res, err := tr.RoundTrip(req)
		if err != nil {
			return nil, err
		}
		defer res.Body.Close()
		if frame != nil && res.StatusCode == 204 {
			return nil, nil
		}
		if frame != nil || res.StatusCode != 200 {
			return nil, vault.ErrPayloadMissing
		}
		b, err := io.ReadAll(io.LimitReader(res.Body, vaultFrameLimit+1))
		if len(b) > vaultFrameLimit {
			return nil, errors.New("vault frame too large")
		}
		return b, err
	}
	return nil, vault.ErrPayloadMissing
}
