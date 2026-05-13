// Package docker (continued). discovery.go owns the single producer
// goroutine that turns Docker daemon events into state.Store mutations.
//
// Architectural anchor (see .planning/research/ARCHITECTURE.md Pattern 3
// "single-consumer channel for state mutations" + lines 400-422):
//
//   Discoverer is the FIRST producer of container-related state mutations
//   in Phase 2. Phase 3's cron poller becomes the second producer (registry
//   digest checks); both producers feed state through state.Store.Update,
//   which is the single mutation surface that serializes writes under
//   state.Store.mu and writes through to disk via persist().
//
// Anti-deadlock invariant (ARCHITECTURE.md lines 419-420 — "Never hold
// state.Store.mu while calling registry/docker/compose"):
//
//   Discoverer NEVER calls dockerClient.ContainerInspect from inside
//   state.Store.Update's closure. Inspect FIRST, then call Update with
//   the resolved fields. The closure is a pure map-mutation function.
//   Violating this would stall every reader of state for the duration of
//   the inspect HTTP round-trip — a wedge under heavy poll load (Phase 5
//   UI polls /api/state every 5s).
//
// TestDiscoverer_InspectPrecedesUpdate (discovery_test.go) directly verifies
// the invariant by instrumenting call ordering. Do not move ContainerInspect
// into the Update closure as a "simplification" — the test will fail at the
// call site, not at the downstream consequence.
package docker

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/centroid-is/hmi-update/internal/state"
	"github.com/moby/moby/api/types/events"
)

// Discoverer is the single producer of container-related state mutations in
// Phase 2. It owns one boot ContainerList call and one long-running Events
// subscription with reconnect logic.
//
// Phase 3's cron poller will be the second producer; both feed state through
// the same state.Store.Update surface. Today there is no channel between them
// because there is only one producer — the channel materializes in Phase 3
// if/when poller-vs-discovery interleaving needs a serialization seam.
//
// Discoverer is safe to construct but its Run method must be invoked from a
// single goroutine (it is itself the consumer; calling Run from two
// goroutines would produce two parallel boot lists and two events
// subscriptions, neither of which is the intended behaviour).
// stateStore is the narrow seam between Discoverer and *state.Store. It
// holds the two methods (Get and Update) the discovery code actually calls.
// Production callers pass *state.Store (which satisfies this interface);
// TestDiscoverer_InspectPrecedesUpdate substitutes a recording wrapper so
// the test can observe the moment Update's closure runs without polling
// the underlying map (which would race against state.Store.Update's
// in-closure writes per the race detector).
//
// The interface is package-private — it is not a Phase 3 extension point.
type stateStore interface {
	Get() state.State
	Update(fn func(*state.State)) error
}

type Discoverer struct {
	// client is the package-local Client interface from client.go (plan
	// 02-01). Bare type name because we live in the same package.
	client Client
	store  stateStore

	// sleeper is the back-off sleep function. Default is a context-aware
	// sleep that wakes early on ctx cancellation (WR-02). Tests substitute
	// a recording sleeper so the reconnect-backoff test runs without
	// actually sleeping 1+2+4+...=63 real seconds.
	//
	// The ctx parameter is honoured by the default implementation:
	//   select { case <-ctx.Done(): case <-time.After(d): }
	// — a shutdown signal therefore unblocks the loop within microseconds
	// instead of waiting out a 30s back-off cap.
	sleeper func(ctx context.Context, d time.Duration)

	// maxBackoff caps the exponential progression. CONTEXT.md
	// <specifics>: "up to 30s." Exposed as a field for future operator
	// tuning + deterministic tests.
	maxBackoff time.Duration

	// backoffBase is the first sleep after the first failure. CONTEXT.md
	// says 1s.
	backoffBase time.Duration
}

