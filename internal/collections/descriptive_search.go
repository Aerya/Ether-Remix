package collections

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net"
	"slices"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/contentrating"
	"github.com/braedonsaunders/ether/internal/core"
)

const describeCacheMax = 256

// The draft, name resolution and bounded candidates share one deadline. A
// detached search request cannot inherit the caller's short response budget.
const describeJobTimeout = 2 * time.Minute

// CollectionDraftAgent is the console Describe it tool loop, run headlessly.
// Kept as a dependency to avoid coupling collection evaluation to the agent.
type CollectionDraftAgent interface {
	DescribeCollectionAvailable() bool
	DescribeCollection(context.Context, string) (*core.CollectionSpec, error)
}

func (s *Service) SetDescribeAgent(agent CollectionDraftAgent) {
	s.depsMu.Lock()
	s.describeAgent = agent
	s.depsMu.Unlock()
}

func (s *Service) collectionDraftAgent() CollectionDraftAgent {
	s.depsMu.RLock()
	defer s.depsMu.RUnlock()
	return s.describeAgent
}

type describeDraft struct {
	done         chan struct{}
	listeners    []func(*core.CollectionSpec, []core.Title)
	partialSpec  *core.CollectionSpec
	partialItems []core.Title
	spec         *core.CollectionSpec
	err          error
	at           time.Time
}
type describeJob struct {
	partial    chan struct{}
	done       chan struct{}
	cancel     context.CancelFunc
	waiters    int
	superseded bool
	result     core.DescriptiveResult
	err        error
	at         time.Time
}

func (s *Service) DescriptiveSearchAvailable() bool {
	agent := s.collectionDraftAgent()
	return agent != nil && agent.DescribeCollectionAvailable()
}

// DescribeCached reports whether query already has a live (in-progress or
// cached) descriptive search job for user without starting a new job.
func (s *Service) DescribeCached(user *core.User, query string) bool {
	query = strings.TrimSpace(query)
	if query == "" || len(query) > 1000 {
		return false
	}
	normalized := strings.Join(strings.Fields(strings.ToLower(query)), " ")
	key := fmt.Sprintf("%d|%s", vibeOwner(user), normalized)
	s.describeMu.Lock()
	defer s.describeMu.Unlock()
	job := s.describeJobs[key]
	if job == nil {
		return false
	}
	select {
	case <-job.done:
		ttl := 10 * time.Minute
		if job.err != nil {
			ttl = time.Minute
		}
		return s.now().Sub(job.at) < ttl
	default:
		return true
	}
}

