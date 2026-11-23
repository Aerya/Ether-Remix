package transcode

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/braedonsaunders/ether/internal/core"
)

var (
	errInputExhausted   = errors.New("transcode: input failed after bounded retries")
	errSessionExhausted = fmt.Errorf("transcode: session failure limit reached: %w", core.ErrEncoderExhausted)
	errSegmentExhausted = fmt.Errorf("transcode: segment failure limit reached: %w", core.ErrEncoderExhausted)
	errSessionStopped   = fmt.Errorf("transcode: session stopped: %w", core.ErrEncoderExhausted)
)

func writeStartError(w http.ResponseWriter, err error) {
	if errors.Is(err, errSessionStopped) {
		http.Error(w, "session stopped", http.StatusGone)
		return
	}
	if errors.Is(err, errInputExhausted) || errors.Is(err, core.ErrEncoderExhausted) {
		http.Error(w, "source unavailable", http.StatusBadGateway)
		return
	}
	http.Error(w, "transcoder failed to start", http.StatusInternalServerError)
}