// ctxAwareSleep is the production default for Discoverer.sleeper. It blocks
// until d elapses OR ctx is cancelled — whichever happens first (WR-02).
// Returning early on ctx.Done() lets the events loop's top-of-iteration
// ctx.Err() check exit cleanly during a 30s back-off, instead of stalling
// shutdown for up to 30 seconds per reconnect attempt.
func ctxAwareSleep(ctx context.Context, d time.Duration) {
	if d <= 0 {
		return
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}

// NewDiscoverer constructs a Discoverer wired to the supplied client and
// store. Default sleeper is time.Sleep; default backoff is 1s base, 30s cap
// (per CONTEXT.md <specifics>).
//
// The store parameter is *state.Store at the production call site; the
// signature uses the local stateStore interface so the
// inspect-precedes-update test can inject a recording wrapper. The
// interface narrows to the two methods Discoverer actually invokes — no
// other extension point.
func NewDiscoverer(client Client, store *state.Store) *Discoverer {
	return &Discoverer{
		client:      client,
		store:       store,
		sleeper:     ctxAwareSleep,
		maxBackoff:  30 * time.Second,
		backoffBase: 1 * time.Second,
	}
}

// newDiscovererWithStore is the test-only constructor that accepts any
// stateStore implementation (e.g. a recording wrapper used by
// TestDiscoverer_InspectPrecedesUpdate). Production callers MUST use
// NewDiscoverer; this exists in a non-_test.go file because the test lives
// in the same package, not the external _test package.
func newDiscovererWithStore(client Client, store stateStore) *Discoverer {
	return &Discoverer{
		client:      client,
		store:       store,
		sleeper:     ctxAwareSleep,
		maxBackoff:  30 * time.Second,
		backoffBase: 1 * time.Second,
	}
}

// SetSleeperForTest swaps the sleeper used by the reconnect loop.
//
// PRODUCTION CALLERS MUST NOT USE THIS — it exists only for the
// reconnect-backoff test in discovery_test.go. The alternative (passing the
// sleeper into the constructor) would surface a test-only concern in every
// caller's main() wiring; this hook stays out of the constructor signature.
//
// The injected sleeper receives the events loop's ctx so test sleepers can
// honour cancellation if they choose; the recording-only test sleeper used
// by TestDiscoverer_ReconnectBackoff ignores ctx because it returns
// immediately after appending to its slice.
func (d *Discoverer) SetSleeperForTest(s func(context.Context, time.Duration)) { d.sleeper = s }

// Run executes the boot ContainerList once, then enters the events
// subscription loop with reconnect. Run returns when ctx is cancelled.
// Errors from individual events are logged and recovered; only ctx
// cancellation terminates Run.
//
// Plan 02-04's main.go wiring spawns Run in a goroutine; the HTTP server
// comes up immediately, so an early /api/state poll may return an empty
// containers map. Acceptable per CONTEXT.md <specifics>: "the actual
// target on a warm machine is <2s; the 60s SLA is generous on purpose."
func (d *Discoverer) Run(ctx context.Context) error {
	slog.Info("discovery.boot.start", "label_filter", "hmi-update.watch=true")
	if err := d.bootList(ctx); err != nil {
		// Log but do not exit — the event loop may still produce valid
		// state mutations once the daemon comes back. /healthz is the
		// authoritative liveness signal (plan 02-04).
		slog.Error("discovery.boot.fail", "err", err)
	}
	return d.eventsLoop(ctx)
}

// bootList runs a single ContainerList with the hmi-update.watch=true
// filter and seeds state.Containers. Each container is inspected via
// upsertFromInspect — the same code path the start-event handler takes —
// so a container that appears in boot AND in a near-simultaneous event
// burst is handled identically (idempotent upsert).
func (d *Discoverer) bootList(ctx context.Context) error {
	opts := ContainerListOptions{
		// Filters is map[string]map[string]bool (see _sdk_shape.txt).
		// The Filters.Add helper takes (term, values...) and returns a
		// copy, but on a zero-value Filters that copy is the only path
		// to mutate it (zero map). We construct directly here for
		// clarity — both forms produce the same wire shape.
		Filters: Filters{
			"label": {"hmi-update.watch=true": true},
		},
	}
	containers, err := d.client.ContainerList(ctx, opts)
	if err != nil {
		return err
	}
	slog.Info("discovery.boot.list", "count", len(containers))
	for _, c := range containers {
		d.upsertFromInspect(ctx, c.ID)
	}
	return nil
}

// eventsLoop subscribes to docker events; on stream error or daemon
// disconnect, reconnects with exponential backoff capped at maxBackoff.
// After a successful reconnect, re-runs the boot list to catch any state
// changes that occurred while disconnected (CONTEXT.md <specifics>).
func (d *Discoverer) eventsLoop(ctx context.Context) error {
	attempt := 0
	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		opts := EventsListOptions{
			// Filter to type=container + the three event names we
			// handle. Other event types (image pulls, network attach,
			// etc.) are dropped at the SDK layer and never reach
			// handleEvent — saving a switch-default no-op per event.
			Filters: Filters{
				"type":  {string(events.ContainerEventType): true},
				"event": {"start": true, "die": true, "destroy": true},
			},
		}

		// If this is a reconnect attempt (attempt > 0 from the previous
		// loop iteration's failure path), re-run the boot list BEFORE
		// re-subscribing so state catches up to whatever the daemon did
		// while the stream was down. CONTEXT.md <specifics> requires
		// the boot list re-run on every successful reconnect.
		//
		// Note: "successful reconnect" here is "we are about to call
		// Events() for attempt N>0." The next call may itself fail
		// instantly (see TestDiscoverer_ReconnectBackoff which scripts
		// repeated immediate failures); if so, attempt will keep
		// climbing on the failure path below without being reset.
		if attempt > 0 {
			slog.Info("discovery.events.reconnected", "attempt", attempt)
			if err := d.bootList(ctx); err != nil {
				slog.Error("discovery.reconnect.boot.fail", "err", err)
			}
		}

		eventCh, errCh := d.client.Events(ctx, opts)

		// Drain events until eventCh or errCh closes, or ctx done.
		//
		// WR-01: if drainEvents observed at least one real event before
		// the stream failed, the subscription was genuinely stable —
		// not an immediate-failure reconnect spin. Reset `attempt` so a
		// later disconnect after a long stable run starts the backoff
		// progression from 1s again, instead of inheriting the climbing
		// counter from a failure cluster hours ago. The pre-fix
		// behaviour kept `attempt` monotonically increasing for the
		// life of the process — see SUMMARY 02-03 for the regression
		// rationale.
		//
		// We still DO NOT reset `attempt` on a 0-event drain — the moby
		// SDK's Events returns the channel pair synchronously even on a
		// failed subscription (the error fires on errCh shortly after).
		// Resetting unconditionally would defeat the exponential
		// backoff: every iteration's `attempt++` below would compute
		// from 1, capping the progression at 1s forever.
		eventsHandled, drained := d.drainEvents(ctx, eventCh, errCh)

		if err := ctx.Err(); err != nil {
			return err
		}

		if eventsHandled > 0 {
			attempt = 0
		}

		attempt++
		backoff := d.computeBackoff(attempt)
		slog.Warn("discovery.events.reconnect",
			"attempt", attempt,
			"backoff_ms", backoff.Milliseconds(),
			"drain_reason", drained,
			"events_handled", eventsHandled)
		d.sleeper(ctx, backoff)
	}
}

