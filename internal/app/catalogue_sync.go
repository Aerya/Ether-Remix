package app

import (
	"context"
	"github.com/braedonsaunders/ether/internal/pse/pseenv"
	"strconv"
	"strings"
	"sync"
	"time"
)

func catalogueSyncTimeout() time.Duration {
	seconds, err := strconv.Atoi(strings.TrimSpace(pseenv.Get("PSE_CATALOGUE_SYNC_TIMEOUT")))
	if err != nil || seconds < 30 || seconds > 900 {
		seconds = 180
	}
	return time.Duration(seconds) * time.Second
}

type catalogueSyncStatus struct {
	Running  bool   `json:"running"`
	Error    string `json:"error,omitempty"`
	Started  int64  `json:"started"`
	Finished int64  `json:"finished,omitempty"`
}
type catalogueSyncJobs struct {
	mu     sync.Mutex
	jobs   map[string]catalogueSyncStatus
	ctx    context.Context
	cancel context.CancelFunc
}

func (j *catalogueSyncJobs) status(id string) catalogueSyncStatus {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.jobs[id]
}
func (j *catalogueSyncJobs) start(id string, fn func(context.Context) error) catalogueSyncStatus {
	j.mu.Lock()
	defer j.mu.Unlock()
	if old := j.jobs[id]; old.Running {
		return old
	}
	if j.jobs == nil {
		j.jobs = map[string]catalogueSyncStatus{}
	}
	// Completed results are only useful for the current UI polling window.
	for k, v := range j.jobs {
		if !v.Running && time.Now().Unix()-v.Finished > 3600 {
			delete(j.jobs, k)
		}
	}
	status := catalogueSyncStatus{Running: true, Started: time.Now().Unix()}
	j.jobs[id] = status
	ctx := j.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	go func(status catalogueSyncStatus) {
		// Includes time queued behind other catalogue work. Each fetch has its
		// own configured timeout once it acquires the loader slot.
		ctx, cancel := context.WithTimeout(ctx, 20*time.Minute)
		defer cancel()
		err := fn(ctx)
		status.Running = false
		status.Finished = time.Now().Unix()
		if err != nil {
			status.Error = err.Error()
		}
		j.mu.Lock()
		j.jobs[id] = status
		j.mu.Unlock()
	}(status)
	return status
}
