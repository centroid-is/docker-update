// Package poll (continued). poller.go owns the cron scheduler that
// sweeps eligible containers per HMI_UPDATE_CRON tick. cronPoller is the
// SECOND producer of state mutations (Phase 2's docker events goroutine
// is the first); both feed the channel defined in channel.go.
//
// Architectural anchor (mirror of internal/docker/discovery.go's
// anti-deadlock invariant — see ARCHITECTURE.md lines 419-420):
//
//	cronPoller NEVER calls resolver.Digest from inside state.Store.Update's
//	closure. The sweep computes all digests in a bounded errgroup pool,
//	then sends one StateUpdate per container result on the channel. The
//	single-consumer goroutine (channel.go RunUpdater) is the only writer
//	to state.Store. Producers compute their I/O OUTSIDE the lock and
//	send pure-map-mutation closures.
//
// Phase-3-specific pitfalls (RESEARCH.md):
//
//   - Cron 5-field strictness: robfig/cron/v3's default parser is 5-field
//     (Minute Hour Dom Mon Dow). HMI_UPDATE_CRON "0 0 * * * *" (6 fields)
//     fails AddFunc; we surface a paste-ready error with both the literal
//     "invalid HMI_UPDATE_CRON" and "5-field" tokens so operators can
//     grep boot logs.
//   - errgroup.SetLimit ordering: SetLimit panics if called AFTER any
//     g.Go(f). The sweep calls SetLimit immediately after WithContext,
//     BEFORE the eligible-container loop. Verified by source-line order
//     in the acceptance criteria and by TestPoller_ErrgroupSetLimitBeforeGo's
//     peak-in-flight assertion.
//   - cron.Stop drain: cron.Stop returns a context whose Done channel
//     completes when in-flight tick functions finish. Run blocks on this
//     so a SIGTERM during a sweep waits for the sweep to flush its
//     StateUpdates before the channel goroutine drains and exits.
//
// Sweep ctx: the cron tick closure captures Run's ctx at AddFunc time so
// SIGTERM cancellation propagates into in-flight crane.Digest calls
// promptly (plan-check Warning 5). AddFunc therefore happens inside Run
// — NOT inside NewPoller — so each call to Run binds its own ctx.
// The cron-spec validation that fails fast at boot is performed
// separately by parsing once with cron.New + a throwaway AddFunc at
// construction time.
//
// DETECT-05 (cron tick triggers sweep) + DETECT-08 (tag-pattern filter)
// + DETECT-09 (pinned skip) + DETECT-10 (single-consumer channel) all
// close in this file combined with channel.go's RunUpdater.
package poll

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/robfig/cron/v3"
	"golang.org/x/sync/errgroup"

	"github.com/centroid-is/hmi-update/internal/registry"
	"github.com/centroid-is/hmi-update/internal/state"
)

// Default knobs (overridable via env). CONTEXT.md "Configuration Knobs":
const (
	defaultTimeoutSec  = 10
	defaultConcurrency = 4
)

// Poller is the public interface main.go binds. Plan-03-04 wiring
// constructs one via NewPoller and runs it via Run(ctx). Phase 1 declared
// this as an empty interface stub; plan 03-03 replaces it with the
// method-bearing contract below.
type Poller interface {
	Run(ctx context.Context) error
}

// storeReader is the narrow seam between cronPoller and its data source.
// The poller only needs Get to snapshot containers — writes always go
// through the StateUpdate channel and the consumer goroutine
// (channel.go RunUpdater). Production passes *state.Store concretely
// (which satisfies this interface); tests inject a safeStore wrapper
// for race-clean deep-copy semantics (poller_test.go safeStore).
//
// The interface is package-private — it is not a public extension
// point. Mirrors internal/docker.stateStore (PATTERNS.md Pattern A).
type storeReader interface {
	Get() state.State
}

// cronPoller owns the cron scheduler + the per-sweep errgroup. Mutations
// are sent to the channel; the consumer goroutine is owned by main.go
// (plan 03-04 wiring).
//
// Note: the live *cron.Cron instance is intentionally a local variable
// inside Run (not a struct field) so two concurrent Run invocations
// (or a stop-then-start race) cannot share the same scheduler pointer.
// CR-02: shared mutable cronInst pointer races on Start/Stop calls.
type cronPoller struct {
	spec        string
	store       storeReader
	resolver    registry.Resolver
	patterns    *Patterns
	updates     chan<- StateUpdate
	timeout     time.Duration
	concurrency int
}