// computeBackoff returns backoffBase * 2^(attempt-1), capped at maxBackoff.
// attempt is 1-indexed; the first failure sleeps backoffBase.
//
// Overflow note: a uint64 of nanoseconds overflows after ~30 attempts; the
// safeMaxAttempt guard returns maxBackoff before the shift wraps. In
// practice attempts never exceed ~10 because the SDK reconnects fast on
// daemon-restart and a sustained-failure scenario plateaus at the cap.
func (d *Discoverer) computeBackoff(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	const safeMaxAttempt = 30
	if attempt > safeMaxAttempt {
		return d.maxBackoff
	}
	b := d.backoffBase << (attempt - 1)
	if b > d.maxBackoff || b <= 0 {
		return d.maxBackoff
	}
	return b
}

// drainEvents consumes events until the stream closes or errors. Returns
// the number of events successfully handled in this subscription window
// plus a string describing why the drain ended (for slog).
//
// The eventsHandled count drives the WR-01 backoff reset: callers use
// `eventsHandled > 0` as the signal that the subscription was stable
// (real events arrived) rather than an immediate-failure reconnect spin.
func (d *Discoverer) drainEvents(ctx context.Context, eventCh <-chan EventMessage, errCh <-chan error) (eventsHandled int, reason string) {
	for {
		select {
		case <-ctx.Done():
			return eventsHandled, "ctx-cancelled"
		case err, ok := <-errCh:
			if !ok {
				return eventsHandled, "errch-closed"
			}
			if err != nil {
				slog.Warn("discovery.events.stream.err", "err", err)
				return eventsHandled, "stream-err"
			}
		case ev, ok := <-eventCh:
			if !ok {
				return eventsHandled, "eventch-closed"
			}
			d.handleEvent(ctx, ev)
			eventsHandled++
		}
	}
}

