package transcode

import (
	"context"
	"errors"
	"slices"
	"sync"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

// SlotPriority separates interactive viewing from cancellable render jobs.
type SlotPriority uint8

const (
	SlotViewer SlotPriority = iota
	SlotBackground
	// SlotLivePrep is a single reserved lane for imminent channel masters.
	// It cannot be preempted by a viewer or consume the viewer encoder budget.
	SlotLivePrep
)

type transcodeSlot struct {
	priority       SlotPriority
	cancel         context.CancelFunc
	released       bool
	multiviewGroup string
}

// maxMultiviewGroupEncoders bounds concurrent encoders within a single
// multiview group (4 active tiles plus up to 4 concurrent handovers/replans).
const maxMultiviewGroupEncoders = 8

// AcquireTranscodeSlot reserves the shared encoder budget. Run the job under
// jobCtx, and always release after its process has stopped. Viewers preempt the
// newest background holder; preemption cancels jobCtx and reclaims its charge
// immediately. Background callers wait behind every queued viewer.
func (m *Manager) AcquireTranscodeSlot(ctx context.Context, p SlotPriority) (context.Context, func(), error) {
	return m.acquirePrioritySlot(ctx, p, true)
}

func (m *Manager) acquirePrioritySlot(ctx context.Context, p SlotPriority, wait bool) (jobCtx context.Context, release func(), err error) {
	jobCtx, _, release, err = m.acquirePrioritySlotForGroup(ctx, p, wait, "")
	return jobCtx, release, err
}

func (m *Manager) acquirePrioritySlotForGroup(ctx context.Context, p SlotPriority, wait bool, multiviewGroup string) (jobCtx context.Context, holder *transcodeSlot, release func(), err error) {
	if p != SlotViewer && p != SlotBackground && p != SlotLivePrep {
		return nil, nil, nil, errors.New("transcode: invalid slot priority")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if p == SlotViewer {
		if err := m.waitViewerSlotLocked(ctx, nil, multiviewGroup, wait); err != nil {
			return nil, nil, nil, err
		}
	} else if err := m.waitBackgroundSlotLocked(ctx, p, wait); err != nil {
		return nil, nil, nil, err
	}
	jobCtx = ctx
	var cancel context.CancelFunc
	if p != SlotViewer {
		jobCtx, cancel = context.WithCancel(ctx)
	}
	holder = &transcodeSlot{priority: p, cancel: cancel, multiviewGroup: multiviewGroup}
	m.slotHolders = append(m.slotHolders, holder)
	if p == SlotLivePrep {
		m.livePrepEncodes++
	} else {
		m.downloadEncodes++
	}
	var once sync.Once
	release = func() { once.Do(func() { m.mu.Lock(); m.releaseSlotLocked(holder); m.mu.Unlock() }) }
	return jobCtx, holder, release, nil
}

func (m *Manager) waitBackgroundSlotLocked(ctx context.Context, p SlotPriority, wait bool) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if m.closed {
			return errors.New("transcode: manager closed")
		}
		switch p {
		case SlotLivePrep:
			if m.livePrepEncodes == 0 {
				return nil
			}
		case SlotBackground:
			if m.slotViewers.Len() == 0 && !m.transcodeFullLocked("", "") {
				return nil
			}
		}
		if !wait {
			return ErrTooManyTranscodes
		}
		m.waitSlotChangeLocked(ctx, nil)
	}
}

// waitSlotChangeLocked returns with m.mu held. Ramps and rung transitions
// can release capacity independently of the session and permit registries.
func (m *Manager) waitSlotChangeLocked(ctx context.Context, retired <-chan struct{}) {
	if m.slotChanged == nil {
		m.slotChanged = make(chan struct{})
	}
	changed := m.slotChanged
	m.mu.Unlock()
	timer := time.NewTimer(25 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-m.stop:
	case <-retired:
	case <-changed:
	case <-timer.C:
	}
	m.mu.Lock()
}

// Only the first viewer can claim a slot or preempt a background holder.
// Keep m.mu held on success so the caller can immediately charge the budget.
func (m *Manager) waitViewerSlotLocked(ctx context.Context, retired <-chan struct{}, group string, wait bool) error {
	turn := m.slotViewers.PushBack(struct{}{})
	defer func() {
		m.slotViewers.Remove(turn)
		m.notifySlotsLocked()
	}()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if m.closed {
			return errors.New("transcode: manager closed")
		}
		select {
		case <-retired:
			return context.Canceled
		default:
		}
		if m.slotViewers.Front() == turn {
			if group == "" || m.groupEncodingCountLocked(group) < maxMultiviewGroupEncoders {
				for m.transcodeFullLocked(group, "") && m.preemptBackgroundLocked() {
				}
			}
			if !m.transcodeFullLocked(group, "") {
				return nil
			}
		}
		if !wait {
			return m.encoderCapacityErrorLocked()
		}
		m.waitSlotChangeLocked(ctx, retired)
	}
}