// NewPoller constructs a cronPoller from HMI_UPDATE_CRON (cron expr),
// resolver, patterns cache, the state store, and the state-update
// channel. The timeout and concurrency knobs read from
// HMI_UPDATE_REGISTRY_TIMEOUT_S and HMI_UPDATE_POLL_CONCURRENCY
// respectively, with defaults 10s and 4 (CONTEXT.md Configuration Knobs).
//
// Cron parse errors fail fast HERE (boot time) with a paste-ready
// remediation pointing at the env var name. We achieve fail-fast by
// constructing a throwaway cron.Cron, AddFunc(spec, no-op), and
// returning the error message immediately; the live cron.Cron used by
// Run is constructed fresh in Run so each call binds its own ctx into
// the tick closure (plan-check Warning 5).
//
// cron.WithChain(cron.Recover) wraps the tick function so a panic in
// sweep does not kill the scheduler (RESEARCH.md Open Question #3).
//
// Returns the Poller interface (not *cronPoller) per the WR-04 pattern
// from internal/docker/moby.go.
func NewPoller(
	spec string,
	resolver registry.Resolver,
	patterns *Patterns,
	store *state.Store,
	updates chan<- StateUpdate,
) (Poller, error) {
	timeout := time.Duration(envInt("HMI_UPDATE_REGISTRY_TIMEOUT_S", defaultTimeoutSec)) * time.Second
	// envInt already filters out non-positive values (n > 0 guard); a
	// follow-up clamp here would be dead code. Pre-fix WR-01 had an
	// `if concurrency < 1` clamp that could never fire.
	concurrency := envInt("HMI_UPDATE_POLL_CONCURRENCY", defaultConcurrency)
	// *state.Store satisfies storeReader (its Get returns state.State).
	return newPoller(spec, resolver, patterns, store, updates, timeout, concurrency)
}

// newPollerForTest is the package-private constructor that accepts an
// explicit concurrency knob (avoids env-var coupling in tests). Mirrors
// the pattern of internal/docker/discovery.go's newDiscovererWithStore
// (test-only seam in a non-_test.go file because the tests live in the
// same package).
//
// PRODUCTION CALLERS MUST USE NewPoller — this exists only so the
// Phase-3 pitfall test (TestPoller_ErrgroupSetLimitBeforeGo) can pin
// concurrency = 4 deterministically without t.Setenv side effects.
func newPollerForTest(
	spec string,
	resolver registry.Resolver,
	patterns *Patterns,
	store storeReader,
	updates chan<- StateUpdate,
	concurrency int,
) (Poller, error) {
	timeout := time.Duration(defaultTimeoutSec) * time.Second
	return newPoller(spec, resolver, patterns, store, updates, timeout, concurrency)
}

// newPoller is the shared body for the public + test constructors. It
// validates the cron spec at boot via a throwaway cron.AddFunc; the live
// cron.Cron is constructed fresh in Run so each Run binds its own ctx
// into the tick closure (plan-check Warning 5).
func newPoller(
	spec string,
	resolver registry.Resolver,
	patterns *Patterns,
	store storeReader,
	updates chan<- StateUpdate,
	timeout time.Duration,
	concurrency int,
) (Poller, error) {
	// Fail-fast spec validation. The throwaway cron is discarded; the
	// real scheduler is constructed in Run with the same options but a
	// fresh AddFunc that binds Run's ctx into the tick closure.
	probe := cron.New(
		cron.WithLocation(time.UTC),
		cron.WithChain(cron.Recover(cronSlogAdapter{})),
	)
	if _, err := probe.AddFunc(spec, func() {}); err != nil {
		return nil, fmt.Errorf(
			"invalid HMI_UPDATE_CRON %q: %w (expected 5-field cron expression like '0 * * * *' or '@every 5s')",
			spec, err)
	}

	p := &cronPoller{
		spec:        spec,
		store:       store,
		resolver:    resolver,
		patterns:    patterns,
		updates:     updates,
		timeout:     timeout,
		concurrency: concurrency,
	}
	slog.Info("poll.boot.start",
		"cron_expr", spec,
		"timeout_ms", timeout.Milliseconds(),
		"concurrency", concurrency)
	return p, nil
}

