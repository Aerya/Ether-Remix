package transcode

import (
	"container/list"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// LiveDVREvict applies the continuous encoder's rolling retention rule.
// The live suffix stays available independently of the disk history.
func LiveDVREvict(count, liveCount int, oldestEnd, newestEnd, window time.Duration) bool {
	return count > 1 && (window == 0 && count > liveCount || window > 0 && newestEnd-oldestEnd >= window)
}

// LiveDVRCache shares a byte budget across active live outputs on this node.
type LiveDVRCache struct {
	budget func() int64
	dir    string
	serial atomic.Uint64

	mu    sync.Mutex // disk writers only
	bytes int64
	files list.List // oldest published segment first
}

func NewLiveDVRCache(dir string, budget func() int64) *LiveDVRCache {
	if dir == "" {
		dir = filepath.Join(os.TempDir(), "ether-transcode")
	}
	return &LiveDVRCache{dir: dir, budget: budget}
}

// LiveDVRStore is one output's disposable history. Its bounded queue keeps
// the live publisher independent of filesystem latency and write failures.
type LiveDVRStore struct {
	cache     *LiveDVRCache
	dir       string // writer-owned
	queue     chan liveDVRWrite
	wake      chan struct{}
	stop      chan struct{}
	done      chan struct{}
	floor     atomic.Int64
	files     list.List // guarded by cache.mu
	reapMu    sync.Mutex
	forgotten []*LiveDVRFile
}

func (c *LiveDVRCache) Open() *LiveDVRStore {
	s := &LiveDVRStore{cache: c, queue: make(chan liveDVRWrite, storeQueue), wake: make(chan struct{}, 1), stop: make(chan struct{}), done: make(chan struct{})}
	go s.run()
	return s
}

type liveDVRWrite struct {
	file *LiveDVRFile
	data []byte
}

// LiveDVRFile is the disk copy of one immutable media object.
type LiveDVRFile struct {
	store      *LiveDVRStore
	seq        int64
	order      uint64
	path       string
	size       int64
	entry      *list.Element
	ownedEntry *list.Element
	ready      atomic.Bool
	release    atomic.Bool
}

// Save queues a segment without blocking. A missing disk copy advances the
// rewind floor, while callers continue serving their in-memory live suffix.
func (s *LiveDVRStore) Save(seq int64, data []byte) *LiveDVRFile {
	f := &LiveDVRFile{store: s, seq: seq, size: int64(len(data)), order: s.cache.serial.Add(1)}
	select {
	case s.queue <- liveDVRWrite{file: f, data: data}:
	default:
		s.lose(seq)
	}
	return f
}

// DiscardThrough advances the rewind floor when the live tier has to drop
// bytes before their queued write is ready.
func (s *LiveDVRStore) DiscardThrough(seq int64) { s.lose(seq) }

func (s *LiveDVRStore) Floor() int64 { return s.floor.Load() }

func (s *LiveDVRStore) lose(seq int64) {
	for floor := s.floor.Load(); seq >= floor; floor = s.floor.Load() {
		if s.floor.CompareAndSwap(floor, seq+1) {
			s.signal()
			return
		}
	}
}

func (f *LiveDVRFile) Ready() bool { return f != nil && f.ready.Load() }

func (f *LiveDVRFile) Open() (*os.File, error) {
	if !f.Ready() {
		return nil, os.ErrNotExist
	}
	return os.Open(f.path)
}

func (f *LiveDVRFile) Forget() {
	if f != nil && f.release.CompareAndSwap(false, true) {
		s := f.store
		s.reapMu.Lock()
		s.forgotten = append(s.forgotten, f)
		s.reapMu.Unlock()
		s.signal()
	}
}

func (s *LiveDVRStore) signal() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// Close is called after the output has stopped publishing.
func (s *LiveDVRStore) Close() {
	close(s.stop)
	<-s.done
}

func (s *LiveDVRStore) run() {
	defer close(s.done)
	for {
		select {
		case <-s.stop:
			s.reap(true)
			for len(s.queue) > 0 {
				<-s.queue
			}
			if s.dir != "" {
				_ = os.RemoveAll(s.dir)
				encoderCleanup.dirs.Delete(s.dir)
			}
			return
		case <-s.wake:
			s.reap(false)
		case job := <-s.queue:
			s.write(job)
		}
	}
}

func (s *LiveDVRStore) reap(all bool) {
	s.reapMu.Lock()
	forgotten := s.forgotten
	s.forgotten = nil
	s.reapMu.Unlock()
	c := s.cache
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, f := range forgotten {
		c.remove(f)
	}
	for e := s.files.Front(); e != nil; {
		next := e.Next()
		f := e.Value.(*LiveDVRFile)
		if !all && f.seq >= s.Floor() {
			break
		}
		c.remove(f)
		e = next
	}
}

func (s *LiveDVRStore) write(job liveDVRWrite) {
	f := job.file
	if f.release.Load() || f.seq < s.Floor() {
		return
	}
	c := s.cache
	budget := c.budget()
	if f.size > budget || budget <= 0 {
		s.lose(f.seq)
		return
	}
	if s.dir == "" {
		if err := os.MkdirAll(c.dir, 0o700); err != nil {
			s.lose(f.seq)
			return
		}
		dir, err := os.MkdirTemp(c.dir, "s-live-dvr-")
		if err != nil {
			s.lose(f.seq)
			return
		}
		encoderCleanup.dirs.Store(dir, s)
		s.dir = dir
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for c.bytes > budget-f.size {
		old := c.files.Front().Value.(*LiveDVRFile)
		if old.order > f.order {
			s.lose(f.seq)
			return
		}
		old.store.lose(old.seq)
		if !c.remove(old) {
			s.lose(f.seq)
			return
		}
	}
	if f.seq < s.Floor() {
		return
	}
	f.path = filepath.Join(s.dir, strconv.FormatUint(f.order, 10))
	if err := os.WriteFile(f.path, job.data, 0o600); err != nil {
		_ = os.Remove(f.path)
		s.lose(f.seq)
		return
	}
	at := c.files.Back()
	for at != nil && at.Value.(*LiveDVRFile).order > f.order {
		at = at.Prev()
	}
	if at == nil {
		f.entry = c.files.PushFront(f)
	} else {
		f.entry = c.files.InsertAfter(f, at)
	}
	c.bytes += f.size
	f.ownedEntry = s.files.PushBack(f)
	f.ready.Store(true)
}

func (c *LiveDVRCache) remove(f *LiveDVRFile) bool {
	if f.entry == nil {
		return true
	}
	f.ready.Store(false)
	if err := os.Remove(f.path); err != nil && !os.IsNotExist(err) {
		return false
	}
	c.files.Remove(f.entry)
	f.store.files.Remove(f.ownedEntry)
	f.entry = nil
	c.bytes -= f.size
	return true
}
