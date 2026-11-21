package vela

// Ajout Ether-Remix : pause d'un indexeur imposée par le service. Le 429 est
// gardé ; le code distingue cette pause du budget journalier, et Retry-After
// donne la reprise quand elle est connue.

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/braedonsaunders/ether/internal/frontend"
)

func pseIndexerThrottled(w http.ResponseWriter, err error) {
	var t *frontend.IndexerThrottledError
	if !errors.As(err, &t) || t.RetryAt.IsZero() {
		problem(w, 429, "capacity", "indexer_throttled")
		return
	}
	seconds := max(1, int((time.Until(t.RetryAt)+time.Second-1)/time.Second))
	w.Header().Set("Retry-After", strconv.Itoa(seconds))
	send(w, 429, object{"error": object{"class": "capacity", "code": "indexer_throttled", "message": "indexer throttled", "retry": object{"allowed": true, "afterSeconds": seconds}}})
}
