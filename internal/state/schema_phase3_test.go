// RED-FIRST per C4. These tests are authored before internal/state/schema.go
// has the Phase 3 fields (AvailableDigest, LastPolledAt, Notes on Container;
// LastPollStart, LastPollEnd, LastPollError on State). Plan 03-01 Task 1
// drives them green by adding the six new fields with omitempty + time
// import.
//
// What these tests guard:
//   - TestPhase3SchemaFields_RoundTrip_Container: DETECT-05/07/09 — every new
//     per-container field round-trips through JSON Marshal -> Unmarshal with
//     no value loss; an empty container marshals with the three new keys
//     ABSENT (omitempty invariant), so pre-existing on-disk payloads stay
//     compact.
//   - TestPhase3SchemaFields_RoundTrip_State: OBS-04 audit-surface fields
//     (LastPollStart/End/Error) round-trip; zero-valued state marshals
//     without the three new top-level keys (omitempty invariant).
//   - TestPhase3SchemaFields_ForwardCompat_Phase2OnDisk: load-bearing for
//     STATE-03 forward-compat — a literal pre-Phase-3 JSON document
//     (Phase 2 shape: only version + containers with the original 10
//     fields) must unmarshal into the new State with the six new fields
//     at zero values. This is the boot-from-stale-disk path on a real
//     HMI mid-upgrade.
//
// These tests will FAIL to compile until Task 1 lands the new fields.
package state

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// TestPhase3SchemaFields_RoundTrip_Container drives DETECT-05, DETECT-07, DETECT-09:
// every new per-container field carries data through JSON unchanged, and the
// omitempty invariant keeps zero-valued payloads compact.
func TestPhase3SchemaFields_RoundTrip_Container(t *testing.T) {
	t.Parallel()

	// A representative time value (not the zero value) — RFC3339Nano survives
	// json.Marshal/Unmarshal with full nanosecond precision in Go's stdlib.
	polledAt := time.Date(2026, 5, 14, 12, 0, 0, 123456789, time.UTC)

	original := Container{
		Service:         "svc1",
		Image:           "ghcr.io/centroid-is/centroidx-backend",
		Tag:             "latest",
		CurrentDigest:   "sha256:abc",
		AvailableDigest: "sha256:def",
		LastPolledAt:    polledAt,
		Notes:           "pinned: opt-out",
	}

	raw, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var round Container
	if err := json.Unmarshal(raw, &round); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if round.AvailableDigest != original.AvailableDigest {
		t.Errorf("AvailableDigest: want %q, got %q", original.AvailableDigest, round.AvailableDigest)
	}
	if !round.LastPolledAt.Equal(original.LastPolledAt) {
		t.Errorf("LastPolledAt: want %v, got %v", original.LastPolledAt, round.LastPolledAt)
	}
	if round.Notes != original.Notes {
		t.Errorf("Notes: want %q, got %q", original.Notes, round.Notes)
	}

	// omitempty invariant — zero-valued Container must NOT serialize the
	// three new keys at all. This keeps the wire payload for the 95% case
	// (just-discovered, never-polled, no notes) byte-compact.
	empty := Container{Service: "foo"}
	emptyRaw, err := json.Marshal(empty)
	if err != nil {
		t.Fatalf("Marshal(empty): %v", err)
	}
	emptyStr := string(emptyRaw)
	for _, key := range []string{"available_digest", "last_polled_at", "notes"} {
		if strings.Contains(emptyStr, key) {
			t.Errorf("omitempty broken: zero-value Container payload contained %q. Payload: %s", key, emptyStr)
		}
	}
}

