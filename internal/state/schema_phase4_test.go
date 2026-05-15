// RED-FIRST per C4. These tests are authored before internal/state/schema.go
// has the Phase 4 fields (ActionInFlight, ActionError on Container). Plan
// 04-01 Task 1 drives them green by adding the two new fields with
// omitempty.
//
// What these tests guard:
//   - TestPhase4SchemaFields_RoundTrip_Container: every new per-container
//     field round-trips through JSON Marshal -> Unmarshal with no value
//     loss; an empty Container marshals with the two new keys ABSENT
//     (omitempty invariant), so pre-existing on-disk payloads stay
//     compact.
//   - TestPhase4SchemaFields_ForwardCompat_Phase3OnDisk: load-bearing for
//     T-04-01-01 (Tampering disposition: mitigate). A literal pre-Phase-4
//     JSON document (Phase 3 shape: only the original 13 Container fields
//     plus version + containers + the three top-level poll-observability
//     keys) must load through state.NewStore with the two new fields at
//     zero values, AND a subsequent no-op state.Store.Update must NOT
//     write action_in_flight / action_error keys back to disk (omitempty
//     proof under the renameio write path).
//
// These tests will FAIL to compile until Task 1 lands the new fields.
package state

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestPhase4SchemaFields_RoundTrip_Container drives the round-trip + omitempty
// invariant for the two new Phase 4 fields.
func TestPhase4SchemaFields_RoundTrip_Container(t *testing.T) {
	t.Parallel()

	original := Container{
		Service:        "svc1",
		Image:          "ghcr.io/centroid-is/centroidx-backend",
		Tag:            "latest",
		CurrentDigest:  "sha256:abc",
		ActionInFlight: "updating",
		ActionError:    "verify_failed: container restarted 3 times in 15s",
	}

	raw, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var round Container
	if err := json.Unmarshal(raw, &round); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if round.ActionInFlight != original.ActionInFlight {
		t.Errorf("ActionInFlight: want %q, got %q", original.ActionInFlight, round.ActionInFlight)
	}
	if round.ActionError != original.ActionError {
		t.Errorf("ActionError: want %q, got %q", original.ActionError, round.ActionError)
	}

	// omitempty invariant — zero-valued Container must NOT serialize the
	// two new keys at all. This keeps the wire payload for the 95% case
	// (idle row, no prior failure) byte-compact.
	empty := Container{Service: "foo"}
	emptyRaw, err := json.Marshal(empty)
	if err != nil {
		t.Fatalf("Marshal(empty): %v", err)
	}
	emptyStr := string(emptyRaw)
	for _, key := range []string{"action_in_flight", "action_error"} {
		if strings.Contains(emptyStr, key) {
			t.Errorf("omitempty broken: zero-value Container payload contained %q. Payload: %s", key, emptyStr)
		}
	}
}

// TestPhase4SchemaFields_ForwardCompat_Phase3OnDisk loads a literal pre-Phase-4
// JSON document (Phase 3 shape) through state.NewStore and verifies the two
// new fields land at zero values. Then a no-op Update is performed and the
// rewritten on-disk file is asserted to OMIT action_in_flight / action_error
// keys entirely (omitempty proof through the renameio write path).
//
// This is the boot-from-stale-disk path: an operator upgrades from a Phase 3
// build to a Phase 4 build without wiping hmi_update_state.json. T-04-01-01
// disposition: mitigate.
func TestPhase4SchemaFields_ForwardCompat_Phase3OnDisk(t *testing.T) {
	t.Parallel()

	// Literal byte-for-byte Phase 3 on-disk shape. No Phase 4 keys present.
	phase3JSON := []byte(`{
	  "version": 1,
	  "containers": {
	    "svc": {
	      "service": "svc",
	      "image": "ghcr.io/centroid-is/centroidx-backend",
	      "tag": "latest",
	      "current_digest": "sha256:abc",
	      "available_digest": "sha256:def",
	      "update_available": true,
	      "container_id": "deadbeefcafe",
	      "labels": {"hmi-update.watch": "true"},
	      "last_polled_at": "2026-05-14T12:00:00.123456789Z",
	      "notes": "pinned: opt-out"
	    }
	  },
	  "last_poll_start": "2026-05-14T12:00:00Z",
	  "last_poll_end": "2026-05-14T12:00:04.2Z"
	}`)

	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	if err := os.WriteFile(path, phase3JSON, 0o600); err != nil {
		t.Fatalf("seed Phase 3 state file: %v", err)
	}

	store, err := NewStore(path)
	if err != nil {
		t.Fatalf("NewStore on Phase 3 doc: %v", err)
	}

	snap := store.Get()
	c, ok := snap.Containers["svc"]
	if !ok {
		t.Fatalf("Containers[\"svc\"] missing after Phase 3 doc load")
	}

	// Phase 3 fields preserved (sanity).
	if c.CurrentDigest != "sha256:abc" {
		t.Errorf("CurrentDigest: want sha256:abc, got %q", c.CurrentDigest)
	}
	if c.AvailableDigest != "sha256:def" {
		t.Errorf("AvailableDigest: want sha256:def, got %q", c.AvailableDigest)
	}
	if c.Notes != "pinned: opt-out" {
		t.Errorf("Notes: want pinned: opt-out, got %q", c.Notes)
	}

	// Phase 4 fields zero.
	if c.ActionInFlight != "" {
		t.Errorf("ActionInFlight on Phase 3 doc: want empty, got %q", c.ActionInFlight)
	}
	if c.ActionError != "" {
		t.Errorf("ActionError on Phase 3 doc: want empty, got %q", c.ActionError)
	}

	// No-op Update — drives the renameio write path; the rewritten file
	// must omit the Phase 4 keys.
	if err := store.Update(func(_ *State) { /* no-op */ }); err != nil {
		t.Fatalf("no-op Update: %v", err)
	}

	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back persisted state: %v", err)
	}
	for _, key := range []string{"action_in_flight", "action_error"} {
		if bytes.Contains(written, []byte(key)) {
			t.Errorf("omitempty broken: rewritten Phase 4 state contained %q. Payload: %s", key, written)
		}
	}
}
