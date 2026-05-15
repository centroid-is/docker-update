// RED-FIRST per C4. This file is authored as part of Task 2 of plan 04-03
// and drives middleware.go's ValidateServiceName, CheckSafetyLabel, and the
// orchestrator-method helpers (LookupContainer, CheckSelfProtection) green.
//
// What this test file guards (ACT-09 + ACT-10 + SAFE-01/02/03 acceptance
// surface):
//
//   - TestValidateServiceName_HappyPath
//   - TestValidateServiceName_PathTraversal_Rejected (../../etc/passwd → 400)
//   - TestValidateServiceName_EmptyService_Rejected
//   - TestValidateServiceName_SpecialChars_Rejected (svc;rm -rf /)
//   - TestLookupContainer_HappyPath
//   - TestLookupContainer_NotFound
//   - TestCheckSelfProtection_MatchesSelfService_Rejected (409)
//   - TestCheckSelfProtection_DifferentService_Allowed
//   - TestCheckSafetyLabel_Update_AllowFalse_Rejected (verbatim body)
//   - TestCheckSafetyLabel_Update_AllowTrue_Allowed
//   - TestCheckSafetyLabel_Update_LabelAbsent_Allowed
//   - TestCheckSafetyLabel_Rollback_AllowFalse_Rejected (verbatim body)
//   - TestCheckSafetyLabel_ForcePull_AllowFalse_Allowed (SAFE-03 carve-out)
//   - TestSAFE03_PollIgnoresActionLabels (code-grep on poll/poller.go)
//
// Verbatim-body assertions compare against the exported ActionBody*
// constants (Pattern K). Catches typos in either place via the byte-for-
// byte equality check.
//
// Goroutine assertion contract: none of these tests spawn goroutines;
// all assertions on the test goroutine.
package actions

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/centroid-is/docker-update/internal/state"
)

// memoryStore is a minimal stateReader for middleware tests. Stores a
// snapshot of Containers and Get returns it under no lock (single-test-
// goroutine usage).
type memoryStore struct {
	mu sync.RWMutex
	s  state.State
}

func newMemoryStore() *memoryStore {
	return &memoryStore{
		s: state.State{
			Version:    state.SchemaVersion,
			Containers: map[string]state.Container{},
		},
	}
}

func (m *memoryStore) Get() state.State {
	m.mu.RLock()
	defer m.mu.RUnlock()
	// Return a shallow snapshot.
	out := state.State{
		Version:    m.s.Version,
		Containers: make(map[string]state.Container, len(m.s.Containers)),
	}
	for k, v := range m.s.Containers {
		out.Containers[k] = v
	}
	return out
}

func (m *memoryStore) put(c state.Container) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.s.Containers[c.Service] = c
}

// newReqWithService constructs an httptest.Request where r.PathValue("service")
// returns the supplied svc. Go 1.22+'s ServeMux requires the request to be
// routed through a registered handler for PathValue to be populated, OR the
// caller can use req.SetPathValue (Go 1.22+).
func newReqWithService(svc string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/api/containers/x/update", nil)
	r.SetPathValue("service", svc)
	return r
}

// ----------------------------------------------------------------------------
// ValidateServiceName
// ----------------------------------------------------------------------------

func TestValidateServiceName_HappyPath(t *testing.T) {
	t.Parallel()
	w := httptest.NewRecorder()
	got, ok := ValidateServiceName(w, newReqWithService("my-svc"))
	if !ok {
		t.Fatalf("ValidateServiceName(my-svc): want ok=true, got false (status=%d body=%q)",
			w.Code, w.Body.String())
	}
	if got != "my-svc" {
		t.Errorf("returned service: want %q got %q", "my-svc", got)
	}
}

func TestValidateServiceName_PathTraversal_Rejected(t *testing.T) {
	t.Parallel()
	w := httptest.NewRecorder()
	_, ok := ValidateServiceName(w, newReqWithService("../../etc/passwd"))
	if ok {
		t.Fatalf("ValidateServiceName(../../etc/passwd): want ok=false, got true")
	}
	if w.Code != http.StatusBadRequest {
		t.Errorf("status: want 400, got %d", w.Code)
	}
	if got := w.Body.String(); got != ActionBodyInvalidServiceName {
		t.Errorf("body mismatch:\n  got:  %q\n  want: %q", got, ActionBodyInvalidServiceName)
	}
}