func (m *Manager) setSlotGroup(h *transcodeSlot, multiviewGroup string) {
	if m == nil || h == nil {
		return
	}
	m.mu.Lock()
	if !h.released && h.multiviewGroup != multiviewGroup {
		h.multiviewGroup = multiviewGroup
		m.notifySlotsLocked()
	}
	m.mu.Unlock()
}
func (m *Manager) notifySlotsLocked() {
	if m.slotChanged != nil {
		close(m.slotChanged)
		m.slotChanged = make(chan struct{})
	}
}
func (m *Manager) releaseSlotLocked(h *transcodeSlot) {
	if h.released {
		return
	}
	h.released = true
	if h.cancel != nil {
		h.cancel()
	}
	if h.priority == SlotLivePrep {
		m.livePrepEncodes--
	} else {
		m.downloadEncodes--
	}
	for i, v := range m.slotHolders {
		if v == h {
			m.slotHolders = append(m.slotHolders[:i], m.slotHolders[i+1:]...)
			break
		}
	}
	m.notifySlotsLocked()
}
func (m *Manager) preemptBackgroundLocked() bool {
	for i := len(m.slotHolders) - 1; i >= 0; i-- {
		h := m.slotHolders[i]
		if h.priority == SlotBackground && !h.released {
			m.releaseSlotLocked(h)
			return true
		}
	}
	return false
}

// EncodingCount reports held encoding slots, including runs awaiting retirement.
func (m *Manager) EncodingCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.encodingLocked("")
}

// VideoEncoderLimit is the effective local limit. Hardware detection checks
// encoder availability, but does not measure concurrent throughput.
func (m *Manager) VideoEncoderLimit() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cfg.MaxTranscodes
}

// VideoEncoderBusy is advisory for source selection. Admission checks the
// budget again under the lock after deciding how to deliver the source.
func (m *Manager) VideoEncoderBusy(group string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.transcodeFullLocked(group, "") || m.slotViewers.Len() > 0
}

// EncoderCapacityError carries a snapshot of the local budget at rejection.
// Viewers is for owner diagnostics only.
type EncoderCapacityError struct {
	Running int
	Viewers []string
}

func (*EncoderCapacityError) Error() string { return ErrTooManyTranscodes.Error() }
func (*EncoderCapacityError) Unwrap() error { return ErrTooManyTranscodes }

func (m *Manager) encoderCapacityErrorLocked() error {
	e := &EncoderCapacityError{Running: m.encodingLocked("")}
	for life := range m.encoders {
		if life.viewer != "" && (life.encoding.Load() || life.runs.Load() > 0) {
			e.Viewers = append(e.Viewers, life.viewer)
		}
	}
	slices.Sort(e.Viewers)
	e.Viewers = slices.Compact(e.Viewers)
	return e
}

// admitStartEncoding uses the same viewer order as acquirePrioritySlotForGroup.
// Charge the lifetime before unlocking so a new start cannot overtake a waiter.
func (m *Manager) admitStartEncoding(ctx context.Context, req core.HLSRequest, life *encoderLifetime, encoding bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return errors.New("transcode: manager closed")
	}
	videoOff := func() bool {
		return !req.LiveInput && (req.NoVideoTranscode || m.cfg.VideoTranscodingOff())
	}
	if !encoding {
		life.viewer = req.EncoderViewer
		life.multiviewGroup = req.MultiviewGroup
		return nil
	}
	if videoOff() {
		return ErrVideoTranscodingDisabled
	}
	if req.WaitForEncoder {
		bounded, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		if err := m.waitViewerSlotLocked(bounded, life.cancel, req.MultiviewGroup, true); err != nil {
			if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
				return m.encoderCapacityErrorLocked()
			}
			return err
		}
	} else if err := m.waitViewerSlotLocked(ctx, life.cancel, req.MultiviewGroup, false); err != nil {
		return err
	}
	if videoOff() {
		return ErrVideoTranscodingDisabled
	}
	life.viewer = req.EncoderViewer
	life.multiviewGroup = req.MultiviewGroup
	life.encoding.Store(true)
	return nil
}
