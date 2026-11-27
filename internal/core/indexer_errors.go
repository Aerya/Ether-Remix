package core

// IndexersPausedError means no indexer answered during source resolution.
// Names contains only the owner-configured display labels. Keep them off
// Error so wrapped errors and operational logs never expose them.
type IndexersPausedError struct {
	Names []string
}

func (*IndexersPausedError) Error() string { return "indexer_paused" }
func (*IndexersPausedError) Unwrap() error { return ErrSourceUnavailable }
