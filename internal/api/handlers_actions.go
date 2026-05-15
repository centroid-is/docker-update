// Package api (continued). handlers_actions.go owns the three Phase 4
// HTTP action endpoints — POST /api/containers/{service}/update,
// /rollback, /force-pull — plus the writeActionError dispatcher that
// maps every actions/compose sentinel to the documented HTTP status code
// and verbatim-constant response body.
//
// Middleware chain order (load-bearing):
//
//	ValidateServiceName → CheckSelfProtection → LookupContainer → CheckSafetyLabel
//
// CheckSelfProtection runs BEFORE LookupContainer because hmi-update is
// NOT in the watched-containers state cache by default (the self
// container ships with hmi-update.watch=false — the operator never wants
// the tool to manage itself via the API). If LookupContainer ran first,
// a probe of POST /api/containers/hmi-update/update would 404 (misleading)
// instead of 409 self_protection (operator-actionable) — ACT-09.
//
// Pattern K (verbatim-constant response bodies — T-01-04-03 path-leak
// guard): every error body except verify_failed is a const. The ONE
// exception is writeVerifyFailedBody — CONTEXT.md Area 3 lines 102–112
// LOCK a structured body shape for operator diagnosis. Inputs to that
// body are pre-trimmed by the orchestrator (no operator paths in the
// trim domain) and the JSON struct literal only emits integer fields,
// a bool, a sha256-format ContainerID, and a Reason string the
// orchestrator constructs via Sprintf over non-path values. Tracked as
// T-04-04-03 in the plan's threat model.
//
// Constants that overlap with the middleware-emitted wire contract live
// EXCLUSIVELY in internal/actions (Plan 04-03 exports them as
// actions.ActionBody*); this file imports + reuses them so the wire
// shape stays single-sourced. Handler-only bodies (errors the middleware
// never produces) live here as package-private constants.
package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/centroid-is/hmi-update/internal/actions"
	"github.com/centroid-is/hmi-update/internal/compose"
)

// Handler-only response bodies. These are the verbatim wire payloads for
// errors the middleware layer never emits (so they cannot live in
// internal/actions/middleware.go). Pattern K — NO fmt.Sprintf, NO
// interpolation; T-01-04-03 path-leak guard is defined per-string.
const (
	// actionBodyOrchestratorUnwired is emitted ONLY by the defensive
	// nil-guard at the top of each action handler. Production main.go
	// log.Fatalf's on actions.NewOrchestrator errors so this branch is
	// only reachable via test wiring (TestHandleUpdate_OrchestratorUnwired_503).
	actionBodyOrchestratorUnwired = `{"error":"orchestrator_not_wired","detail":"restart hmi-update; check boot logs for actions.NewOrchestrator errors"}`

	// actionBodyNoPreviousDigest is the Rollback-specific 400 — the
	// container has never been updated so there is no previous digest
	// to roll back to.
	actionBodyNoPreviousDigest = `{"error":"no_previous_digest","detail":"rollback requires a recorded previous digest; perform an Update first"}`

	// actionBodyPullFailed surfaces docker pull failures (including
	// Pitfall 1 digest mismatch). The detailed err (registry URL,
	// underlying network error, digest values) goes to slog via
	// action.pull_failed; the wire body just points at the logs.
	actionBodyPullFailed = `{"error":"pull_failed","detail":"see logs for action.pull_failed event"}`

	// actionBodyComposeFailed surfaces docker compose runner non-zero
	// exits. The stderr snippet goes to slog via action.compose_failed
	// (Plan 04-02's compose.run event); the wire body just points at
	// the logs — never echo stderr to the wire (T-04-04-04 path-leak
	// guard: stderr may contain absolute paths from compose).
	actionBodyComposeFailed = `{"error":"compose_failed","detail":"see logs for action.compose_failed event"}`

	// actionBodyVerifyCanceled is emitted when the verify loop receives
	// ctx.Done (SIGTERM, request abandon). Distinct from verify_failed:
	// the recreate may or may not have succeeded; the operator should
	// retry after the cause of cancellation is resolved.
	actionBodyVerifyCanceled = `{"error":"verify_canceled","detail":"action canceled by server shutdown; retry after restart"}`

	// actionBodyVerifyFailedFallback is the safety net body for the
	// unlikely case where writeVerifyFailedBody is invoked but the
	// wrap chain does NOT carry a *actions.VerifyDetail (orchestrator
	// invariant violation). Should never appear in production.
	actionBodyVerifyFailedFallback = `{"error":"verify_failed","detail":"see logs for action.verify_failed event"}`

	// actionBodyInternal is the default-branch body — any sentinel
	// errors.Is doesn't recognise lands here. Should be vanishingly
	// rare; surfacing it is a signal that the orchestrator added a new
	// sentinel without updating the writeActionError dispatch.
	actionBodyInternal = `{"error":"internal","detail":"see logs"}`
)

