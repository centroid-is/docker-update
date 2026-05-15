// Package actions (continued). middleware.go owns the HTTP request
// validation layer that Plan 04-04's handlers_actions.go composes around
// every action endpoint. Four helpers run in this order:
//
//  1. ValidateServiceName    → 400 invalid_service_name
//  2. CheckSelfProtection    → 409 self_protection         (orchestrator method)
//  3. LookupContainer        → 404 container_not_found     (orchestrator method)
//  4. CheckSafetyLabel       → 409 action_disabled_by_label
//
// CRITICAL ORDER NOTE (B1 fix from plan revision):
//
//	CheckSelfProtection runs BEFORE LookupContainer because hmi-update is
//	NOT in the watched-containers state cache by default — the self
//	container ships with hmi-update.watch=false (the operator never wants
//	the tool to manage itself via the API). If LookupContainer ran first,
//	a probe of POST /api/containers/hmi-update/update would return 404
//	(misleading) instead of 409 self_protection (operator-actionable).
//	CheckSelfProtection compares only the path string + the
//	constructor-captured selfService env value — no container state
//	required.
//
// The middleware layer reads container labels EXCLUSIVELY from the cached
// state.Container.Labels populated by the Phase 2 Discoverer goroutine
// (OBS-03 no-I/O discipline). No docker.Inspect call lives in any
// middleware helper.
//
// Force-pull SAFE-03 carve-out:
//
//	ActionForcePull is exempt from CheckSafetyLabel because force-pull-no-
//	recreate is read-only with respect to the running container (it just
//	refreshes the local image cache). Plan 04-04's handler for
//	POST /api/containers/{svc}/force-pull?recreate=true opts INTO the
//	Update safety check explicitly (per RESEARCH.md Open Question #5).
//
// Pattern K (verbatim-constant response bodies — T-01-04-03 path-leak
// guard): every response body in this file is a const. Do NOT interpolate
// variables. If a future branch needs a dynamic field, build a typed body
// and add to the threat model first. Grep gate: zero fmt.Sprintf calls
// for response bodies in this file.
//
// EXPORTED (capitalized) intentionally: the body constants are the wire
// contract. Plan 04-04's internal/api/handlers_actions_test.go imports
// them to assert handler-emitted bodies match the middleware emissions
// byte-for-byte. Duplicating the strings across packages would let drift
// sneak in; one source of truth in internal/actions is the contract
// anchor.
package actions

import (
	"net/http"
	"regexp"

	"github.com/centroid-is/hmi-update/internal/state"
)

// Action discriminates which safety label CheckSafetyLabel consults.
type Action string

const (
	ActionUpdate    Action = "update"
	ActionRollback  Action = "rollback"
	ActionForcePull Action = "force-pull"
)

// Pattern K verbatim-constant response bodies. Do NOT interpolate
// variables — the path-leak guard T-01-04-03 is defined per-string.
// If a future branch needs a dynamic field, build a typed body and
// add it to the threat model first.
//
// EXPORTED (capitalized) intentionally: Plan 04-04's
// internal/api/handlers_actions_test.go cross-imports them to assert
// handler-emitted response bodies match the middleware's parity. One
// source of truth in internal/actions; one consumer in internal/api.
const (
	ActionBodyInvalidServiceName     = `{"error":"invalid_service_name","detail":"service name must match ^[a-zA-Z0-9._-]+$"}`
	ActionBodyContainerNotFound      = `{"error":"container_not_found"}`
	ActionBodySelfProtection         = `{"error":"self_protection","detail":"see PROJECT.md 'Manual self-upgrade procedure'"}`
	ActionBodyActionDisabledUpdate   = `{"error":"action_disabled_by_label","detail":"hmi-update.allow-update=false"}`
	ActionBodyActionDisabledRollback = `{"error":"action_disabled_by_label","detail":"hmi-update.allow-rollback=false"}`
	ActionBodyServiceBusy            = `{"error":"service_busy"}`
	ActionBodyComposeFileMoved       = `{"error":"compose_file_moved","hint":"restart hmi-update to pick up the new docker-compose.yml"}`
)

// serviceNameRegex is the ACT-10 allowlist. Compiled once at package
// init via regexp.MustCompile (the literal is hard-coded; a compile
// error here would be a programmer error and fails the test suite
// immediately). Phase 3's registry/transport.go's sensitiveHeaders
// is the analogous compile-time-constant precedent.
var serviceNameRegex = regexp.MustCompile(`^[a-zA-Z0-9._-]+$`)

