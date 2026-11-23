package vault

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/braedonsaunders/ether/internal/clusterbus"
	"github.com/braedonsaunders/ether/internal/core"
)

// FramePeer exchanges sealed frames with a specific live member.
type FramePeer func(context.Context, string, Key, []byte) ([]byte, error)

type clusteredStore struct {
	ReplicaStore
	node    clusterbus.Node
	peer    FramePeer
	iv      *clusterbus.Invalidator
	mu      sync.Mutex
	pending map[Key]struct{}
	wake    chan struct{}
}

func WithCluster(ctx context.Context, local ReplicaStore, node clusterbus.Node, peer FramePeer, log *slog.Logger) (Store, func()) {
	ctx, cancel := context.WithCancel(ctx)
	s := &clusteredStore{ReplicaStore: local, node: node, peer: peer, pending: make(map[Key]struct{}), wake: make(chan struct{}, 1)}
	s.iv = clusterbus.NewInvalidator(node, "vault", log, func(context.Context, []string, bool) { local.DropCache() })
	if peer == nil || node == nil {
		return s, func() { cancel(); s.iv.Close() }
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-ctx.Done():
				return
			case <-s.wake:
				s.mu.Lock()
				batch := s.pending
				s.pending = make(map[Key]struct{})
				s.mu.Unlock()
				for k := range batch {
					if ctx.Err() != nil {
						return
					}
					call, end := context.WithTimeout(ctx, 10*time.Second)
					s.push(call, k)
					end()
				}
			}
		}
	}()
	return s, func() { cancel(); <-done; s.iv.Close() }
}

func (s *clusteredStore) push(ctx context.Context, k Key) {
	live, err := s.node.LiveNodes(ctx)
	if err != nil {
		return
	}
	holders, err := s.Holders(ctx, k)
	if err != nil {
		return
	}
	for _, id := range holders {
		if id != s.node.NodeID() && live[id] {
			return
		}
	}
	frame, err := s.ReadFrame(ctx, k)
	if err != nil {
		return
	}
	for id, up := range live {
		if up && id != s.node.NodeID() {
			if _, err = s.peer(ctx, id, k, frame); err == nil {
				return
			}
		}
	}
}

func (s *clusteredStore) Put(ctx context.Context, r PutRequest) (Key, error) {
	k, err := s.ReplicaStore.Put(ctx, r)
	if err == nil && s.peer != nil && s.node != nil {
		s.mu.Lock()
		s.pending[k] = struct{}{}
		s.mu.Unlock()
		select {
		case s.wake <- struct{}{}:
		default:
		}
	}
	return k, err
}

func (s *clusteredStore) Get(ctx context.Context, k Key) (*Record, error) {
	r, err := s.ReplicaStore.Get(ctx, k)
	if !errors.Is(err, ErrPayloadMissing) || s.peer == nil || s.node == nil {
		return r, err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	holders, e := s.Holders(ctx, k)
	if e != nil {
		return nil, e
	}
	for _, id := range holders {
		if id == s.node.NodeID() {
			continue
		}
		frame, e := s.peer(ctx, id, k, nil)
		if e == nil && s.InstallFrame(ctx, k, frame) == nil {
			return s.ReplicaStore.Get(ctx, k)
		}
	}
	return nil, ErrPayloadMissing
}

func (s *clusteredStore) MarkHealth(ctx context.Context, k Key, h Health) error {
	err := s.ReplicaStore.MarkHealth(ctx, k, h)
	if err == nil {
		s.iv.Notify(string(k))
	}
	return err
}

func (s *clusteredStore) Purge(ctx context.Context, k Key) error {
	err := s.ReplicaStore.Purge(ctx, k)
	if err == nil {
		s.iv.Notify(string(k))
	}
	return err
}

func (s *clusteredStore) MigrateDir(ctx context.Context, dir string) error {
	if m, ok := s.ReplicaStore.(Migrator); ok {
		return m.MigrateDir(ctx, dir)
	}
	return ErrNotImplemented
}

func (s *clusteredStore) MarkChosen(ctx context.Context, k Key, fp string) error {
	if m, ok := s.ReplicaStore.(Chooser); ok {
		return m.MarkChosen(ctx, k, fp)
	}
	return ErrNotImplemented
}

func (s *clusteredStore) List(ctx context.Context, q string, kind Kind, health *Health, limit int, cursor string) ([]Entry, error) {
	type lister interface {
		List(context.Context, string, Kind, *Health, int, string) ([]Entry, error)
	}
	if l, ok := s.ReplicaStore.(lister); ok {
		return l.List(ctx, q, kind, health, limit, cursor)
	}
	return nil, ErrNotImplemented
}

func (s *clusteredStore) Tracks(ctx context.Context, key Key, season, episode int) (core.MediaInfo, bool, error) {
	if ts, ok := s.ReplicaStore.(TrackStore); ok {
		return ts.Tracks(ctx, key, season, episode)
	}
	return core.MediaInfo{}, false, nil
}

func (s *clusteredStore) SetTracks(ctx context.Context, key Key, season, episode int, info core.MediaInfo) error {
	if ts, ok := s.ReplicaStore.(TrackStore); ok {
		err := ts.SetTracks(ctx, key, season, episode, info)
		if err == nil {
			s.iv.Notify(string(key))
		}
		return err
	}
	return ErrNotImplemented
}

func (s *clusteredStore) KeysFor(ctx context.Context, releases []core.Release) (map[string]Key, error) {
	if ar, ok := s.ReplicaStore.(AliasResolver); ok {
		return ar.KeysFor(ctx, releases)
	}
	return nil, ErrNotImplemented
}