// handleUpdate implements POST /api/containers/{service}/update.
//
// Chain: ValidateServiceName → CheckSelfProtection → LookupContainer →
// CheckSafetyLabel(ActionUpdate) → orchestrator.Update.
//
// CheckSelfProtection runs BEFORE LookupContainer because hmi-update is
// not in the watched-containers state cache by default — see file-level
// godoc + ACT-09.
func (s *Server) handleUpdate(w http.ResponseWriter, r *http.Request) {
	if s.orchestrator == nil {
		writeActionBody(w, http.StatusServiceUnavailable, actionBodyOrchestratorUnwired)
		return
	}
	svc, ok := actions.ValidateServiceName(w, r)
	if !ok {
		return
	}
	if !s.orchestrator.CheckSelfProtection(w, svc) {
		return
	}
	c, ok := s.orchestrator.LookupContainer(svc)
	if !ok {
		writeActionBody(w, http.StatusNotFound, actions.ActionBodyContainerNotFound)
		return
	}
	if !actions.CheckSafetyLabel(w, c, actions.ActionUpdate) {
		return
	}
	result, err := s.orchestrator.Update(r.Context(), svc)
	if err != nil {
		writeActionError(w, err)
		return
	}
	writeActionResult(w, result)
}

// handleRollback implements POST /api/containers/{service}/rollback.
//
// Chain: ValidateServiceName → CheckSelfProtection → LookupContainer →
// CheckSafetyLabel(ActionRollback) → orchestrator.Rollback.
func (s *Server) handleRollback(w http.ResponseWriter, r *http.Request) {
	if s.orchestrator == nil {
		writeActionBody(w, http.StatusServiceUnavailable, actionBodyOrchestratorUnwired)
		return
	}
	svc, ok := actions.ValidateServiceName(w, r)
	if !ok {
		return
	}
	if !s.orchestrator.CheckSelfProtection(w, svc) {
		return
	}
	c, ok := s.orchestrator.LookupContainer(svc)
	if !ok {
		writeActionBody(w, http.StatusNotFound, actions.ActionBodyContainerNotFound)
		return
	}
	if !actions.CheckSafetyLabel(w, c, actions.ActionRollback) {
		return
	}
	result, err := s.orchestrator.Rollback(r.Context(), svc)
	if err != nil {
		writeActionError(w, err)
		return
	}
	writeActionResult(w, result)
}

