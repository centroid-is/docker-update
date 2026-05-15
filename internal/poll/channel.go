// Package poll (continued). channel.go owns the StateUpdate message type
// and the single-consumer goroutine RunUpdater.
//
// Architectural anchor (mirror of internal/docker/discovery.go's
// anti-deadlock invariant — see ARCHITECTURE.md lines 419-420):
//
//	Three producers feed a single buffered channel of StateUpdate messages:
//	  - Producer A: Phase 2's docker events goroutine (internal/docker.Discoverer)
//	  - Producer B: Phase 3's poll-tick goroutine (internal/poll.cronPoller)
//	  - (Phase 4 plan 04-03 adds the third: actions.actionOrchestrator
//	    producing KindActionStart / KindActionProgress / KindActionResult.)
//
//	The SOLE consumer is RunUpdater, which applies each message via
//	state.Store.Update. The store's RWMutex is taken INSIDE Update, never
//	around channel sends. Producers compute their I/O results first
//	(registry fetch, docker inspect) then construct a pure-map-mutation
//	Apply closure and send it. The consumer's job inside the lock is a
//	single map mutation — bounded latency, no fan-out blocking.
//
// On ctx cancellation, RunUpdater drains pending messages from the channel
// before returning. This is the load-bearing invariant for graceful
// shutdown (Phase 4's STATE-04 SIGKILL-resistance work). Without the
// drain, a producer that already wrote a message into the buffer would
// see its mutation silently dropped during shutdown.
//
// DETECT-10 acceptance criterion is satisfied by this file's single-
// consumer design plus the race-detector run in channel_test.go.
//
// Phase 3 plan 03-04 wires RunUpdater into cmd/hmi-update/main.go as one
// of the long-lived goroutines spawned at boot, joined via WaitGroup at
// shutdown.
package poll

import (
	"context"
	"log/slog"

	"github.com/centroid-is/hmi-update/internal/state"
)

// UpdateKind discriminates the producer source so consumers (and the
// slog event stream) can attribute mutations to a cause. Used for
// observability and (Phase 4) for slow-consumer / back-pressure
// diagnostics.
type UpdateKind int

const (
	// KindDigestResolved is sent by the poll-tick worker after a
	// resolver.Digest call (success OR classified error — a Notes-only
	// update for permanent errors still uses this Kind so the consumer
	// has a single dispatch point per container).
	KindDigestResolved UpdateKind = iota

	// KindContainerEvent is sent by the docker-events goroutine on
	// start / die / destroy.
	KindContainerEvent

	// KindPollSweepStart is sent once per cron tick, before fan-out,
	// carrying the LastPollStart timestamp.
	KindPollSweepStart

	// KindPollSweepEnd is sent once per cron tick, after all errgroup
	// workers return, carrying the LastPollEnd timestamp.
	KindPollSweepEnd

	// Phase 4 — action lifecycle (orchestrator producer). The actions
	// package's orchestrator (internal/actions/orchestrator.go, lands in
	// Plan 04-03) is the THIRD producer of state mutations; the docker
	// events goroutine (Phase 2) and the cron poller (Phase 3) are the
	// first two. All three feed this same channel. T-04-01-03: these
	// constants are APPENDED (never inserted), so the integer iota
	// values of pre-existing Kinds (0..3) remain stable.

	// KindActionStart marks the orchestrator entering an action body.
	// Apply closure sets state.Container.ActionInFlight to one of
	// "updating", "rolling_back", "force_pulling".
	KindActionStart

	// KindActionProgress carries an intermediate phase (pulled,
	// recreated). Apply closure is currently a no-op on state
	// (reserved for Phase 5 UI breadcrumbs); included for observability
	// symmetry with start+result.
	KindActionProgress

	// KindActionResult marks the orchestrator completing or aborting
	// an action. Apply closure clears ActionInFlight, sets
	// CurrentDigest/PreviousDigest on success, sets ActionError on
	// failure.
	KindActionResult
)

// StateUpdate is the message type exchanged on the single state-update
// channel. Service is empty for the two sweep-level kinds. Apply is the
// pure-map-mutation closure the consumer invokes inside state.Store.Update.
//
// The Apply func receives the state.State pointer that state.Store.Update
// passes to its closure; mutations are persisted by Store.Update via
// renameio (Phase 1 plan 01-02 atomic write).
//
// Apply MUST NOT block on I/O (registry fetch, docker inspect, etc.) —
// it runs under state.Store's write lock and would stall every reader of
// state for the duration of the I/O. The anti-deadlock invariant in this
// package's doc comment is the formal statement of this rule.
type StateUpdate struct {
	Kind    UpdateKind
	Service string
	Apply   func(*state.State)
}

// storeUpdater is the narrow seam between channel.go and *state.Store.
// Production callers pass *state.Store concretely via RunUpdater; tests
// substitute a recording wrapper via the package-private runUpdater so
// the error-from-store path is exercisable without a real store.
type storeUpdater interface {
	Update(fn func(*state.State)) error
}

// RunUpdater is the SINGLE consumer goroutine for the state-update
// channel. Range-receives messages, invokes store.Update(msg.Apply),
// logs persistence errors as slog.Error "poll.consumer.persist".
//
// On ctx.Done(), drains pending messages before exiting (graceful
// shutdown). This drain is the load-bearing invariant for Phase 4's
// SIGKILL-resistance work.
//
// Production wiring: cmd/hmi-update/main.go (plan 03-04) spawns this as
// a goroutine and joins via WaitGroup on shutdown.
func RunUpdater(ctx context.Context, ch <-chan StateUpdate, store *state.Store) {
	runUpdater(ctx, ch, store)
}

// runUpdater is the package-private form used by tests that inject a
// storeUpdater interface. Production callers use RunUpdater which
// receives *state.Store concretely. The two forms differ only in the
// store parameter's static type — runtime behaviour is identical.
func runUpdater(ctx context.Context, ch <-chan StateUpdate, store storeUpdater) {
	for {
		select {
		case <-ctx.Done():
			// Drain pending messages before exit (graceful drain).
			// Inner non-blocking select pulls until the channel is
			// drained, then default-falls out to return.
			for {
				select {
				case msg := <-ch:
					if err := store.Update(msg.Apply); err != nil {
						slog.Error("poll.consumer.persist",
							"service", msg.Service, "err", err)
					}
				default:
					return
				}
			}
		case msg := <-ch:
			if err := store.Update(msg.Apply); err != nil {
				slog.Error("poll.consumer.persist",
					"service", msg.Service, "err", err)
			}
		}
	}
}
