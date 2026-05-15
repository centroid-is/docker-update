// RED-FIRST per C4. This file is authored as part of Task 2 of plan 04-03
// and drives verify.go's verifyAfterRecreate green.
//
// What this test file guards (Pitfalls 4 + 12 acceptance surface + B2
// typed-inner-error contract for Plan 04-04):
//
//   - TestVerifyAfterRecreate_15ConsecutiveSuccessfulTicks_ReturnsNil
//     (default-mode happy path; ≥15 ContainerInspect calls observed)
//   - TestVerifyAfterRecreate_RestartCountIncremented_ReturnsErrVerifyFailed
//   - TestVerifyAfterRecreate_NotRunning_ReturnsErrVerifyFailed
//   - TestVerifyAfterRecreate_CtxCanceled_ReturnsErrVerifyCanceled
//   - TestVerifyAfterRecreate_HealthcheckOptIn_Healthy_ReturnsNil
//     ("healthy" short-circuits success before 15 ticks)
//   - TestVerifyAfterRecreate_HealthcheckOptIn_Unhealthy_ReturnsErrVerifyFailed
//   - TestVerifyAfterRecreate_HealthcheckOptIn_NoStatusAfter60s_SoftSuccess
//     (HealthcheckWindow overridden to 100ms; "no status" → nil)
//   - TestVerifyAfterRecreate_TickBoundary_CancelDuringFinalTick_ReturnsErrVerifyCanceled
//     (RESEARCH.md lines 1490–1492 — Phase-4-specific pitfall)
//   - TestVerifyAfterRecreate_VerifyDetail_Extractable (B2: errors.As
//     against *VerifyDetail returns the structured fields for Plan 04-04)
//
// Test seam: verifyTickInterval is a package-private VAR that we override
// to 1*time.Millisecond via t.Cleanup-restored assignment so each test
// runs in <50ms instead of the production 15+ seconds. Documented in
// verify.go's header.
//
// Goroutine assertion contract (Pattern I): assertions fired off-goroutine
// use t.Errorf, NEVER t.Fatal — TestVerifyAfterRecreate_TickBoundary_CancelDuringFinalTick
// runs verifyAfterRecreate in a goroutine to coordinate the cancel.
package actions

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"

	"github.com/centroid-is/hmi-update/internal/docker"
)

// fakeInspector implements dockerInspector with a tick-indexed scripted
// response slice. Each call to ContainerInspect consumes the next entry
// from inspectScript; if the script runs short, the last entry repeats
// (mirrors discovery_test.go::fakeClient.listScript pattern).
//
// The mutex covers script + calls + inspectHook; reading from
// inspectScript without the mutex would race with t.Cleanup-style
// scripted appends in subsequent table-driven cases (we keep it for
// safety against future test extensions).
type fakeInspector struct {
	mu            sync.Mutex
	inspectScript []docker.ContainerInspect
	inspectErr    []error // nil-or-err parallel to inspectScript
	calls         int
	inspectHook   func(call int)
}

func newFakeInspector(script ...docker.ContainerInspect) *fakeInspector {
	return &fakeInspector{inspectScript: script}
}

