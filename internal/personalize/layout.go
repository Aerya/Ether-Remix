package personalize

import (
	"fmt"
	"sort"
	"strings"

	"github.com/braedonsaunders/ether/internal/core"
)

// ValidationError lists problems with an owner-supplied document (Home
// layout, taste overrides), addressed by dotted field path.
type ValidationError struct {
	Problems []Problem
}

// Problem is one validation failure.
type Problem struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string {
	if len(e.Problems) == 0 {
		return "invalid"
	}
	p := e.Problems[0]
	return p.Field + ": " + p.Message
}

// InvalidFields returns (field, message) pairs; the admin API maps them to
// its validation error shape.
func (e *ValidationError) InvalidFields() [][2]string {
	out := make([][2]string, len(e.Problems))
	for i, p := range e.Problems {
		out[i] = [2]string{p.Field, p.Message}
	}
	return out
}

func (e *ValidationError) add(field, format string, args ...any) {
	e.Problems = append(e.Problems, Problem{field, fmt.Sprintf(format, args...)})
}

func (e *ValidationError) orNil() error {
	if len(e.Problems) == 0 {
		return nil
	}
	return e
}

var validDevices = map[string]bool{core.DeviceTV: true, core.DeviceMobile: true, core.DeviceWeb: true, core.DeviceDesktop: true}

type decision struct {
	hidden, pinned bool
	// repeats: the row keeps titles other rows show (RowPolicy.ShowRepeats).
	repeats  bool
	position int
}

// hubStem is a hub id without its screen prefix ("home.because.123" →
// "because.123").
func hubStem(hubID string) string {
	for _, p := range []string{"home.", "movie.", "tv."} {
		if s, ok := strings.CutPrefix(hubID, p); ok {
			return s
		}
	}
	return hubID
}

// placePinned inserts pinned rows at their positions (0 = top) into the
// ranked list, in ascending position order.
func placePinned[T any](ranked []T, pinned []T, pos func(T) int) []T {
	sort.SliceStable(pinned, func(i, j int) bool { return pos(pinned[i]) < pos(pinned[j]) })
	out := append([]T{}, ranked...)
	for _, p := range pinned {
		i := pos(p)
		if i > len(out) {
			i = len(out)
		}
		out = append(out, p)
		copy(out[i+1:], out[i:])
		out[i] = p
	}
	return out
}
