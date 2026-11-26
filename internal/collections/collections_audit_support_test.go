package collections

// WithExternalBases points external list fetches at other origins (tests).
func WithExternalBases(b ExternalBases) Option { return func(s *Service) { s.opts.bases = &b } }