func TestValidateServiceName_EmptyService_Rejected(t *testing.T) {
	t.Parallel()
	w := httptest.NewRecorder()
	_, ok := ValidateServiceName(w, newReqWithService(""))
	if ok {
		t.Fatalf("ValidateServiceName(''): want ok=false, got true")
	}
	if w.Code != http.StatusBadRequest {
		t.Errorf("status: want 400, got %d", w.Code)
	}
}

func TestValidateServiceName_SpecialChars_Rejected(t *testing.T) {
	t.Parallel()
	w := httptest.NewRecorder()
	_, ok := ValidateServiceName(w, newReqWithService("svc;rm -rf /"))
	if ok {
		t.Fatalf("ValidateServiceName(svc;rm -rf /): want ok=false, got true")
	}
	if w.Code != http.StatusBadRequest {
		t.Errorf("status: want 400, got %d", w.Code)
	}
	if got := w.Body.String(); got != ActionBodyInvalidServiceName {
		t.Errorf("body mismatch:\n  got:  %q\n  want: %q", got, ActionBodyInvalidServiceName)
	}
}

// ----------------------------------------------------------------------------
// LookupContainer
// ----------------------------------------------------------------------------

func TestLookupContainer_HappyPath(t *testing.T) {
	t.Parallel()
	store := newMemoryStore()
	store.put(state.Container{Service: "svc-a", Image: "img/a", Tag: "latest"})
	o := &actionOrchestrator{store: store}
	c, ok := o.LookupContainer("svc-a")
	if !ok {
		t.Fatalf("LookupContainer(svc-a): want ok=true, got false")
	}
	if c.Service != "svc-a" {
		t.Errorf("returned service: want svc-a, got %q", c.Service)
	}
}

func TestLookupContainer_NotFound(t *testing.T) {
	t.Parallel()
	store := newMemoryStore()
	o := &actionOrchestrator{store: store}
	_, ok := o.LookupContainer("missing")
	if ok {
		t.Errorf("LookupContainer(missing): want ok=false, got true")
	}
}

// ----------------------------------------------------------------------------
// CheckSelfProtection
// ----------------------------------------------------------------------------

func TestCheckSelfProtection_MatchesSelfService_Rejected(t *testing.T) {
	t.Parallel()
	o := &actionOrchestrator{selfService: "docker-update"}
	w := httptest.NewRecorder()
	if o.CheckSelfProtection(w, "docker-update") {
		t.Fatalf("CheckSelfProtection(docker-update): want false, got true")
	}
	if w.Code != http.StatusConflict {
		t.Errorf("status: want 409, got %d", w.Code)
	}
	if got := w.Body.String(); got != ActionBodySelfProtection {
		t.Errorf("body mismatch:\n  got:  %q\n  want: %q", got, ActionBodySelfProtection)
	}
}

func TestCheckSelfProtection_DifferentService_Allowed(t *testing.T) {
	t.Parallel()
	o := &actionOrchestrator{selfService: "docker-update"}
	w := httptest.NewRecorder()
	if !o.CheckSelfProtection(w, "some-other-svc") {
		t.Fatalf("CheckSelfProtection(some-other-svc): want true, got false")
	}
	if w.Code != 200 && w.Code != 0 {
		// httptest.NewRecorder defaults Code to 200; we should not have
		// written anything that flips that.
		t.Errorf("status: want unwritten/200, got %d (body=%q)", w.Code, w.Body.String())
	}
}

// ----------------------------------------------------------------------------
// CheckSafetyLabel
// ----------------------------------------------------------------------------

func TestCheckSafetyLabel_Update_AllowFalse_Rejected(t *testing.T) {
	t.Parallel()
	c := state.Container{Service: "db", Labels: map[string]string{"hmi-update.allow-update": "false"}}
	w := httptest.NewRecorder()
	if CheckSafetyLabel(w, c, ActionUpdate) {
		t.Fatalf("CheckSafetyLabel(db, ActionUpdate) with allow-update=false: want false, got true")
	}
	if w.Code != http.StatusConflict {
		t.Errorf("status: want 409, got %d", w.Code)
	}
	if got := w.Body.String(); got != ActionBodyActionDisabledUpdate {
		t.Errorf("body mismatch:\n  got:  %q\n  want: %q", got, ActionBodyActionDisabledUpdate)
	}
}

func TestCheckSafetyLabel_Update_AllowTrue_Allowed(t *testing.T) {
	t.Parallel()
	c := state.Container{Labels: map[string]string{"hmi-update.allow-update": "true"}}
	w := httptest.NewRecorder()
	if !CheckSafetyLabel(w, c, ActionUpdate) {
		t.Fatalf("CheckSafetyLabel(allow-update=true): want true, got false")
	}
}

