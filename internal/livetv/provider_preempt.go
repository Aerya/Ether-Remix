package livetv

import (
	"context"
	"errors"
)

// withProviderCapacity retries admission only after an idle output has fully
// released its input. The existing reservation/lease/tap locks remain the
// authority: competing tunes still cannot acquire the same slot twice.
// Call only from viewer admission, never from a broadcast's own frame workers.
func (s *Service) withProviderCapacity(ctx context.Context, c Channel, src StreamSource, admit func() error) error {
	err := admit()
	if !errors.Is(err, ErrProviderBusy) || ctx.Err() != nil {
		return err
	}
	account := src.AuthSourceID
	if account == "" {
		account = c.SourceID
	}
	if s.preemptIdleProvider(account, c.ID) && ctx.Err() == nil {
		return admit()
	}
	return err
}

func (s *Service) preemptIdleProvider(account, exceptChannel string) bool {
	if account == "" {
		return false
	}
	// Inspect only current provider reservations, never the library/lineup.
	s.mu.Lock()
	channels := map[string]bool{}
	for _, channel := range s.broadcastProviders[account] {
		if channel != exceptChannel {
			channels[channel] = true
		}
	}
	s.mu.Unlock()
	r := &s.broadcasts
	r.mu.Lock()
	waiting := map[*broadcast]bool{}
	for b, key := range r.retiring {
		if channels[key.channelID] {
			waiting[b] = true
		}
	}
	for key, b := range r.outputs {
		if !channels[key.channelID] {
			continue
		}
		b.mu.Lock()
		// A new output is awaiting its first viewer. Only previously attached
		// outputs qualify for preemption, even immediately after detachment.
		if b.viewers == 0 && b.attaching == 0 && b.hadViewer && !b.closed {
			b.closed = true
			b.cancel()
			delete(r.outputs, key)
			if r.retiring == nil {
				r.retiring = map[*broadcast]broadcastKey{}
			}
			r.retiring[b] = key
			waiting[b] = true
		}
		b.mu.Unlock()
	}
	r.mu.Unlock()
	for b := range waiting {
		b.close()
		r.mu.Lock()
		delete(r.retiring, b)
		r.mu.Unlock()
	}
	return len(waiting) > 0
}
