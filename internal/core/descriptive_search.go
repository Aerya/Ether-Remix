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

// DescriptiveSearcher reuses the collection builder and waits for AI when
// wait is explicit (protocol AI jobs and bounded descriptive searches).
type DescriptiveSearcher interface {
	DescriptiveSearchAvailable() bool
	DescribeSearch(context.Context, *User, string, bool) (DescriptiveResult, error)
}

// DescriptiveSearchController is an optional extension of DescriptiveSearcher
// for warm-cache checks and cancelling jobs superseded by search-as-you-type.
type DescriptiveSearchController interface {
	DescriptiveSearcher
	DescribeCached(user *User, query string) bool
	CancelDescribeSearch(user *User, query string)
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

type searchClientKey struct{}

// WithSearchClient attaches the client/device identifier for per-client
// search-as-you-type single-flight cancellation.
func WithSearchClient(ctx context.Context, clientID string) context.Context {
	if clientID == "" {
		return ctx
	}
	return context.WithValue(ctx, searchClientKey{}, clientID)
}

// SearchClientFrom returns the client/device identifier attached by WithSearchClient.
func SearchClientFrom(ctx context.Context) string {
	id, _ := ctx.Value(searchClientKey{}).(string)
	return id
}
