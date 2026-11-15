package vela

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strings"

	"github.com/braedonsaunders/ether/internal/frontend/kit/authhdr"
	"github.com/braedonsaunders/ether/internal/logredact"
)

// Error strings can quote remote requests. Remove entire URLs, as well as
// credentials without a named field, before sending anything to the logger.
var failureURL = regexp.MustCompile(`(?i)[a-z][a-z0-9+.-]*://[^\s<>"']+`)
var failureOpaque = regexp.MustCompile(`[A-Za-z0-9_+/=-]{32,}`)

func logFailure(err error, requests ...*http.Request) {
	route := "unmatched"
	if len(requests) > 0 && requests[0] != nil && requests[0].Pattern != "" {
		route = requests[0].Pattern // route template only: never a URL or query
	}
	var chain []string
	var walk func(error, int)
	walk = func(e error, depth int) {
		if e == nil || depth >= 8 || len(chain) >= 16 {
			return
		}
		message := logredact.String(e.Error())
		if len(requests) > 0 && requests[0] != nil {
			if credential, ok := authhdr.Bearer(requests[0].Header.Get("Authorization")); ok {
				message = strings.ReplaceAll(message, credential, logredact.Mask)
			}
		}
		message = failureURL.ReplaceAllString(message, "[url]")
		message = failureOpaque.ReplaceAllString(message, logredact.Mask)
		message = strings.Map(func(r rune) rune {
			if r < 32 {
				return ' '
			}
			return r
		}, message)
		if len(message) > 1024 {
			message = message[:1024]
		}
		chain = append(chain, fmt.Sprintf("%T: %s", e, message))
		if many, ok := e.(interface{ Unwrap() []error }); ok {
			for _, child := range many.Unwrap() {
				walk(child, depth+1)
			}
		} else {
			walk(errors.Unwrap(e), depth+1)
		}
	}
	walk(err, 0)
	slog.Warn("Vela request failed", "route", route, "error_class", fmt.Sprintf("%T", err), "error_chain", chain)
}