// Run constructs the live cron scheduler, registers the tick function
// capturing this Run invocation's ctx (so SIGTERM unblocks in-flight
// resolver calls promptly — plan-check Warning 5), starts the
// scheduler, blocks on ctx.Done, then drains in-flight ticks via
// cron.Stop().Done() before returning.
//
// The cron spec was validated at NewPoller construction time; AddFunc
// here cannot return a parse error (the spec is verbatim what already
// parsed cleanly above). A defensive error wrap is included anyway in
// case of a future API change.
func (p *cronPoller) Run(ctx context.Context) error {
	// Live cron.Cron instance is local to this Run invocation. Two
	// concurrent Run calls (or a stop-then-start race) would otherwise
	// share state via a struct field (CR-02). Local scope removes the
	// shared mutable pointer entirely.
	c := cron.New(
		cron.WithLocation(time.UTC),
		cron.WithChain(cron.Recover(cronSlogAdapter{})),
	)
	if _, err := c.AddFunc(p.spec, func() {
		p.sweep(ctx)
	}); err != nil {
		// Spec already validated at NewPoller — surface defensively
		// with the same paste-ready remediation if somehow we land here.
		return fmt.Errorf(
			"invalid HMI_UPDATE_CRON %q: %w (expected 5-field cron expression like '0 * * * *' or '@every 5s')",
			p.spec, err)
	}
	c.Start()
	<-ctx.Done()
	// Drain in-flight ticks before returning. cron.Stop returns a ctx
	// that completes when in-flight job funcs finish (RESEARCH.md
	// Phase-3 pitfall: cron.Stop() not awaited).
	<-c.Stop().Done()
	return ctx.Err()
}

// ----------------------------------------------------------------------
// sweep — the cron tick body.
// ----------------------------------------------------------------------

// sweep is invoked once per cron tick. Snapshots state.Store.Get,
// filters to eligible containers, applies the tag-pattern filter,
// fetches digests through a bounded errgroup, and sends one
// StateUpdate per result on the channel.
//
// ctx is the ctx captured at Run's AddFunc time; SIGTERM via the root
// ctx cancels in-flight crane.Digest calls promptly. Per-call timeouts
// layered atop via context.WithTimeout.
func (p *cronPoller) sweep(ctx context.Context) {
	sweepStart := time.Now()
	p.send(ctx, StateUpdate{
		Kind: KindPollSweepStart,
		Apply: func(st *state.State) {
			st.LastPollStart = sweepStart
			st.LastPollError = ""
		},
	})

	snapshot := p.store.Get()
	eligible := p.eligibleContainers(ctx, snapshot.Containers)

	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(p.concurrency) // MUST be before any g.Go (Phase-3 pitfall)

	for _, c := range eligible {
		c := c // shadow for closure
		ref := p.refForContainer(c)
		if ref == "" {
			// Tag-pattern excludes the running tag (DETECT-08 misconfig
			// branch). Surface a Note and skip the fetch.
			p.sendTagMismatch(gctx, c)
			continue
		}
		g.Go(func() error {
			callCtx, cancel := context.WithTimeout(gctx, p.timeout)
			defer cancel()
			t0 := time.Now()
			digest, err := p.resolver.Digest(callCtx, ref)
			elapsed := time.Since(t0)
			p.handleFetchResult(gctx, c, ref, digest, err, elapsed)
			return nil // never fail-fast; per-container errors do not abort the sweep
		})
	}
	_ = g.Wait()

	sweepEnd := time.Now()
	p.send(ctx, StateUpdate{
		Kind: KindPollSweepEnd,
		Apply: func(st *state.State) {
			st.LastPollEnd = sweepEnd
		},
	})
	slog.Info("poll.sweep.end",
		"elapsed_ms", sweepEnd.Sub(sweepStart).Milliseconds(),
		"polled", len(eligible))
}

// eligibleContainers filters to containers that should be polled this
// tick: not pinned (DETECT-09), not stopped, have a non-empty Image.
// Pinned containers also receive a pinned-opt-out Notes update so the
// Phase 5 UI can render the badge tooltip (canonical string lives at
// the single assignment site in sendPinnedNote below).
func (p *cronPoller) eligibleContainers(ctx context.Context, in map[string]state.Container) []state.Container {
	out := make([]state.Container, 0, len(in))
	for _, c := range in {
		if c.Pinned {
			p.sendPinnedNote(ctx, c)
			continue
		}
		if c.Stopped {
			continue
		}
		if c.Image == "" {
			continue
		}
		out = append(out, c)
	}
	return out
}