// handleForcePull implements POST /api/containers/{service}/force-pull
// (optional ?recreate=true query param).
//
// Chain default (no recreate): ValidateServiceName → CheckSelfProtection
// → LookupContainer → orchestrator.ForcePull(ctx, svc, false). SAFE-03
// carve-out: NO CheckSafetyLabel — force-pull-no-recreate is read-only
// with respect to the running container.
//
// Chain with ?recreate=true: same as above PLUS CheckSafetyLabel(ActionUpdate)
// — the recreate IS a recreate operation, so SAFE-01 applies (RESEARCH.md
// Open Question #5).
func (s *Server) handleForcePull(w http.ResponseWriter, r *http.Request) {
	if s.orchestrator == nil {
		writeActionBody(w, http.StatusServiceUnavailable, actionBodyOrchestratorUnwired)
		return
	}
	svc, ok := actions.ValidateServiceName(w, r)
	if !ok {
		return
	}
	if !s.orchestrator.CheckSelfProtection(w, svc) {
		return
	}
	c, ok := s.orchestrator.LookupContainer(svc)
	if !ok {
		writeActionBody(w, http.StatusNotFound, actions.ActionBodyContainerNotFound)
		return
	}
	recreate := r.URL.Query().Get("recreate") == "true"
	if recreate {
		// recreate=true opts INTO the Update safety check (SAFE-01
		// applies; the recreate IS a recreate operation).
		if !actions.CheckSafetyLabel(w, c, actions.ActionUpdate) {
			return
		}
	}
	result, err := s.orchestrator.ForcePull(r.Context(), svc, recreate)
	if err != nil {
		writeActionError(w, err)
		return
	}
	writeActionResult(w, result)
}

// writeActionError dispatches every error class to its documented HTTP
// status code + verbatim-constant body, with the ONE locked exception
// for verify_failed (structured body — see writeVerifyFailedBody).
//
// Order of errors.Is checks matters: compose.ErrComposeFileMoved is
// tested BEFORE actions.ErrComposeFailed because the compose-layer
// sentinel is a distinct error class (412 — pre-action drift) and a
// drifted compose file should never have entered the action body.
// The actions-layer ErrComposeFailed is the runtime non-zero exit (500).
func writeActionError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, compose.ErrComposeFileMoved):
		writeActionBody(w, http.StatusPreconditionFailed, actions.ActionBodyComposeFileMoved)

	case errors.Is(err, actions.ErrServiceBusy):
		writeActionBody(w, http.StatusConflict, actions.ActionBodyServiceBusy)

	case errors.Is(err, actions.ErrSelfProtection):
		// Rare — middleware CheckSelfProtection writes 409 directly.
		// The sentinel exists for future programmatic consumers.
		writeActionBody(w, http.StatusConflict, actions.ActionBodySelfProtection)

	case errors.Is(err, actions.ErrActionDisabledByLabel):
		// Rare — middleware CheckSafetyLabel writes 409 directly.
		writeActionBody(w, http.StatusConflict, actions.ActionBodyActionDisabledUpdate)

	case errors.Is(err, actions.ErrVerifyCanceled):
		writeActionBody(w, http.StatusServiceUnavailable, actionBodyVerifyCanceled)

	case errors.Is(err, actions.ErrVerifyFailed):
		// SOLE exception to Pattern K — structured body per CONTEXT.md
		// Area 3 lines 102–112. Inputs to the body are pre-trimmed by
		// the orchestrator (no operator paths). Tracked as T-04-04-03.
		writeVerifyFailedBody(w, err)

	case errors.Is(err, actions.ErrComposeFailed):
		writeActionBody(w, http.StatusInternalServerError, actionBodyComposeFailed)

	case errors.Is(err, actions.ErrPullFailed):
		writeActionBody(w, http.StatusInternalServerError, actionBodyPullFailed)

	case errors.Is(err, actions.ErrNoPreviousDigest):
		// WARNING-02 fix: proper sentinel dispatch. The orchestrator now
		// wraps ErrNoPreviousDigest in Rollback step 2. The legacy
		// substring scan (isNoPreviousDigest below) is retained as a
		// fallback for any wrap-chain error that does not carry the
		// sentinel but does contain the literal token — defensive
		// belt-and-braces in case a future Rollback caller wraps a raw
		// string without the sentinel.
		writeActionBody(w, http.StatusBadRequest, actionBodyNoPreviousDigest)

	case isNoPreviousDigest(err):
		writeActionBody(w, http.StatusBadRequest, actionBodyNoPreviousDigest)

	default:
		// Unrecognised error class — log via slog so the operator can
		// trace and surface generic 500.
		slog.Error("handlers_actions.unknown_error", "err", err)
		writeActionBody(w, http.StatusInternalServerError, actionBodyInternal)
	}
}