// ValidateServiceName extracts the {service} path-parameter, validates
// it against the allowlist regex, and either returns (svc, true) or
// writes 400 ActionBodyInvalidServiceName and returns ("", false).
//
// EXPORTED — Plan 04-04 handlers consume this as the OUTER middleware
// step on every action endpoint.
func ValidateServiceName(w http.ResponseWriter, r *http.Request) (string, bool) {
	svc := r.PathValue("service")
	if !serviceNameRegex.MatchString(svc) {
		writeBody(w, http.StatusBadRequest, ActionBodyInvalidServiceName)
		return "", false
	}
	return svc, true
}

// CheckSafetyLabel writes 409 + the appropriate ActionBodyActionDisabled*
// constant and returns false if the container's labels disable the
// requested action. ActionForcePull is EXEMPT (SAFE-03 carve-out — the
// running container is unaffected; we just refresh the local image
// cache). The caller (Plan 04-04 force-pull handler with ?recreate=true)
// invokes CheckSafetyLabel(w, c, ActionUpdate) explicitly to opt INTO
// the Update label check when ?recreate=true (RESEARCH.md OQ#5).
//
// EXPORTED — Plan 04-04 handlers consume.
func CheckSafetyLabel(w http.ResponseWriter, c state.Container, action Action) bool {
	switch action {
	case ActionUpdate:
		if c.Labels["hmi-update.allow-update"] == "false" {
			writeBody(w, http.StatusConflict, ActionBodyActionDisabledUpdate)
			return false
		}
	case ActionRollback:
		if c.Labels["hmi-update.allow-rollback"] == "false" {
			writeBody(w, http.StatusConflict, ActionBodyActionDisabledRollback)
			return false
		}
	case ActionForcePull:
		// SAFE-03: force-pull is read-only with respect to the running
		// container (just refreshes the local image cache). Not gated
		// by safety labels. The poll loop (internal/poll/poller.go) also
		// ignores these labels — TestSAFE03_PollIgnoresActionLabels
		// pins the source-grep invariant.
	}
	return true
}

// writeBody is the verbatim-constant emitter — sets Content-Type +
// status + writes the body. Mirrors internal/api/handlers.go's healthz
// write shape (w.Header().Set + WriteHeader + Write).
//
// Package-private — middleware helpers and orchestrator helpers in
// this package all funnel through here. NO fmt.Sprintf, NO json.Encode
// of a variable shape.
func writeBody(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(body))
}

// LookupContainer returns the cached state.Container for svc, or the
// zero value + false if the container is not in state.Store. NO 404 is
// written here — the caller (Plan 04-04 handler) decides whether to
// write 404 or to fall through (e.g. force-pull may still proceed with
// an empty container; the orchestrator decides).
//
// NOTE on ordering: CheckSelfProtection MUST run BEFORE LookupContainer
// because hmi-update is NOT in the watched-containers state cache by
// default (hmi-update.watch=false on the self container). If
// LookupContainer ran first, POST /api/containers/hmi-update/update
// would return 404 (misleading) instead of 409 self_protection
// (operator-actionable).
//
// Reads via o.store.Get() which takes the store's RLock for the
// snapshot — see RESEARCH.md A6 for the lock-compat analysis.
func (o *actionOrchestrator) LookupContainer(svc string) (state.Container, bool) {
	if o.store == nil {
		return state.Container{}, false
	}
	c, ok := o.store.Get().Containers[svc]
	return c, ok
}

// CheckSelfProtection writes 409 ActionBodySelfProtection and returns
// false if svc matches the constructor-captured selfService
// (HMI_UPDATE_SELF_SERVICE, default "hmi-update"). Returns true
// (allowed to proceed) otherwise.
//
// CRITICAL: runs BEFORE LookupContainer in the middleware chain — see
// LookupContainer's doc comment for the ordering rationale.
func (o *actionOrchestrator) CheckSelfProtection(w http.ResponseWriter, svc string) bool {
	if svc == o.selfService {
		writeBody(w, http.StatusConflict, ActionBodySelfProtection)
		return false
	}
	return true
}

// SelfService returns the compose service name this hmi-update process
// is running as (HMI_UPDATE_SELF_SERVICE env, default "hmi-update").
// Exposed on the Orchestrator interface so Plan 04-04's main.go can
// echo the value at boot and so test fakes can assert against the
// captured value.
func (o *actionOrchestrator) SelfService() string {
	return o.selfService
}