func TestCheckSafetyLabel_Update_LabelAbsent_Allowed(t *testing.T) {
	t.Parallel()
	c := state.Container{} // no labels at all
	w := httptest.NewRecorder()
	if !CheckSafetyLabel(w, c, ActionUpdate) {
		t.Fatalf("CheckSafetyLabel(no labels): want true (default permissive), got false")
	}
}

func TestCheckSafetyLabel_Rollback_AllowFalse_Rejected(t *testing.T) {
	t.Parallel()
	c := state.Container{Labels: map[string]string{"hmi-update.allow-rollback": "false"}}
	w := httptest.NewRecorder()
	if CheckSafetyLabel(w, c, ActionRollback) {
		t.Fatalf("CheckSafetyLabel(allow-rollback=false): want false, got true")
	}
	if w.Code != http.StatusConflict {
		t.Errorf("status: want 409, got %d", w.Code)
	}
	if got := w.Body.String(); got != ActionBodyActionDisabledRollback {
		t.Errorf("body mismatch:\n  got:  %q\n  want: %q", got, ActionBodyActionDisabledRollback)
	}
}

func TestCheckSafetyLabel_ForcePull_AllowFalse_Allowed(t *testing.T) {
	t.Parallel()
	// SAFE-03 carve-out: force-pull is NOT gated by safety labels even
	// when allow-update=false. The poll loop also ignores these labels
	// (TestSAFE03_PollIgnoresActionLabels asserts the source-grep
	// invariant on internal/poll/poller.go).
	c := state.Container{Labels: map[string]string{"hmi-update.allow-update": "false"}}
	w := httptest.NewRecorder()
	if !CheckSafetyLabel(w, c, ActionForcePull) {
		t.Fatalf("CheckSafetyLabel(force-pull, allow-update=false): want true (SAFE-03 carve-out), got false (status=%d)",
			w.Code)
	}
}

// ----------------------------------------------------------------------------
// SAFE-03 source-grep gate
// ----------------------------------------------------------------------------

// TestSAFE03_PollIgnoresActionLabels asserts the source-grep invariant
// that internal/poll/poller.go does NOT reference any hmi-update.allow-*
// label. SAFE-03 specifies that the poll loop continues to tick for
// containers whose Update/Rollback are server-refused — only the action
// middleware honors these labels.
//
// This is a code-grep test (RESEARCH.md lines 1062–1070 has the canonical
// shape). It guards against a future refactor that accidentally moves
// label-checks into the poll loop.
func TestSAFE03_PollIgnoresActionLabels(t *testing.T) {
	t.Parallel()
	data, err := os.ReadFile("../../internal/poll/poller.go")
	if err != nil {
		t.Fatalf("read poll/poller.go: %v", err)
	}
	if bytes.Contains(data, []byte("hmi-update.allow-")) {
		t.Errorf("SAFE-03 violation: internal/poll/poller.go references hmi-update.allow-* — these labels MUST only be honored by internal/actions/middleware.go")
	}
}

// ----------------------------------------------------------------------------
// Pattern K source-grep gate
// ----------------------------------------------------------------------------

// TestMiddlewarePatternK_NoSprintf asserts internal/actions/middleware.go
// contains zero fmt.Sprintf calls. Pattern K (verbatim-constant response
// bodies, T-01-04-03 path-leak guard) forbids body interpolation.
//
// This is a code-grep test against the project source; the acceptance
// criterion's separate `grep -c 'fmt.Sprintf' internal/actions/middleware.go`
// gate verifies the same invariant from the verifier suite.
func TestMiddlewarePatternK_NoSprintf(t *testing.T) {
	t.Parallel()
	data, err := os.ReadFile("middleware.go")
	if err != nil {
		t.Fatalf("read middleware.go: %v", err)
	}
	if bytes.Contains(data, []byte("fmt.Sprintf")) {
		// Filter false-positives from doc-comments mentioning the symbol.
		// Iterate lines and only flag non-comment lines.
		lines := bytes.Split(data, []byte("\n"))
		for i, line := range lines {
			trim := bytes.TrimLeft(line, " \t")
			if strings.HasPrefix(string(trim), "//") {
				continue
			}
			if bytes.Contains(line, []byte("fmt.Sprintf")) {
				t.Errorf("Pattern K violation: middleware.go:%d uses fmt.Sprintf (response body interpolation forbidden): %s",
					i+1, string(line))
			}
		}
	}
}