// isNoPreviousDigest is the fallback substring scan retained alongside
// the proper sentinel dispatch (errors.Is(err, actions.ErrNoPreviousDigest))
// in writeActionError. WARNING-01 / WARNING-02 of the Phase 4 review
// promoted the canonical detection path to errors.Is; this helper now
// only catches edge cases where a future caller wraps a raw string
// without using the sentinel.
//
// WARNING-01 fix: use strings.Contains (SIMD-optimised in the stdlib via
// bytealg.IndexString) instead of the prior hand-rolled byte loop.
// Identical anti-pattern to Phase 3 WR-09 (commit c697286).
func isNoPreviousDigest(err error) bool {
	return err != nil && strings.Contains(err.Error(), "no_previous_digest")
}

// writeVerifyFailedBody is the SOLE Pattern K exception in this file.
// CONTEXT.md Area 3 lines 102–112 LOCK the response body shape for
// verify-after-recreate failures:
//
//	{
//	  "error": "verify_failed",
//	  "reason": "container restarted 3 times in 15s",
//	  "exit_code": null,
//	  "restart_count": 3,
//	  "running": false,
//	  "container_id": "abc123def456"
//	}
//
// Inputs are extracted from the *actions.VerifyDetail typed inner error
// via errors.As. The detail is pre-trimmed by the orchestrator (Reason
// is constructed via Sprintf over integer counters + duration — no
// operator paths in the trim domain). Tracked as T-04-04-03 in the
// plan's threat model.
//
// If the wrap chain does NOT carry a *actions.VerifyDetail (orchestrator
// invariant violation), we fall back to the safe verbatim body
// actionBodyVerifyFailedFallback.
func writeVerifyFailedBody(w http.ResponseWriter, err error) {
	var detail *actions.VerifyDetail
	if !errors.As(err, &detail) {
		// Orchestrator MUST wrap ErrVerifyFailed with *VerifyDetail.
		// If it didn't, fall back rather than panic.
		slog.Error("handlers_actions.verify_failed_missing_detail", "err", err)
		writeActionBody(w, http.StatusInternalServerError, actionBodyVerifyFailedFallback)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusInternalServerError)
	_ = json.NewEncoder(w).Encode(struct {
		Error        string `json:"error"`
		Reason       string `json:"reason"`
		ExitCode     *int   `json:"exit_code"`
		RestartCount int    `json:"restart_count"`
		Running      bool   `json:"running"`
		ContainerID  string `json:"container_id"`
	}{
		Error:        "verify_failed",
		Reason:       detail.Reason,
		ExitCode:     nil,
		RestartCount: detail.RestartCount,
		Running:      detail.Running,
		ContainerID:  detail.ContainerID,
	})
}

// writeActionResult writes the 200 OK success envelope:
//
//	{"current_digest":"sha256:...","previous_digest":"sha256:...","no_op":false}
//
// previous_digest and no_op are emitted with omitempty so the wire stays
// minimal for the common Update case (no_op false, previous_digest
// populated). ACT-11 requires current_digest to always be present.
func writeActionResult(w http.ResponseWriter, r actions.ActionResult) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(struct {
		CurrentDigest  string `json:"current_digest"`
		PreviousDigest string `json:"previous_digest,omitempty"`
		NoOp           bool   `json:"no_op,omitempty"`
	}{r.CurrentDigest, r.PreviousDigest, r.NoOp})
}

// writeActionBody is the verbatim-constant emitter for the action
// handler path. Mirrors writeBody in internal/actions/middleware.go and
// handlers.go's healthz pattern (Header().Set + WriteHeader + Write).
// Package-private — only the action handlers + writeActionError funnel
// through here.
func writeActionBody(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(body))
}
