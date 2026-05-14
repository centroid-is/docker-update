// RED-FIRST per C4. These tests are authored before internal/api/types.go
// has the Phase 3 fields. Plan 03-01 Task 2 drives them green by mirroring
// the six new fields from internal/state.Container / .State into the
// tygo-source-of-truth file.
//
// What these tests guard:
//   - TestPhase3APITypes_Parity_Container: the tygo source-of-truth
//     invariant — internal/api.Container and internal/state.Container
//     produce byte-identical JSON for the same logical value. Drift here
//     is the entire reason `make check-types` exists; a unit-level guard
//     catches the regression before the CI step.
//   - TestPhase3APITypes_Parity_State: same invariant for the State
//     top-level fields.
//   - TestPhase3APITypes_OmitZero_TimeFields: api.Container and api.State
//     time.Time fields must use `omitzero` so an un-polled container's
//     wire payload doesn't contain "last_polled_at":"0001-01-01...".
//     This is the Phase 2 forward-compat invariant pushed up to the
//     wire layer.
//
// These tests will FAIL to compile until Task 2 lands the new fields.
package api

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/centroid-is/hmi-update/internal/state"
)

// TestPhase3APITypes_Parity_Container marshals a Container through both
// types and asserts byte-identical JSON. This is the tygo source-of-truth
// invariant (CONTEXT.md "Phase 01 P03 decision: types.go mirrors state
// json tags verbatim — wire/disk schema parity is the load-bearing
// invariant").
func TestPhase3APITypes_Parity_Container(t *testing.T) {
	t.Parallel()

	polledAt := time.Date(2026, 5, 14, 12, 0, 0, 123456789, time.UTC)

	stateC := state.Container{
		Service:         "svc1",
		Image:           "ghcr.io/centroid-is/centroidx-backend",
		Tag:             "latest",
		CurrentDigest:   "sha256:abc",
		AvailableDigest: "sha256:def",
		UpdateAvailable: true,
		ContainerID:     "deadbeefcafe",
		LastPolledAt:    polledAt,
		Notes:           "pinned: opt-out",
	}
	apiC := Container{
		Service:         "svc1",
		Image:           "ghcr.io/centroid-is/centroidx-backend",
		Tag:             "latest",
		CurrentDigest:   "sha256:abc",
		AvailableDigest: "sha256:def",
		UpdateAvailable: true,
		ContainerID:     "deadbeefcafe",
		LastPolledAt:    polledAt,
		Notes:           "pinned: opt-out",
	}

	stateJSON, err := json.Marshal(stateC)
	if err != nil {
		t.Fatalf("Marshal(state.Container): %v", err)
	}
	apiJSON, err := json.Marshal(apiC)
	if err != nil {
		t.Fatalf("Marshal(api.Container): %v", err)
	}

	if string(stateJSON) != string(apiJSON) {
		t.Errorf("tygo source-of-truth parity broken:\n  state: %s\n  api:   %s", stateJSON, apiJSON)
	}
}

// TestPhase3APITypes_Parity_State asserts the same byte-identical-JSON
// invariant for the State top-level fields.
func TestPhase3APITypes_Parity_State(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, 5, 14, 12, 0, 0, 0, time.UTC)
	end := time.Date(2026, 5, 14, 12, 0, 4, 200_000_000, time.UTC)

	stateS := state.State{
		Version:       1,
		Containers:    map[string]state.Container{},
		LastPollStart: start,
		LastPollEnd:   end,
		LastPollError: "timeout",
	}
	apiS := State{
		Version:       1,
		Containers:    map[string]Container{},
		LastPollStart: start,
		LastPollEnd:   end,
		LastPollError: "timeout",
	}

	stateJSON, err := json.Marshal(stateS)
	if err != nil {
		t.Fatalf("Marshal(state.State): %v", err)
	}
	apiJSON, err := json.Marshal(apiS)
	if err != nil {
		t.Fatalf("Marshal(api.State): %v", err)
	}
	if string(stateJSON) != string(apiJSON) {
		t.Errorf("tygo source-of-truth parity broken on State:\n  state: %s\n  api:   %s", stateJSON, apiJSON)
	}
}

// TestPhase3APITypes_OmitZero_TimeFields asserts the api types omit
// zero-valued time fields from the wire payload (the forward-compat
// invariant inherited from state.Container — see schema.go for the
// `omitzero` vs `omitempty` discussion).
func TestPhase3APITypes_OmitZero_TimeFields(t *testing.T) {
	t.Parallel()

	emptyC := Container{Service: "foo"}
	rawC, err := json.Marshal(emptyC)
	if err != nil {
		t.Fatalf("Marshal(empty api.Container): %v", err)
	}
	if strings.Contains(string(rawC), "last_polled_at") {
		t.Errorf("api.Container.LastPolledAt omitzero broken: payload contained 'last_polled_at'. Got: %s", rawC)
	}

	emptyS := State{Version: 1, Containers: map[string]Container{}}
	rawS, err := json.Marshal(emptyS)
	if err != nil {
		t.Fatalf("Marshal(empty api.State): %v", err)
	}
	for _, key := range []string{"last_poll_start", "last_poll_end"} {
		if strings.Contains(string(rawS), key) {
			t.Errorf("api.State omitzero broken: payload contained %q. Got: %s", key, rawS)
		}
	}
}
