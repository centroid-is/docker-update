// RED-FIRST per C4. These tests are authored before internal/api/types.go
// has the Phase 4 fields. Plan 04-01 Task 1 drives them green by mirroring
// the two new fields from internal/state.Container into the
// tygo-source-of-truth file.
//
// What these tests guard:
//   - TestPhase4Types_StateApiTagParity: the tygo source-of-truth invariant
//     specialized to Phase 4 — api.Container.ActionInFlight and
//     api.Container.ActionError have byte-identical struct tags to their
//     state.Container counterparts. Drift here is the entire reason
//     `make check-types` exists; a unit-level guard catches the
//     regression before the CI step. Uses reflection so BOTH mismatches
//     surface at once (t.Errorf, not t.Fatal).
//   - TestPhase4APITypes_Parity_Container: belt-and-braces — marshal
//     state.Container and api.Container through json with the new fields
//     populated and assert byte-identical output. Catches tag-syntax
//     equality + json-encoder behavior in one shot.
//
// These tests will FAIL to compile until Task 1 lands the new fields.
package api

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/centroid-is/hmi-update/internal/state"
)

// TestPhase4Types_StateApiTagParity asserts that state.Container and
// api.Container's Phase 4 fields have byte-identical struct tags. Uses
// reflection so multiple drifts surface simultaneously.
func TestPhase4Types_StateApiTagParity(t *testing.T) {
	t.Parallel()

	stateT := reflect.TypeOf(state.Container{})
	apiT := reflect.TypeOf(Container{})

	phase4Fields := []string{"ActionInFlight", "ActionError"}
	for _, name := range phase4Fields {
		stateF, ok := stateT.FieldByName(name)
		if !ok {
			t.Errorf("state.Container missing Phase 4 field %q", name)
			continue
		}
		apiF, ok := apiT.FieldByName(name)
		if !ok {
			t.Errorf("api.Container missing Phase 4 field %q", name)
			continue
		}
		if string(stateF.Tag) != string(apiF.Tag) {
			t.Errorf("tygo source-of-truth tag parity broken on %s:\n  state: %q\n  api:   %q",
				name, stateF.Tag, apiF.Tag)
		}
	}
}

// TestPhase4APITypes_Parity_Container marshals state.Container and
// api.Container through json with the new Phase 4 fields populated and
// asserts byte-identical output. Catches both tag drift and encoder
// behavior in one shot.
func TestPhase4APITypes_Parity_Container(t *testing.T) {
	t.Parallel()

	stateC := state.Container{
		Service:        "svc1",
		Image:          "ghcr.io/centroid-is/centroidx-backend",
		Tag:            "latest",
		CurrentDigest:  "sha256:abc",
		ActionInFlight: "updating",
		ActionError:    "verify_failed: container restarted 3 times in 15s",
	}
	apiC := Container{
		Service:        "svc1",
		Image:          "ghcr.io/centroid-is/centroidx-backend",
		Tag:            "latest",
		CurrentDigest:  "sha256:abc",
		ActionInFlight: "updating",
		ActionError:    "verify_failed: container restarted 3 times in 15s",
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
		t.Errorf("tygo source-of-truth parity broken on Phase 4 fields:\n  state: %s\n  api:   %s", stateJSON, apiJSON)
	}
}