// handleEvent dispatches a single event message. The SDK's EventMessage
// (= events.Message) carries Action and Actor.ID — see _sdk_shape.txt
// "go doc github.com/moby/moby/api/types/events Message".
func (d *Discoverer) handleEvent(ctx context.Context, ev EventMessage) {
	action := ev.Action
	id := ev.Actor.ID
	slog.Info("discovery.event.received",
		"action", string(action),
		"container_id", shortID(id))
	switch action {
	case events.ActionStart:
		d.upsertFromInspect(ctx, id)
	case events.ActionDie:
		d.markStopped(ctx, id)
	case events.ActionDestroy:
		d.removeContainer(ctx, id)
	default:
		// Defensive: the SDK-level filter should mean we never see
		// other actions, but if a future SDK delivers extras we drop
		// them silently rather than echo them into state.
	}
}

// upsertFromInspect calls ContainerInspect THEN state.Store.Update.
//
// THE INSPECT HAPPENS OUTSIDE THE STORE LOCK — anti-deadlock invariant
// from ARCHITECTURE.md lines 419-420. DO NOT inline the inspect into the
// Update closure: that closure runs under state.Store.mu (write mode) and
// any blocking call inside it stalls every reader.
//
// TestDiscoverer_InspectPrecedesUpdate (discovery_test.go) instruments the
// call ordering directly. If a future regression moves inspect into the
// closure, the test fails at the call site.
func (d *Discoverer) upsertFromInspect(ctx context.Context, id string) {
	if id == "" {
		return
	}
	insp, err := d.client.ContainerInspect(ctx, id)
	if err != nil {
		slog.Error("discovery.inspect.fail",
			"container_id", shortID(id),
			"err", err)
		return
	}

	cfg := insp.Container.Config
	if cfg == nil {
		// Defensive: the SDK populates Config on a successful inspect,
		// but a malformed daemon response could leave it nil — skip.
		slog.Warn("discovery.inspect.no-config", "container_id", shortID(id))
		return
	}

	svc := cfg.Labels[composeServiceLabel]
	if svc == "" {
		// No compose service label — not a watched-via-compose
		// container. Silently ignore (the daemon may emit events for
		// any container; we only care about compose-managed ones).
		return
	}

	imageRef := cfg.Image
	img, tag := parseImageRef(imageRef)
	pinned := strings.Contains(imageRef, "@sha256:")
	filteredLabels := filterHmiLabels(cfg.Labels)

	if err := d.store.Update(func(st *state.State) {
		c := st.Containers[svc]
		c.Service = svc
		c.Image = img
		c.Tag = tag
		c.ContainerID = shortID(id)
		c.Labels = filteredLabels
		c.Pinned = pinned
		c.Stopped = false // we just saw a start (or boot) — clear any prior die marker
		st.Containers[svc] = c
	}); err != nil {
		slog.Error("discovery.event.start.persist", "service", svc, "err", err)
	}
}

// markStopped sets Container.Stopped = true while preserving every other
// field. Phase 5's status badge consumes this; nothing in Phase 2 changes
// behaviour for stopped rows. Phase 3's poll loop will skip them.
func (d *Discoverer) markStopped(ctx context.Context, id string) {
	svc := d.serviceForContainerID(id)
	if svc == "" {
		return
	}
	if err := d.store.Update(func(st *state.State) {
		c, ok := st.Containers[svc]
		if !ok {
			return
		}
		c.Stopped = true
		st.Containers[svc] = c
	}); err != nil {
		slog.Error("discovery.event.die.persist", "service", svc, "err", err)
	}
}

