package plugins

import (
	"container/list"
	"context"
	"io"
	"sync"

	container "github.com/braedonsaunders/ether/internal/archive"
)

const archiveDirectoryCacheBytes = 32 << 20

type archiveDirectoryKey struct {
	ref    string
	format container.Format
	// Grants are part of the key so a directory validated under a larger grant
	// cannot bypass stricter limits. Deadline, unit budget and per-call read
	// ceiling do not affect parsing.
	limits RuntimeLimits
}
type archiveDirectoryEntry struct {
	key       archiveDirectoryKey
	directory *dataArchiveDirectory
}

// Lazily allocated, host-owned LRU. No file handles or call contexts survive a
// call. In-flight parses are coalesced without holding the mutex during I/O.
type archiveDirectoryCache struct {
	mu      sync.Mutex
	entries map[archiveDirectoryKey]*list.Element
	pending map[archiveDirectoryKey]chan struct{}
	lru     list.List
	bytes   int64
}

func (h *host) archiveDirectoryCache() *archiveDirectoryCache { return &h.m.archiveDirectories }

func (c *archiveDirectoryCache) open(ctx context.Context, ref string, rc io.ReadSeeker, l RuntimeLimits) (*dataArchive, error) {
	ra := &archiveIOBudget{r: seekReaderAt{ctx, rc}, left: l.FetchBytes}
	var sig [8]byte
	if _, err := ra.ReadAt(sig[:], 0); err != nil {
		return nil, err
	}
	grants := l
	grants.Deadline, grants.UnitBudget, grants.ReadBytes = "", 0, 0
	key := archiveDirectoryKey{ref: ref, format: container.Detect(sig[:]), limits: grants}
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		c.mu.Lock()
		if e := c.entries[key]; e != nil {
			c.lru.MoveToFront(e)
			d := e.Value.(archiveDirectoryEntry).directory
			c.mu.Unlock()
			return &dataArchive{members: d.members, open: d.bind(ctx, ra, l)}, nil
		}
		if ready := c.pending[key]; ready != nil {
			c.mu.Unlock()
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-ready:
			}
			continue
		}
		if c.pending == nil {
			c.pending = make(map[archiveDirectoryKey]chan struct{})
		}
		ready := make(chan struct{})
		c.pending[key] = ready
		c.mu.Unlock()

		d, err := parseDataArchive(ctx, rc, ra, l)
		c.mu.Lock()
		if err == nil && d.bytes+int64(len(ref)) <= archiveDirectoryCacheBytes {
			if c.entries == nil {
				c.entries = make(map[archiveDirectoryKey]*list.Element)
			}
			d.bytes += int64(len(ref))
			for c.bytes+d.bytes > archiveDirectoryCacheBytes {
				e := c.lru.Back()
				old := e.Value.(archiveDirectoryEntry)
				c.bytes -= old.directory.bytes
				delete(c.entries, old.key)
				c.lru.Remove(e)
			}
			c.entries[key] = c.lru.PushFront(archiveDirectoryEntry{key: key, directory: d})
			c.bytes += d.bytes
		}
		delete(c.pending, key)
		close(ready)
		c.mu.Unlock()
		if err != nil {
			return nil, err
		}
		return &dataArchive{members: d.members, open: d.bind(ctx, ra, l)}, nil
	}
}