// CancelDescribeSearch cancels an in-progress descriptive search job for query
// when no active callers remain waiting on it (e.g. superseded keystroke text).
func (s *Service) CancelDescribeSearch(user *core.User, query string) {
	query = strings.TrimSpace(query)
	if query == "" || len(query) > 1000 {
		return
	}
	normalized := strings.Join(strings.Fields(strings.ToLower(query)), " ")
	key := fmt.Sprintf("%d|%s", vibeOwner(user), normalized)
	s.describeMu.Lock()
	job := s.describeJobs[key]
	if job == nil || job.waiters > 0 {
		s.describeMu.Unlock()
		return
	}
	select {
	case <-job.done:
		s.describeMu.Unlock()
		return
	default:
	}
	job.superseded = true
	delete(s.describeJobs, key)
	cancel := job.cancel
	s.describeMu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// DescribeSearch schedules at most two jobs. Cache hits never call the model.
// Drafts are shared by normalized query; results and taste are viewer scoped.
// Errors back off for a minute. Maps are lazy and bounded, with no sweeper.
func (s *Service) DescribeSearch(ctx context.Context, user *core.User, query string, wait bool) (core.DescriptiveResult, error) {
	if !s.DescriptiveSearchAvailable() {
		return core.DescriptiveResult{State: "unavailable"}, nil
	}
	query = strings.TrimSpace(query)
	if query == "" || len(query) > 1000 {
		return core.DescriptiveResult{}, fmt.Errorf("invalid descriptive search")
	}
	if err := ctx.Err(); err != nil {
		return core.DescriptiveResult{}, err
	}
	normalized := strings.Join(strings.Fields(strings.ToLower(query)), " ")
	key := fmt.Sprintf("%d|%s", vibeOwner(user), normalized)
	s.describeMu.Lock()
	job := s.describeJobs[key]
	if job != nil {
		select {
		case <-job.done:
			ttl := 10 * time.Minute
			if job.err != nil {
				ttl = time.Minute
			}
			if s.now().Sub(job.at) >= ttl {
				delete(s.describeJobs, key)
				job = nil
			}
		default:
		}
	}
	if job == nil {
		if s.describeActive >= 2 {
			s.describeMu.Unlock()
			return core.DescriptiveResult{State: "busy", RetryAfterSeconds: 2}, nil
		}
		if s.describeJobs == nil {
			s.describeJobs = map[string]*describeJob{}
		}
		if len(s.describeJobs) >= describeCacheMax {
			for k, j := range s.describeJobs {
				select {
				case <-j.done:
					delete(s.describeJobs, k)
				default:
				}
				if len(s.describeJobs) < describeCacheMax {
					break
				}
			}
		}
		jobCtx, jobCancel := context.WithCancel(context.WithoutCancel(ctx))
		job = &describeJob{done: make(chan struct{}), partial: make(chan struct{}), cancel: jobCancel, at: s.now()}
		s.describeJobs[key] = job
		s.describeActive++
		var viewer *core.User
		if user != nil {
			copy := *user
			viewer = &copy
		}
		go s.runDescribe(jobCtx, viewer, normalized, query, job)
	}
	if wait {
		job.waiters++
	}
	s.describeMu.Unlock()
	if wait {
		defer func() {
			s.describeMu.Lock()
			job.waiters--
			s.describeMu.Unlock()
		}()
		select {
		case <-ctx.Done():
			return core.DescriptiveResult{}, ctx.Err()
		case <-job.done:
		case <-job.partial:
			s.describeMu.Lock()
			provisional := job.result
			s.describeMu.Unlock()
			if provisional.State == "partial" {
				core.PublishDescribeProgress(ctx, provisional.Spec, provisional.Items)
			}
			select {
			case <-ctx.Done():
				return core.DescriptiveResult{}, ctx.Err()
			case <-job.done:
			}
		}
	}
	s.describeMu.Lock()
	result, jobErr, at := job.result, job.err, job.at
	s.describeMu.Unlock()
	if result.State == "" {
		result.State, result.RetryAfterSeconds = "pending", 2
	}
	if result.State == "failed" {
		result.RetryAfterSeconds = max(1, int(math.Ceil((time.Minute - s.now().Sub(at)).Seconds())))
	}
	result.Items = slices.Clone(result.Items)
	result.Items = slices.DeleteFunc(result.Items, func(t core.Title) bool {
		enf := contentrating.EffectiveEnforcement(t.EnforcementRating, t.ContentRating, t.Adult, t.Keywords)
		return t.Adult || (user != nil && contentrating.EnforceExceeds(enf, user.MaxContentRating))
	})
	if result.Spec != nil {
		sp := cloneSpec(*result.Spec)
		result.Spec = &sp
	}
	return result, jobErr
}

func (s *Service) describeSpec(ctx context.Context, key, query string, publish func(*core.CollectionSpec, []core.Title)) (*core.CollectionSpec, error) {
	s.describeMu.Lock()
	if s.describeDrafts == nil {
		s.describeDrafts = map[string]*describeDraft{}
	}
	draft := s.describeDrafts[key]
	if draft != nil {
		select {
		case <-draft.done:
			ttl := 6 * time.Hour
			if draft.err != nil {
				ttl = time.Minute
			}
			if s.now().Sub(draft.at) >= ttl {
				delete(s.describeDrafts, key)
				draft = nil
			}
		default:
		}
	}
	if draft != nil {
		select {
		case <-draft.done:
			s.describeMu.Unlock()
			return draft.spec, draft.err
		default:
		}
		draft.listeners = append(draft.listeners, publish)
		partialSpec, partialItems := draft.partialSpec, draft.partialItems
		s.describeMu.Unlock()
		if len(partialItems) > 0 {
			publish(partialSpec, partialItems)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-draft.done:
			return draft.spec, draft.err
		}
	}
	if len(s.describeDrafts) >= describeCacheMax {
		for k, d := range s.describeDrafts {
			select {
			case <-d.done:
				delete(s.describeDrafts, k)
			default:
			}
			if len(s.describeDrafts) < describeCacheMax {
				break
			}
		}
	}
	draft = &describeDraft{done: make(chan struct{}), at: s.now(), listeners: []func(*core.CollectionSpec, []core.Title){publish}}
	s.describeDrafts[key] = draft
	s.describeMu.Unlock()
	// Run exactly the console's Describe it agent, not the one-shot generator.
	var spec *core.CollectionSpec
	var err error
	if agent := s.collectionDraftAgent(); agent != nil {
		progress := func(spec *core.CollectionSpec, items []core.Title) {
			s.describeMu.Lock()
			// Only retain the first useful candidates while the agent refines. The
			// final draft always replaces these and runs normal taste ordering.
			if len(draft.partialItems) > 0 {
				s.describeMu.Unlock()
				return
			}
			draft.partialItems = slices.Clone(items)
			if spec != nil {
				sp := cloneSpec(*spec)
				draft.partialSpec = &sp
			}
			listeners := slices.Clone(draft.listeners)
			partialSpec, partialItems := draft.partialSpec, draft.partialItems
			s.describeMu.Unlock()
			for _, listener := range listeners {
				listener(partialSpec, partialItems)
			}
		}
		spec, err = agent.DescribeCollection(core.WithDescribeProgress(core.WithoutCatalogViewer(ctx), progress), query)
	} else {
		err = ErrLLMDisabled
	}
	if err == nil && spec == nil {
		err = fmt.Errorf("collection agent returned no draft")
	}
	if err == nil {
		// Rules only: never log the request, model explanation or credentials.
		log := s.log
		if log == nil {
			log = slog.Default()
		}
		log.Debug("descriptive search rules", "media_type", spec.MediaType, "match", spec.Match, "rules", spec.Rules, "exclude", spec.Exclude)
	}
	s.describeMu.Lock()
	draft.spec, draft.err, draft.at = spec, err, s.now()
	draft.listeners = nil
	draft.partialItems, draft.partialSpec = nil, nil
	if errors.Is(err, context.Canceled) && s.describeDrafts[key] == draft {
		delete(s.describeDrafts, key)
	}
	close(draft.done)
	s.describeMu.Unlock()
	return spec, err
}

func (s *Service) runDescribe(parent context.Context, user *core.User, key, query string, job *describeJob) {
	ctx, cancel := context.WithTimeout(parent, describeJobTimeout)
	defer cancel()
	if job.cancel != nil {
		defer job.cancel()
	}
	started := time.Now()
	stage := "draft"
	var draftDuration, candidateDuration, scoringDuration time.Duration
	publish := func(spec *core.CollectionSpec, items []core.Title) {
		if ctx.Err() != nil || len(items) == 0 {
			return
		}
		result := core.DescriptiveResult{State: "partial", RetryAfterSeconds: 2, Items: slices.Clone(items)}
		if spec != nil {
			sp := cloneSpec(*spec)
			result.Spec, result.Title = &sp, sp.Name
		}
		s.describeMu.Lock()
		select {
		case <-job.partial:
		default:
			close(job.partial)
		}
		job.result = result
		s.describeMu.Unlock()
	}
	spec, err := s.describeSpec(ctx, key, query, publish)
	draftDuration = time.Since(started)
	result := core.DescriptiveResult{State: "failed"}
	if err == nil {
		stage = "candidates"
		candidatesStarted := time.Now()
		// The console's collection_draft uses Preview too: preserve its source
		// handling, rule evaluation, exclusions, ordering and spec limit.
		var preview *core.CollectionPreview
		preview, err = s.Preview(core.WithoutCatalogViewer(ctx), *spec, 200)
		if err == nil {
			result.Items = preview.Items
		}
		candidateDuration = time.Since(candidatesStarted)
		if err == nil {
			result.State, result.Title = "ready", spec.Name
			result.Spec = spec
			// Taste only orders rule-matching candidates; it cannot add titles.
			stage = "scoring"
			scoringStarted := time.Now()
			if sc := s.personalScorer(); spec.Sort == "personal" && sc != nil && user != nil && len(result.Items) > 1 {
				if scores, e := sc.ScoreTitles(ctx, user, result.Items); e == nil {
					slices.SortStableFunc(result.Items, func(a, b core.Title) int {
						x, y := scores[a.Key], scores[b.Key]
						if math.IsNaN(x) || math.IsInf(x, 0) {
							x = 0
						}
						if math.IsNaN(y) || math.IsInf(y, 0) {
							y = 0
						}
						if x > y {
							return -1
						}
						if x < y {
							return 1
						}
						return 0
					})
				}
			}
			scoringDuration = time.Since(scoringStarted)
		}
	}
	log := s.log
	if log == nil {
		log = slog.Default()
	}
	log.Debug("descriptive search timings", "draft_duration", draftDuration, "candidate_duration", candidateDuration, "scoring_duration", scoringDuration, "duration", time.Since(started), "candidates", len(result.Items))
	if err == nil {
		err = ctx.Err()
	}
	s.describeMu.Lock()
	superseded := job.superseded
	s.describeMu.Unlock()
	if err != nil {
		result.State = "failed"
		if !superseded {
			class := "upstream"
			var netErr net.Error
			switch {
			case errors.Is(err, context.DeadlineExceeded):
				class = "timeout"
			case errors.Is(err, context.Canceled):
				class = "cancelled"
			case errors.As(err, &netErr) && netErr.Timeout():
				class = "timeout"
			case stage == "candidates":
				class = "catalog"
			}
			log.Warn("descriptive search failed", "query_hash", fmt.Sprintf("%x", sha256.Sum256([]byte(key))), "stage", stage, "error_class", class, "duration", time.Since(started))
		}
	}
	s.describeMu.Lock()
	job.result, job.err, job.at = result, err, s.now()
	s.describeActive--
	close(job.done)
	s.describeMu.Unlock()
}

var _ core.DescriptiveSearchController = (*Service)(nil)