// TestPhase3SchemaFields_RoundTrip_State drives OBS-04 audit-surface fields.
// LastPollStart/End/Error round-trip; zero-valued State omits all three.
func TestPhase3SchemaFields_RoundTrip_State(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, 5, 14, 12, 0, 0, 0, time.UTC)
	end := time.Date(2026, 5, 14, 12, 0, 4, 200_000_000, time.UTC)

	original := State{
		Version:       1,
		Containers:    map[string]Container{},
		LastPollStart: start,
		LastPollEnd:   end,
		LastPollError: "timeout",
	}

	raw, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var round State
	if err := json.Unmarshal(raw, &round); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if !round.LastPollStart.Equal(original.LastPollStart) {
		t.Errorf("LastPollStart: want %v, got %v", original.LastPollStart, round.LastPollStart)
	}
	if !round.LastPollEnd.Equal(original.LastPollEnd) {
		t.Errorf("LastPollEnd: want %v, got %v", original.LastPollEnd, round.LastPollEnd)
	}
	if round.LastPollError != original.LastPollError {
		t.Errorf("LastPollError: want %q, got %q", original.LastPollError, round.LastPollError)
	}

	// Set only LastPollError — verify last_poll_error key present but
	// last_poll_start / last_poll_end omitted (omitempty on time.Time zero
	// value).
	partial := State{
		Version:       1,
		Containers:    map[string]Container{},
		LastPollError: "timeout",
	}
	partialRaw, err := json.Marshal(partial)
	if err != nil {
		t.Fatalf("Marshal(partial): %v", err)
	}
	partialStr := string(partialRaw)
	if !strings.Contains(partialStr, `"last_poll_error":"timeout"`) {
		t.Errorf(`partial State payload should contain "last_poll_error":"timeout", got: %s`, partialStr)
	}
	for _, key := range []string{"last_poll_start", "last_poll_end"} {
		if strings.Contains(partialStr, key) {
			t.Errorf("omitempty broken: partial State (zero-valued time) contained %q. Payload: %s", key, partialStr)
		}
	}
}

// TestPhase3SchemaFields_ForwardCompat_Phase2OnDisk loads a literal pre-Phase-3
// JSON document (Phase 2 shape — only the 10 original Container fields plus
// version + containers at the top) and verifies the six new fields land at
// zero values. This guards the boot-from-stale-disk path: an operator
// upgrades from a Phase 2 build to a Phase 3 build without wiping
// hmi_update_state.json.
func TestPhase3SchemaFields_ForwardCompat_Phase2OnDisk(t *testing.T) {
	t.Parallel()

	// Literal byte-for-byte Phase 2 on-disk shape. No Phase 3 keys present.
	phase2JSON := []byte(`{
	  "version": 1,
	  "containers": {
	    "foo": {
	      "service": "foo",
	      "image": "ghcr.io/centroid-is/centroidx-backend",
	      "tag": "latest",
	      "current_digest": "sha256:abc",
	      "update_available": false,
	      "container_id": "deadbeefcafe",
	      "labels": {"hmi-update.watch": "true"}
	    }
	  }
	}`)

	var st State
	if err := json.Unmarshal(phase2JSON, &st); err != nil {
		t.Fatalf("Unmarshal Phase 2 JSON into Phase 3 State: %v", err)
	}

	// Top-level new fields must be zero.
	if !st.LastPollStart.IsZero() {
		t.Errorf("LastPollStart on Phase 2 doc: want zero, got %v", st.LastPollStart)
	}
	if !st.LastPollEnd.IsZero() {
		t.Errorf("LastPollEnd on Phase 2 doc: want zero, got %v", st.LastPollEnd)
	}
	if st.LastPollError != "" {
		t.Errorf("LastPollError on Phase 2 doc: want empty, got %q", st.LastPollError)
	}

	c, ok := st.Containers["foo"]
	if !ok {
		t.Fatalf("Containers[\"foo\"] missing after Phase 2 doc unmarshal")
	}
	// Phase 2 fields preserved.
	if c.Service != "foo" {
		t.Errorf("Service: want %q, got %q", "foo", c.Service)
	}
	if c.CurrentDigest != "sha256:abc" {
		t.Errorf("CurrentDigest: want sha256:abc, got %q", c.CurrentDigest)
	}
	// Phase 3 fields zero.
	if c.AvailableDigest != "" {
		t.Errorf("AvailableDigest on Phase 2 doc: want empty, got %q", c.AvailableDigest)
	}
	if !c.LastPolledAt.IsZero() {
		t.Errorf("LastPolledAt on Phase 2 doc: want zero, got %v", c.LastPolledAt)
	}
	if c.Notes != "" {
		t.Errorf("Notes on Phase 2 doc: want empty, got %q", c.Notes)
	}
}
