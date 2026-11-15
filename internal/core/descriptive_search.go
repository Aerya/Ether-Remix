package core

import "context"

// DescriptiveResult is an AI-generated collection search. Pending requests
// have no invented results. Partial results are provisional; clients keep polling
// and replace them with the final ready result.
type DescriptiveResult struct {
	Spec              *CollectionSpec
	State             string
	RetryAfterSeconds int
	Title             string
	Items             []Title
}

// DescriptiveSearcher reuses the collection builder and never waits for AI
// unless wait is explicit (an asynchronous protocol job, not a search request).
type DescriptiveSearcher interface {
	DescriptiveSearchAvailable() bool
	DescribeSearch(context.Context, *User, string, bool) (DescriptiveResult, error)
}

// DescriptiveCatalog evaluates a generated spec locally, without gathering a
// library-sized in-memory collection or fetching external lists.
type DescriptiveCatalog interface {
	DescribeCandidates(context.Context, CollectionSpec, int) ([]Title, error)
}

// WithDescribeProgress observes bounded candidates from the existing Describe
// tools. The callback must be safe for concurrent read-only tool executions.
func WithDescribeProgress(ctx context.Context, publish func(*CollectionSpec, []Title)) context.Context {
	return context.WithValue(ctx, describeProgressKey{}, publish)
}

type describeProgressKey struct{}

// PublishDescribeProgress is a no-op outside an active headless search job.
func PublishDescribeProgress(ctx context.Context, spec *CollectionSpec, items []Title) {
	if publish, ok := ctx.Value(describeProgressKey{}).(func(*CollectionSpec, []Title)); ok && publish != nil && len(items) > 0 {
		publish(spec, items)
	}
}
