package debrid

import (
	"errors"
	"fmt"
)

// accountError reports a failure of the whole account (quota reached, key
// rejected) as opposed to one of a release or a transient outage.
func accountError(err error) bool {
	return errors.Is(err, ErrQuotaExceeded) || errors.Is(err, ErrAuth)
}

// joinProviderErrors joins the per-provider errors of one operation. A quota
// or key error stays classifiable (errors.Is) only when every active
// provider failed that way: playback then stops the fallbacks that depend on
// debrid. If another provider is healthy or failed otherwise, the account
// error is kept as text only, so a quota on one account cannot cut the
// fallbacks a healthy provider could still serve.
func (s *Service) joinProviderErrors(errs []error) error {
	if len(errs) == 0 {
		return nil
	}
	account := 0
	for _, err := range errs {
		if accountError(err) {
			account++
		}
	}
	if account == 0 || account == len(errs) && account >= len(s.active()) {
		return errors.Join(errs...)
	}
	soft := make([]error, len(errs))
	for i, err := range errs {
		if accountError(err) {
			err = fmt.Errorf("%v", err)
		}
		soft[i] = err
	}
	return errors.Join(soft...)
}