// refForContainer computes the image:tag ref the resolver should fetch.
// If a tag-pattern is registered for this service and the running tag
// does NOT match, returns "" — sweep treats this as "skip + surface
// note" (DETECT-08 operator-misconfig branch). Empty Tag defaults to
// "latest" (parity with internal/docker/discovery.go parseImageRef).
func (p *cronPoller) refForContainer(c state.Container) string {
	tag := c.Tag
	if tag == "" {
		tag = "latest"
	}
	if !p.patterns.Match(c.Service, tag) {
		return ""
	}
	return c.Image + ":" + tag
}

// handleFetchResult builds and sends a KindDigestResolved StateUpdate
// reflecting the resolver call's outcome. Errors are classified via
// errors.Is(err, registry.ErrPermanent) into the canonical Notes
// strings; success path computes UpdateAvailable from CurrentDigest vs
// upstream digest.
func (p *cronPoller) handleFetchResult(ctx context.Context, c state.Container, ref, digest string, err error, elapsed time.Duration) {
	if err != nil {
		errClass := "transient"
		if errors.Is(err, registry.ErrPermanent) {
			errClass = "permanent"
		}
		slog.Warn("registry.fetch.error",
			"service", c.Service, "ref", ref,
			"err_class", errClass, "err", err,
			"elapsed_ms", elapsed.Milliseconds())
		p.sendFetchError(ctx, c.Service, errClass)
		return
	}
	slog.Info("registry.fetch",
		"service", c.Service, "ref", ref,
		"digest", digest, "elapsed_ms", elapsed.Milliseconds(), "status", "ok")
	now := time.Now()
	svc := c.Service
	resolvedDigest := digest
	p.send(ctx, StateUpdate{
		Kind:    KindDigestResolved,
		Service: svc,
		Apply: func(st *state.State) {
			cur := st.Containers[svc]
			priorAvailable := cur.AvailableDigest
			cur.AvailableDigest = resolvedDigest
			cur.LastPolledAt = now
			// UpdateAvailable flip rules:
			//   1. If CurrentDigest is known (Phase 4+ docker.Discoverer
			//      will populate this from ContainerInspect's image
			//      manifest descriptor), flip when CurrentDigest differs
			//      from the resolved upstream digest. This is the
			//      deployed-vs-upstream comparison and is the canonical
			//      DETECT-07 semantic.
			//   2. If CurrentDigest is unknown (current Phase 3:
			//      Discoverer does not yet populate it — DEPLOY-?? is a
			//      Phase 4 task), fall back to comparing against the
			//      PRIOR AvailableDigest. First tick: prior is "",
			//      resolved is X; no flip (we have nothing to compare
			//      yet). Second tick: prior is X, resolved is Y; flip.
			//      This is how Plan 03-05 e2e detect-multiarch and
			//      detect-tag-pattern assert flip-on-fresh-push without
			//      requiring CurrentDigest to be seeded.
			// Rule 1 takes precedence when applicable; Rule 2 is the
			// fallback for the unknown-CurrentDigest case.
			switch {
			case cur.CurrentDigest != "":
				cur.UpdateAvailable = cur.CurrentDigest != resolvedDigest
			case priorAvailable != "" && priorAvailable != resolvedDigest:
				cur.UpdateAvailable = true
			}
			cur.Notes = clearStaleErrorNotes(cur.Notes)
			st.Containers[svc] = cur
		},
	})
}

// Canonical Notes strings (each appears at exactly ONE quoted
// assignment site below — accepted by the source-grep acceptance
// criteria in the plan). CONTEXT.md Area 3 surface:
//
//   - notePinnedOptOut  — DETECT-09: container has @sha256: pin
//   - noteTagMismatch   — DETECT-08: running tag fails the pattern regex
//   - noteRegistryPrefix + class + noteRegistrySuffix — fetch error class
//
// PERSISTENT NOTE MIRROR (Plan 03-05 e2e wiring fix):
//
//   noteInvalidTagPatternMirror is a LOCAL mirror of the canonical
//   constant in internal/docker.noteInvalidTagPattern. The poller
//   cannot import the docker package (circular: docker imports poll
//   for StateUpdate), so the literal is duplicated here under a
//   distinguished symbol. Both packages reference the same physical
//   string. If one changes, the other MUST be updated in lockstep.
//
//   This literal exists only to power sendFetchError's persistent-note
//   preservation — when the cron sweep's fetch fails for a container
//   whose Notes already say "invalid tag-pattern label, ignored",
//   the existing Note must be preserved (NOT overwritten with a
//   transient registry-error string). Same rule for notePinnedOptOut.
//
//   ALTERNATIVE consideration: extract the literal to a third
//   package (internal/state.NoteInvalidTagPattern) shared by docker +
//   poll. Deferred — adding a third package for a 47-character
//   string feels heavier than the documented duplication-with-mirror
//   pattern. Revisit if a Phase 4 producer ever needs the same
//   literal.
const (
	notePinnedOptOut            = "pinned: opt-out"
	noteTagMismatch             = "running tag does not match tag-pattern label"
	noteRegistryPrefix          = "registry error: "
	noteRegistrySuffix          = " (check image ref)"
	noteInvalidTagPatternMirror = "invalid tag-pattern label, ignored"
)

