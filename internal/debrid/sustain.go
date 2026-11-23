package debrid

import (
	"context"
	"fmt"
	"math"
	"slices"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

type sustainSlotKey struct{}

// sustainPolicy targets the existing ten seconds of reader consumption plus
// 25% headroom for variable bitrate and range turnaround. The old 32 MiB cap
// holds only ~4 seconds at a UHD remux's ~66 Mbps. Multiplying that ceiling by
// the existing four host connections permits ten seconds at those rates,
// without enlarging the 12 MiB memory LRU: completed excess lives on disk.
// Cache capacity (or the memory-only allowance) always wins over that target.
// Depth and parallelism floor demand at the known average media bitrate.
// Parallelism uses the existing per-run throughput EWMA with the same headroom.
func sustainPolicy(consumption, bitrateFloor, tput float64, budget, fixed int64, capRuns int, measured bool) (window int64, parallel int) {
	// Foreground delivery can be supply-limited, so it cannot disprove the
	// demand implied by the known file size and playback duration.
	consumption = max(consumption, bitrateFloor)
	ceiling := min(int64(maxReadAheadBytes*defaultHostConnections), max(0, budget))
	window = min(ceiling, max(int64(minReadAheadBytes), int64(consumption*readAheadSeconds*1.25)))
	if fixed > 0 {
		window = min(ceiling, fixed)
	}
	parallel = 2 // retain the fallback when connection throughput is unknown
	if !measured {
		parallel = capRuns // full bounded startup until the first consumption interval
	} else if consumption > 0 && tput > 0 {
		parallel = int(math.Ceil(consumption * 1.25 / tput))
	}
	return window, min(max(1, capRuns), max(1, parallel))
}

func (s *httpSource) aheadBudget() int64 {
	// Leave a fresh urgent run in the existing LRU. Without disk retention,
	// speculative completion must not evict the blocks the reader needs next.
	memory := max(int64(0), memBlocksPerFile-freshRunBlocks) * s.f.e.block
	cache := s.f.e.cache
	if cache == nil {
		return memory
	}
	var budget int64
	if limit, ok := cache.(core.SegmentCacheLimit); ok {
		budget = limit.UsageLimit()
	} else {
		_, budget = cache.Usage()
		if budget == 0 { // SegmentCache.Usage uses zero for unlimited
			budget = -1
		}
	}
	if budget < 0 { // unbounded disk cache, still bounded by sustainPolicy
		return maxReadAheadBytes * defaultHostConnections
	}
	return max(memory, budget/2) // reserve cache space for demand and other viewers
}

// readAheadDialUrgent exempts refill from spacing below 2.5 seconds of
// contiguous downloaded media. In-flight bytes are not a playback reserve.
// Open/seek epochs remain urgent until their first consumption measurement.
// The scan never exceeds the existing bounded read-ahead horizon.
func (s *httpSource) readAheadDialUrgent(epoch context.Context) bool {
	s.mu.Lock()
	active := !s.closed && epoch == s.raCtx && epoch.Err() == nil
	measured, rate, playhead := s.sustain.measured, s.sustain.rate, s.sustain.playhead
	priority := s.priority
	s.mu.Unlock()
	if !active || (priority != nil && !priority()) {
		return false
	}
	if !measured {
		return true
	}
	f := s.f
	rate = max(rate, float64(f.bitrateFloor.Load()))
	lowWater := int64(min(float64(maxReadAheadBytes*defaultHostConnections), rate*2.5))
	if rate <= 0 {
		lowWater = minReadAheadBytes
	}
	end := min(f.size, playhead+lowWater)
	f.bmu.Lock()
	defer f.bmu.Unlock()
	for idx := playhead / f.e.block; idx*f.e.block < end; idx++ {
		if f.edges[idx] == nil && !f.mem.Contains(idx) && !f.presentOnDiskLocked(idx) {
			return true
		}
	}
	return false
}

type sourceSustain struct {
	measured            bool // a complete consumption interval in this seek epoch
	rate                float64
	rateAt, lastRead    time.Time
	rateBytes           int64
	logAt               time.Time
	logBytes, delivered int64
	playhead            int64 // completed forward bytes within the current seek epoch
}

func (m *sourceSustain) resetRate(now time.Time) {
	m.rate, m.rateBytes, m.rateAt, m.measured = 0, 0, now, false
}

func (s *httpSource) beginConsumption(now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m := &s.sustain
	if m.rateAt.IsZero() || now.Sub(m.lastRead) >= readAheadSeconds*time.Second {
		m.resetRate(now)
	}
	if m.logAt.IsZero() {
		m.logAt = now
	}
	m.lastRead = now
}

// recordConsumption is called once when a foreground read returns. There is no
// logging timer, sampling goroutine, or per-read log; paused sources do no work.
func (s *httpSource) recordConsumption(n int, off int64, epoch context.Context) {
	if n <= 0 {
		return
	}
	f := s.f
	now := f.e.now()
	// Consumption can lower the reserve while a read-ahead dial is sleeping.
	// Wake after releasing source/cache locks: dial admission checks them.
	defer f.e.limit.wake()
	s.mu.Lock()
	m := &s.sustain
	if epoch == s.raCtx {
		m.playhead = max(m.playhead, off+int64(n))
		m.rateBytes += int64(n)
	}
	m.delivered += int64(n)
	m.logBytes += int64(n)
	if elapsed := now.Sub(m.rateAt); epoch == s.raCtx && elapsed >= time.Second {
		m.rate = ewma(m.rate, float64(m.rateBytes)/elapsed.Seconds(), m.rate == 0)
		m.rateAt, m.rateBytes, m.measured = now, 0, true
	}
	m.lastRead = now
	elapsed := now.Sub(m.logAt)
	if elapsed < 10*time.Second {
		s.mu.Unlock()
		return
	}
	delivered, intervalBytes, rate, playhead := m.delivered, m.logBytes, m.rate, m.playhead
	m.logAt, m.logBytes = now, 0
	s.mu.Unlock()

	// Inspect at most the bounded adaptive horizon, never the whole file or
	// library. Flights are unfinished bytes, not downloaded/buffered bytes.
	end := min(f.size, playhead+int64(maxReadAheadBytes*defaultHostConnections))
	last := (end - 1) / f.e.block
	var inFlight, buffered int64
	contiguous := true
	f.bmu.Lock()
	for idx := playhead / f.e.block; idx <= last; idx++ {
		bytes := min(end, idx*f.e.block+f.blockLen(idx)) - max(playhead, idx*f.e.block)
		ready := f.edges[idx] != nil || f.mem.Contains(idx) || f.presentOnDiskLocked(idx)
		if fl := f.flights[idx]; fl != nil {
			inFlight += bytes
		}
		if contiguous && ready {
			buffered += bytes
		} else {
			contiguous = false
		}
	}
	runs, tput := f.runCount, f.tput
	var samples [32]time.Duration
	count := int(min(f.ttfbNext, uint64(len(samples))))
	copy(samples[:], f.ttfbs[:count])
	f.bmu.Unlock()
	slices.Sort(samples[:count])
	var median time.Duration
	if count > 0 {
		median = samples[count/2]
		if count%2 == 0 {
			median = samples[count/2-1] + (median-samples[count/2-1])/2
		}
	}
	f.e.log.Info("debrid playback throughput", "release", f.id, "source", fmt.Sprintf("%p", s),
		"deliveredBytes", delivered, "intervalBytes", intervalBytes, "intervalSeconds", elapsed.Seconds(),
		"goodputMbps", float64(intervalBytes)*8/elapsed.Seconds()/1e6,
		"consumptionMbps", rate*8/1e6, "bitrateFloorMbps", float64(f.bitrateFloor.Load())*8/1e6, "connectionMbps", tput*8/1e6,
		"aheadInFlightBytes", inFlight, "bufferedAheadBytes", buffered,
		"concurrentRuns", runs, "medianTTFBMs", float64(median)/float64(time.Millisecond), "ttfbSamples", count)
}
