package frontend

import "github.com/braedonsaunders/ether/internal/core"

// Liaison sans import direct de core (TestFrontendImports) : pause d'un
// indexeur imposée par le service, voir internal/core/pse_indexer_throttle.go.
var ErrIndexerThrottled = core.ErrIndexerThrottled

// IndexerThrottledError : voir core.IndexerThrottledError.
type IndexerThrottledError = core.IndexerThrottledError