// clearStaleErrorNotes drops any prior registry-error or
// running-tag-mismatch note when the current fetch succeeds. Returns
// the input unchanged otherwise (pinned-opt-out and
// invalid-tag-pattern notes persist independent of fetch results —
// those reflect static container properties).
func clearStaleErrorNotes(n string) string {
	if n == noteTagMismatch {
		return ""
	}
	if strings.HasPrefix(n, noteRegistryPrefix) {
		return ""
	}
	return n
}

// sendPinnedNote / sendTagMismatch / sendFetchError build small
// StateUpdate closures that set a single short Note string per
// CONTEXT.md Area 3.
func (p *cronPoller) sendPinnedNote(ctx context.Context, c state.Container) {
	service := c.Service
	p.send(ctx, StateUpdate{
		Kind:    KindDigestResolved,
		Service: service,
		Apply: func(st *state.State) {
			cur := st.Containers[service]
			cur.Notes = notePinnedOptOut
			st.Containers[service] = cur
		},
	})
}

func (p *cronPoller) sendTagMismatch(ctx context.Context, c state.Container) {
	service := c.Service
	p.send(ctx, StateUpdate{
		Kind:    KindDigestResolved,
		Service: service,
		Apply: func(st *state.State) {
			cur := st.Containers[service]
			cur.Notes = noteTagMismatch
			st.Containers[service] = cur
		},
	})
}

func (p *cronPoller) sendFetchError(ctx context.Context, service, errClass string) {
	p.send(ctx, StateUpdate{
		Kind:    KindDigestResolved,
		Service: service,
		Apply: func(st *state.State) {
			c := st.Containers[service]
			// Preserve PERSISTENT notes — these reflect static
			// container properties (pinned-by-digest; misconfigured
			// tag-pattern label) that a transient registry error
			// MUST NOT shadow. clearStaleErrorNotes's doc comment
			// promises this invariant; this is its symmetric
			// enforcement on the error path. Plan 03-05 e2e wiring
			// fix.
			if c.Notes == notePinnedOptOut || c.Notes == noteInvalidTagPatternMirror {
				return
			}
			c.Notes = noteRegistryPrefix + errClass + noteRegistrySuffix
			st.Containers[service] = c
		},
	})
}

// send wraps the channel send so future back-pressure / metrics hooks
// can land here without changing every call site. The select on
// ctx.Done() makes the send ctx-aware so SIGTERM during a sweep mid
// fan-out does not block forever on a saturated channel whose
// consumer has already exited (CR-01).
func (p *cronPoller) send(ctx context.Context, u StateUpdate) {
	select {
	case p.updates <- u:
	case <-ctx.Done():
	}
}

// envInt reads an int from the named env var, falling back to def if
// missing, unparseable, or <= 0. Mirrors the convention from
// cmd/hmi-update/main.go's HMI_UPDATE_LOG_LEVEL parsing.
func envInt(name string, def int) int {
	if v := os.Getenv(name); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return def
}

// cronSlogAdapter routes cron.Recover panic messages to slog. Implements
// the cron.Logger interface (Info + Error). RESEARCH.md Open Question #3
// verified that cron.WithChain(cron.Recover(logger)) wraps every tick
// function so a panic in sweep does not kill the scheduler.
type cronSlogAdapter struct{}

func (cronSlogAdapter) Info(msg string, keysAndValues ...interface{}) {
	slog.Info(msg, keysAndValues...)
}

func (cronSlogAdapter) Error(err error, msg string, keysAndValues ...interface{}) {
	slog.Error(msg, append(keysAndValues, "err", err)...)
}