func (f *fakeInspector) ContainerInspect(ctx context.Context, id string) (docker.ContainerInspect, error) {
	f.mu.Lock()
	idx := f.calls
	hook := f.inspectHook
	f.calls++
	f.mu.Unlock()
	if hook != nil {
		hook(idx)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if idx < len(f.inspectErr) && f.inspectErr[idx] != nil {
		return docker.ContainerInspect{}, f.inspectErr[idx]
	}
	if idx < len(f.inspectScript) {
		return f.inspectScript[idx], nil
	}
	// Script exhausted: repeat last entry.
	if len(f.inspectScript) == 0 {
		return docker.ContainerInspect{}, nil
	}
	return f.inspectScript[len(f.inspectScript)-1], nil
}

func (f *fakeInspector) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// runningInspect produces a ContainerInspect whose State.Running is true
// and RestartCount is the supplied value. The factory hides the moby
// pointer-nested shape so individual tests stay compact.
func runningInspect(restartCount int) docker.ContainerInspect {
	insp := docker.ContainerInspect{}
	insp.Container.State = &container.State{Running: true}
	insp.Container.RestartCount = restartCount
	return insp
}

// runningInspectWithHealth is the healthcheck-opt-in factory.
func runningInspectWithHealth(restartCount int, status container.HealthStatus) docker.ContainerInspect {
	insp := docker.ContainerInspect{}
	insp.Container.State = &container.State{
		Running: true,
		Health:  &container.Health{Status: status},
	}
	insp.Container.RestartCount = restartCount
	return insp
}

// notRunningInspect is the fast-fail factory.
func notRunningInspect(restartCount int) docker.ContainerInspect {
	insp := docker.ContainerInspect{}
	insp.Container.State = &container.State{Running: false}
	insp.Container.RestartCount = restartCount
	return insp
}

// setFastTick speeds up the verify loop for unit-test wall-clock budget.
// t.Cleanup restores the production value.
func setFastTick(t *testing.T) {
	t.Helper()
	prior := verifyTickInterval
	verifyTickInterval = 1 * time.Millisecond
	t.Cleanup(func() { verifyTickInterval = prior })
}

// ----------------------------------------------------------------------------
// Default-mode happy + fast-fail
// ----------------------------------------------------------------------------

func TestVerifyAfterRecreate_15ConsecutiveSuccessfulTicks_ReturnsNil(t *testing.T) {
	setFastTick(t)
	// 30 ticks of Running=true,RestartCount=0 so the loop comfortably
	// reaches the 15-tick target. target = VerifyWindow / verifyTickInterval
	// (verify.go), so VerifyWindow=15*tickInterval → target=15. The
	// production deadline grants a 2*verifyTickInterval safety factor on
	// top of verifyWindow so the 15th tick completes before deadline
	// expiry.
	tickInterval := verifyTickInterval
	script := make([]docker.ContainerInspect, 30)
	for i := range script {
		script[i] = runningInspect(0)
	}
	insp := newFakeInspector(script...)
	o := &actionOrchestrator{dockerInspector: insp}
	err := o.verifyAfterRecreate(context.Background(), verifySnapshot{
		ContainerID:  "abc",
		RestartCount: 0,
		VerifyWindow: 15 * tickInterval,
	})
	if err != nil {
		t.Fatalf("verifyAfterRecreate: want nil, got %v", err)
	}
	if got := insp.callCount(); got < 15 {
		t.Errorf("inspect calls: want >=15, got %d", got)
	}
}

func TestVerifyAfterRecreate_RestartCountIncremented_ReturnsErrVerifyFailed(t *testing.T) {
	setFastTick(t)
	// Tick 0..1 healthy; tick 2 shows RestartCount delta.
	insp := newFakeInspector(
		runningInspect(0),
		runningInspect(0),
		runningInspect(3),
	)
	o := &actionOrchestrator{dockerInspector: insp}
	err := o.verifyAfterRecreate(context.Background(), verifySnapshot{
		ContainerID:  "abc",
		RestartCount: 0,
		VerifyWindow: 150 * time.Millisecond,
	})
	if err == nil {
		t.Fatalf("verifyAfterRecreate: want non-nil err, got nil")
	}
	if !errors.Is(err, ErrVerifyFailed) {
		t.Errorf("errors.Is ErrVerifyFailed: want true, got false (err=%v)", err)
	}
	if errors.Is(err, ErrVerifyCanceled) {
		t.Errorf("errors.Is ErrVerifyCanceled: want false (this is verify_failed, not canceled), got true")
	}
}

func TestVerifyAfterRecreate_NotRunning_ReturnsErrVerifyFailed(t *testing.T) {
	setFastTick(t)
	insp := newFakeInspector(notRunningInspect(0))
	o := &actionOrchestrator{dockerInspector: insp}
	err := o.verifyAfterRecreate(context.Background(), verifySnapshot{
		ContainerID:  "abc",
		VerifyWindow: 150 * time.Millisecond,
	})
	if err == nil {
		t.Fatalf("verifyAfterRecreate: want non-nil err, got nil")
	}
	if !errors.Is(err, ErrVerifyFailed) {
		t.Errorf("errors.Is ErrVerifyFailed: want true, got false (err=%v)", err)
	}
}

// ----------------------------------------------------------------------------
// Ctx cancellation
// ----------------------------------------------------------------------------

func TestVerifyAfterRecreate_CtxCanceled_ReturnsErrVerifyCanceled(t *testing.T) {
	setFastTick(t)
	insp := newFakeInspector(runningInspect(0))
	o := &actionOrchestrator{dockerInspector: insp}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel BEFORE first tick
	err := o.verifyAfterRecreate(ctx, verifySnapshot{
		ContainerID:  "abc",
		VerifyWindow: 150 * time.Millisecond,
	})
	if err == nil {
		t.Fatalf("verifyAfterRecreate: want non-nil err, got nil")
	}
	if !errors.Is(err, ErrVerifyCanceled) {
		t.Errorf("errors.Is ErrVerifyCanceled: want true, got false (err=%v)", err)
	}
	if errors.Is(err, ErrVerifyFailed) {
		t.Errorf("errors.Is ErrVerifyFailed: want false (this is canceled, not failed), got true")
	}
}

// TestVerifyAfterRecreate_TickBoundary_CancelDuringFinalTick_ReturnsErrVerifyCanceled
// asserts the Phase-4-specific pitfall (RESEARCH.md lines 1490–1492):
// if ctx is canceled in the same select cycle that would otherwise
// return success, the canceled branch is taken. Go's pseudo-random
// select means we can't deterministically force the ordering, so this
// test uses the inspectHook to cancel at exactly tick 14 — before the
// 15th success — and asserts the loop exits with ErrVerifyCanceled.
func TestVerifyAfterRecreate_TickBoundary_CancelDuringFinalTick_ReturnsErrVerifyCanceled(t *testing.T) {
	setFastTick(t)
	script := make([]docker.ContainerInspect, 30)
	for i := range script {
		script[i] = runningInspect(0)
	}
	insp := newFakeInspector(script...)
	ctx, cancel := context.WithCancel(context.Background())
	// Cancel after 14 inspect calls have started — before tick 15 ever
	// runs select; the next select picks ctx.Done.
	insp.inspectHook = func(call int) {
		if call == 13 {
			// inspectHook runs INSIDE ContainerInspect (before the loop
			// returns to the for-select). Calling cancel here cancels
			// ctx; the next iteration sees ctx.Done.
			cancel()
		}
	}
	o := &actionOrchestrator{dockerInspector: insp}
	err := o.verifyAfterRecreate(ctx, verifySnapshot{
		ContainerID:  "abc",
		VerifyWindow: 150 * time.Millisecond,
	})
	if err == nil {
		t.Fatalf("verifyAfterRecreate: want non-nil err after cancel, got nil")
	}
	if !errors.Is(err, ErrVerifyCanceled) {
		t.Errorf("errors.Is ErrVerifyCanceled after mid-window cancel: want true, got false (err=%v)", err)
	}
}

// ----------------------------------------------------------------------------
// Healthcheck opt-in
// ----------------------------------------------------------------------------

func TestVerifyAfterRecreate_HealthcheckOptIn_Healthy_ReturnsNil(t *testing.T) {
	setFastTick(t)
	// First tick is "starting"; second tick is "healthy" — the loop
	// short-circuits success before reaching the 15-tick target.
	insp := newFakeInspector(
		runningInspectWithHealth(0, container.Starting),
		runningInspectWithHealth(0, container.Healthy),
	)
	o := &actionOrchestrator{dockerInspector: insp}
	err := o.verifyAfterRecreate(context.Background(), verifySnapshot{
		ContainerID:       "abc",
		HealthcheckOptIn:  true,
		VerifyWindow:      150 * time.Millisecond,
		HealthcheckWindow: 600 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("verifyAfterRecreate: want nil (healthy short-circuit), got %v", err)
	}
}

func TestVerifyAfterRecreate_HealthcheckOptIn_Unhealthy_ReturnsErrVerifyFailed(t *testing.T) {
	setFastTick(t)
	insp := newFakeInspector(runningInspectWithHealth(0, container.Unhealthy))
	o := &actionOrchestrator{dockerInspector: insp}
	err := o.verifyAfterRecreate(context.Background(), verifySnapshot{
		ContainerID:       "abc",
		HealthcheckOptIn:  true,
		HealthcheckWindow: 150 * time.Millisecond,
	})
	if err == nil {
		t.Fatalf("verifyAfterRecreate: want non-nil err, got nil")
	}
	if !errors.Is(err, ErrVerifyFailed) {
		t.Errorf("errors.Is ErrVerifyFailed: want true, got false (err=%v)", err)
	}
}

func TestVerifyAfterRecreate_HealthcheckOptIn_NoStatusAfter60s_SoftSuccess(t *testing.T) {
	setFastTick(t)
	// Soft-success semantic (CONTEXT.md Area 3): in healthcheck-opt-in
	// mode, if the loop NEVER observes Health.Status == Healthy or
	// Unhealthy throughout the window (e.g. the container has no
	// HEALTHCHECK directive yet the operator labeled wait-for-healthy=true),
	// the deadline expiry returns nil rather than ErrVerifyFailed — the
	// operator's signal that the opt-in label was misconfigured, not
	// that the recreate is broken. The implementation tracks
	// `sawHealthDeclared` and gates soft-success on its negation.
	//
	// Test setup: all inspects return runningInspect (no Health field;
	// fast-fail checks pass; healthcheck branch is bypassed because
	// State.Health is nil so sawHealthDeclared stays false). The
	// HealthcheckWindow is set very short so the deadline expires
	// before the (default 15s/1ms = 15000-tick) target — the
	// "no Health declared" soft-success branch fires.
	insp := newFakeInspector(runningInspect(0))
	o := &actionOrchestrator{dockerInspector: insp}
	err := o.verifyAfterRecreate(context.Background(), verifySnapshot{
		ContainerID:       "abc",
		HealthcheckOptIn:  true,
		HealthcheckWindow: 5 * time.Millisecond,
		// Leave VerifyWindow=0 → defaults to 15s → target=15000;
		// the loop cannot reach target before the 5ms HealthcheckWindow
		// deadline, so the soft-success branch fires.
	})
	if err != nil {
		t.Fatalf("verifyAfterRecreate (soft-success path): want nil, got %v", err)
	}
}

// ----------------------------------------------------------------------------
// Typed VerifyDetail extraction (B2)
// ----------------------------------------------------------------------------

// TestVerifyAfterRecreate_VerifyDetail_Extractable drives the
// RestartCount-delta branch and asserts errors.As yields a populated
// *VerifyDetail. Plan 04-04's TestHandleUpdate_VerifyFailed_500_StructuredBody
// depends on this contract — the structured response body is built from
// these fields.
func TestVerifyAfterRecreate_VerifyDetail_Extractable(t *testing.T) {
	setFastTick(t)
	insp := newFakeInspector(
		runningInspect(0),
		runningInspect(0),
		runningInspect(5), // delta=5 triggers ErrVerifyFailed
	)
	o := &actionOrchestrator{dockerInspector: insp}
	err := o.verifyAfterRecreate(context.Background(), verifySnapshot{
		ContainerID:  "container-xyz",
		RestartCount: 0,
		VerifyWindow: 150 * time.Millisecond,
	})
	if err == nil {
		t.Fatalf("verifyAfterRecreate: want non-nil err, got nil")
	}
	if !errors.Is(err, ErrVerifyFailed) {
		t.Fatalf("errors.Is ErrVerifyFailed: want true, got false (err=%v)", err)
	}
	var detail *VerifyDetail
	if !errors.As(err, &detail) {
		t.Fatalf("errors.As against *VerifyDetail: want true, got false (err=%v)", err)
	}
	if detail.RestartCount != 5 {
		t.Errorf("detail.RestartCount: want 5, got %d", detail.RestartCount)
	}
	if detail.ContainerID != "container-xyz" {
		t.Errorf("detail.ContainerID: want container-xyz, got %q", detail.ContainerID)
	}
	if detail.Reason == "" {
		t.Errorf("detail.Reason: want non-empty, got empty")
	}
	if !detail.Running {
		t.Errorf("detail.Running: want true (the increment happens while still Running), got false")
	}
}