// removeContainer deletes the row entirely. If the container reappears
// under the same service name later (e.g. compose recreate), a fresh start
// event will repopulate via upsertFromInspect.
func (d *Discoverer) removeContainer(ctx context.Context, id string) {
	svc := d.serviceForContainerID(id)
	if svc == "" {
		return
	}
	if err := d.store.Update(func(st *state.State) {
		delete(st.Containers, svc)
	}); err != nil {
		slog.Error("discovery.event.destroy.persist", "service", svc, "err", err)
	}
}

// serviceForContainerID looks up the service name in state by matching
// ContainerID. For die/destroy events we don't need to re-inspect — the
// row already carries the service name from the prior start event.
//
// ctx is intentionally absent from the signature: store.Get() is a
// non-blocking RLock; no daemon I/O happens here.
func (d *Discoverer) serviceForContainerID(id string) string {
	short := shortID(id)
	snapshot := d.store.Get()
	for svc, c := range snapshot.Containers {
		if c.ContainerID == short {
			return svc
		}
	}
	return ""
}

// composeServiceLabel is the docker label compose sets on every service
// container. Used to derive the state.Container.Service key.
const composeServiceLabel = "com.docker.compose.service"

// shortID returns the leading 12 characters of id (the standard docker ps
// short form). Returns id unchanged if shorter than 12 — handles the
// degenerate case where a fakeClient feeds a short ID directly.
func shortID(id string) string {
	if len(id) < 12 {
		return id
	}
	return id[:12]
}

// parseImageRef splits an image reference into (image, tag). Rules:
//
//   - "name@sha256:abc..." — pinned-by-digest. Returns (name, "") so
//     Container.Tag is empty for pinned containers; Pinned bool carries
//     the pinning signal separately. Phase 5's UI shows "pinned: opt-out".
//   - "name:tag" with no slash AFTER the colon — splits into (name, tag).
//     E.g. "busybox:latest" -> ("busybox", "latest").
//   - "registry:port/path:tag" — the LAST colon splits only if no slash
//     follows it. E.g. "localhost:5000/foo" -> ("localhost:5000/foo",
//     "latest") because the colon's right-hand side contains a slash;
//     "ghcr.io:443/centroid-is/svc:v1" -> ("ghcr.io:443/centroid-is/svc",
//     "v1") because the final colon's RHS ("v1") contains no slash.
//   - Bare "name" — defaults tag to "latest" (docker's implicit default).
//
// TestParseImageRef_RegistryPrefixed pins all four branches.
//
// Why default to "latest" rather than leaving tag empty for bare refs: it
// matches the docker CLI's implicit behaviour, and the Phase 3 digest
// poller (DETECT-01) expects a non-empty tag in its manifest request URL.
func parseImageRef(ref string) (image, tag string) {
	if at := strings.Index(ref, "@"); at >= 0 {
		return ref[:at], ""
	}
	if colon := strings.LastIndex(ref, ":"); colon >= 0 && !strings.Contains(ref[colon+1:], "/") {
		return ref[:colon], ref[colon+1:]
	}
	return ref, "latest"
}

// filterHmiLabels returns a new map containing only the keys that start
// with "hmi-update." per CONTEXT.md "Per-container enumeration fields."
//
// Returns nil if no matches so the omitempty json tag on
// Container.Labels suppresses the field in the wire payload — keeps the
// 95% case (containers without any hmi-update.* labels — but Wave-2
// discovery only sees containers WITH the watch label, so this branch is
// rare in practice).
//
// T-02-03-01 mitigation: an attacker who plants arbitrary labels on a
// container cannot pollute state.Container.Labels with unrelated keys
// (e.g. `is_admin=true`) — only hmi-update.* survive the filter.
func filterHmiLabels(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := map[string]string{}
	for k, v := range in {
		if strings.HasPrefix(k, "hmi-update.") {
			out[k] = v
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
