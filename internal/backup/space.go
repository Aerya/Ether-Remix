package backup

import (
	"fmt"
	"io"

	"github.com/braedonsaunders/ether/internal/datafs"
)

const stageSpaceInterval = 4 << 20

// stageFloor is the free space a staging operation leaves on its volume:
// 5% of it, at least 2 GiB and at most 20 GiB, so a large array is not
// refused a restore while hundreds of gigabytes are free.
var stageFloor = func(total int64) int64 { return max(2<<30, min(total/20, 20<<30)) }

// DisableStageFloorForTests turns the free-space floor off for the rest of
// the process. Tests in other packages stage tiny bundles on whatever volume
// the machine has; real servers never call it.
func DisableStageFloorForTests() { stageFloor = func(int64) int64 { return 0 } }

// stageSpace shares one disk budget across all files in a staging operation.
type stageSpace struct {
	path, operation string
	allowance       int64
}

func (s *stageSpace) check(need int64) error {
	total, free, err := datafs.Usage(s.path)
	if err != nil {
		return fmt.Errorf("could not check free disk space to %s: %w", s.operation, err)
	}
	available := max(0, free) - stageFloor(total)
	if available < need {
		mib := float64(need-available) / (1 << 20)
		return fmt.Errorf("not enough free disk space to %s: it needs about %.0f MiB more. Free up space and try again", s.operation, max(1, mib))
	}
	s.allowance = min(available, stageSpaceInterval)
	return nil
}

func (s *stageSpace) writer(dst io.Writer) io.Writer {
	return stageWriter{space: s, dst: dst}
}

type stageWriter struct {
	space *stageSpace
	dst   io.Writer
}

func (w stageWriter) Write(p []byte) (int, error) {
	written := 0
	for len(p) > 0 {
		if w.space.allowance == 0 {
			if err := w.space.check(min(int64(len(p)), stageSpaceInterval)); err != nil {
				return written, err
			}
		}
		chunk := p[:min(int64(len(p)), w.space.allowance)]
		n, err := w.dst.Write(chunk)
		w.space.allowance -= int64(n)
		written += n
		if err != nil {
			return written, err
		}
		if n != len(chunk) {
			return written, io.ErrShortWrite
		}
		p = p[n:]
	}
	return written, nil
}
