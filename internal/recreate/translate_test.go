// Package recreate (test-only seed).
//
// RED-FIRST per C4 (CLAUDE.md): this file is the unit-level regression
// guard for Phase 9 (a) — the InspectResponse → ContainerCreate
// translation table from 09-RESEARCH.md Pattern 2.
//
// At the moment 09-02 lands, NO production code exists in this package.
// The plan's intent is that:
//
//  1. `go test ./internal/recreate/...` fails with "undefined: Translate"
//     (or a similar build-time error). That is the RED state.
//  2. Plan 09-03 lands `internal/recreate/translate.go` whose `Translate`
//     function turns every test in this file GREEN one-by-one.
//
// The tests here exercise EVERY row in 09-RESEARCH.md § Pattern 2 (the
// 13-row translation table) so a regression in any single field surfaces
// at unit-test speed (sub-second) rather than at the next manual smoke on
// the elevator-hmi.
//
// SC mapping (see 09-VALIDATION.md):
//   - SC-2 (a) — TestTranslate_HostConfig_Binds_AbsoluteAfterDaemonResolution
//   - SC-6 (ii) — TestRecreate_NoComposeProjectNameEnvDependency
//   - SC-6 (iii) — TestTranslate_HostConfig_Binds_AbsoluteAfterDaemonResolution
//                  (unit-level companion to e2e/relative-bind-mount.spec.ts)
//
// Expected signature (per RESEARCH.md Pattern 2 — Plan 09-03 lands this):
//
//	func Translate(inspect container.InspectResponse) (
//	    *container.Config,
//	    *container.HostConfig,
//	    *network.NetworkingConfig,
//	    map[string]*network.EndpointSettings,
//	)
//
// Import discipline: tests use github.com/moby/moby/api/types/{container,
// network,mount} directly because they fixture daemon InspectResponse
// shapes. This is the ONE legitimate exception to the
// "no moby/moby imports outside internal/docker" CI gate — translate_test.go
// has to construct realistic InspectResponse fixtures, and the
// internal/docker facade only re-exports the result-wrapper types, not the
// shape types.
package recreate

import (
	"net/netip"
	"os"
	"testing"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/api/types/network"
)

// ----------------------------------------------------------------------------
// Test 1: HostConfig.Binds is passed through verbatim — Translation Table
// row 3. This is the unit-level fix for the (b)/relative-bind-mount bug
// chain (BUG-7 family). On the elevator-hmi, flutter's compose service has
// `./wayland-socket:/run/wayland` in its volumes. Compose tells the daemon
// the absolute resolved host path; the daemon stores it verbatim in
// HostConfig.Binds. When recreate.Translate passes that path back into
// ContainerCreate, the recreated container gets the SAME absolute path —
// no docker-update CWD substitution.
//
// SC-2(a) gate + SC-6(iii) unit-level companion.
// ----------------------------------------------------------------------------

func TestTranslate_HostConfig_Binds_AbsoluteAfterDaemonResolution(t *testing.T) {
	t.Parallel()
	inspect := container.InspectResponse{
		Config: &container.Config{Image: "flutter:latest"},
		HostConfig: &container.HostConfig{
			Binds: []string{"/home/centroid/wayland-socket:/run/wayland"},
		},
	}
	_, hc, _, _ := Translate(inspect)
	if hc == nil {
		t.Fatalf("Translate returned nil HostConfig; want non-nil")
	}
	if len(hc.Binds) != 1 {
		t.Fatalf("HostConfig.Binds len: want 1, got %d (%v)", len(hc.Binds), hc.Binds)
	}
	want := "/home/centroid/wayland-socket:/run/wayland"
	if hc.Binds[0] != want {
		t.Errorf("HostConfig.Binds[0]: want %q (absolute host path verbatim — no CWD resolution), got %q", want, hc.Binds[0])
	}
}

// ----------------------------------------------------------------------------
// Test 2: HostConfig.Mounts (structured form — mount.Mount{Type,Source,Target})
// passes through verbatim — Translation Table row 4.
// ----------------------------------------------------------------------------

func TestTranslate_HostConfig_Mounts_PassThrough(t *testing.T) {
	t.Parallel()
	inspect := container.InspectResponse{
		Config: &container.Config{Image: "x:latest"},
		HostConfig: &container.HostConfig{
			Mounts: []mount.Mount{
				{Type: mount.TypeBind, Source: "/host/path", Target: "/container/path"},
			},
		},
	}
	_, hc, _, _ := Translate(inspect)
	if hc == nil {
		t.Fatalf("nil HostConfig")
	}
	if len(hc.Mounts) != 1 {
		t.Fatalf("Mounts len: want 1, got %d", len(hc.Mounts))
	}
	got := hc.Mounts[0]
	if got.Type != mount.TypeBind {
		t.Errorf("Mounts[0].Type: want bind, got %q", got.Type)
	}
	if got.Source != "/host/path" {
		t.Errorf("Mounts[0].Source: want /host/path, got %q", got.Source)
	}
	if got.Target != "/container/path" {
		t.Errorf("Mounts[0].Target: want /container/path, got %q", got.Target)
	}
}

// ----------------------------------------------------------------------------
// Test 3: HostConfig.NetworkMode passes through verbatim — Translation
// Table row 5. Table-driven over the four canonical values (host, bridge,
// container:<id>, custom-name).
// ----------------------------------------------------------------------------

func TestTranslate_HostConfig_NetworkMode_PassThrough(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		mode container.NetworkMode
	}{
		{"host", container.NetworkMode("host")},
		{"bridge", container.NetworkMode("bridge")},
		{"container-bind", container.NetworkMode("container:abc123")},
		{"custom-network", container.NetworkMode("hmi_default")},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			inspect := container.InspectResponse{
				Config: &container.Config{Image: "x:latest"},
				HostConfig: &container.HostConfig{
					NetworkMode: tc.mode,
				},
			}
			_, hc, _, _ := Translate(inspect)
			if hc == nil {
				t.Fatalf("nil HostConfig")
			}
			if hc.NetworkMode != tc.mode {
				t.Errorf("NetworkMode: want %q, got %q", tc.mode, hc.NetworkMode)
			}
		})
	}
}

// ----------------------------------------------------------------------------
// Test 4: RestartPolicy with empty-string Name is normalized to "no" —
// Translation Table row 6 / gotcha #1.
//
// RESEARCH.md: "If inspect.HostConfig.RestartPolicy.Name == "" some Engine
// versions reject it; normalize to RestartPolicyDisabled (string 'no')."
// ----------------------------------------------------------------------------

func TestTranslate_HostConfig_RestartPolicy_EmptyNameNormalizedToNo(t *testing.T) {
	t.Parallel()
	inspect := container.InspectResponse{
		Config: &container.Config{Image: "x:latest"},
		HostConfig: &container.HostConfig{
			RestartPolicy: container.RestartPolicy{Name: container.RestartPolicyMode("")},
		},
	}
	_, hc, _, _ := Translate(inspect)
	if hc == nil {
		t.Fatalf("nil HostConfig")
	}
	if hc.RestartPolicy.Name != container.RestartPolicyDisabled {
		t.Errorf("RestartPolicy.Name: want %q (normalised from empty per gotcha #1), got %q",
			container.RestartPolicyDisabled, hc.RestartPolicy.Name)
	}
}

// ----------------------------------------------------------------------------
// Test 5: Healthcheck preserves the nil-vs-empty-Test distinction —
// Translation Table row 7 / gotcha #2.
//
// Three sub-cases:
//   - inspect.Config.Healthcheck == nil → out.Healthcheck == nil
//     ("no healthcheck override")
//   - inspect.Config.Healthcheck = &HealthConfig{Test: nil} → out has
//     a non-nil pointer with Test == nil ("use image's HEALTHCHECK")
//   - inspect.Config.Healthcheck = &HealthConfig{Test: [...]} → out
//     has same non-nil pointer with same Test
// ----------------------------------------------------------------------------

func TestTranslate_Config_Healthcheck_PreservesNilVsEmptyDistinction(t *testing.T) {
	t.Parallel()

	t.Run("nil-pointer", func(t *testing.T) {
		t.Parallel()
		inspect := container.InspectResponse{
			Config: &container.Config{Image: "x:latest", Healthcheck: nil},
		}
		cfg, _, _, _ := Translate(inspect)
		if cfg == nil {
			t.Fatalf("nil Config")
		}
		if cfg.Healthcheck != nil {
			t.Errorf("Healthcheck: want nil (no override), got %+v", cfg.Healthcheck)
		}
	})

	t.Run("struct-with-nil-test", func(t *testing.T) {
		t.Parallel()
		inspect := container.InspectResponse{
			Config: &container.Config{
				Image:       "x:latest",
				Healthcheck: &container.HealthConfig{Test: nil},
			},
		}
		cfg, _, _, _ := Translate(inspect)
		if cfg == nil {
			t.Fatalf("nil Config")
		}
		if cfg.Healthcheck == nil {
			t.Fatalf("Healthcheck: want non-nil pointer (use image's HEALTHCHECK), got nil")
		}
		if cfg.Healthcheck.Test != nil {
			t.Errorf("Healthcheck.Test: want nil (preserved), got %v", cfg.Healthcheck.Test)
		}
	})

	t.Run("struct-with-cmd-test", func(t *testing.T) {
		t.Parallel()
		test := []string{"CMD", "true"}
		inspect := container.InspectResponse{
			Config: &container.Config{
				Image:       "x:latest",
				Healthcheck: &container.HealthConfig{Test: test},
			},
		}
		cfg, _, _, _ := Translate(inspect)
		if cfg == nil {
			t.Fatalf("nil Config")
		}
		if cfg.Healthcheck == nil {
			t.Fatalf("Healthcheck: want non-nil, got nil")
		}
		if len(cfg.Healthcheck.Test) != 2 || cfg.Healthcheck.Test[0] != "CMD" || cfg.Healthcheck.Test[1] != "true" {
			t.Errorf("Healthcheck.Test: want [CMD true], got %v", cfg.Healthcheck.Test)
		}
	})
}

// ----------------------------------------------------------------------------
// Test 6: NetworkingConfig is populated from the FIRST entry in
// inspect.NetworkSettings.Networks — Translation Table row 8 / gotcha #3.
//
// Pre-Engine-API-1.44 daemons reject multi-endpoint NetworkingConfig in
// Create; the safer pattern is "Create with first network, NetworkConnect
// the rest." Plan 09-03's recreate.Service will call ContainerCreate with
// the first network and then NetworkConnect for each additional one.
//
// We assert: returned NetworkingConfig.EndpointsConfig has exactly ONE key
// (regardless of how many networks are in the inspect fixture).
// ----------------------------------------------------------------------------

func TestTranslate_NetworkSettings_FirstNetworkInNetworkingConfig(t *testing.T) {
	t.Parallel()
	inspect := container.InspectResponse{
		Config: &container.Config{Image: "x:latest"},
		NetworkSettings: &container.NetworkSettings{
			Networks: map[string]*network.EndpointSettings{
				"net-a": {Aliases: []string{"svc-a"}},
				"net-b": {Aliases: []string{"svc-a-b"}},
			},
		},
	}
	_, _, nc, _ := Translate(inspect)
	if nc == nil {
		t.Fatalf("NetworkingConfig: want non-nil, got nil")
	}
	if got := len(nc.EndpointsConfig); got != 1 {
		t.Errorf("EndpointsConfig: want exactly 1 entry (first-network-in-Create per gotcha #3), got %d (%v)", got, keysOf(nc.EndpointsConfig))
	}
}

// ----------------------------------------------------------------------------
// Test 7: Extra networks (second-and-later) come back as the fourth return
// value so the caller can NetworkConnect them after ContainerCreate.
//
// Two-network fixture: one network goes into NetworkingConfig (above),
// the SECOND comes back in the extras map. Translation Table row 8.
// ----------------------------------------------------------------------------

func TestTranslate_NetworkSettings_ExtraNetworksReturnedSeparately(t *testing.T) {
	t.Parallel()
	inspect := container.InspectResponse{
		Config: &container.Config{Image: "x:latest"},
		NetworkSettings: &container.NetworkSettings{
			Networks: map[string]*network.EndpointSettings{
				"net-a": {Aliases: []string{"svc-a"}},
				"net-b": {Aliases: []string{"svc-a-b"}},
			},
		},
	}
	_, _, nc, extras := Translate(inspect)
	if nc == nil {
		t.Fatalf("nil NetworkingConfig")
	}
	if got := len(nc.EndpointsConfig) + len(extras); got != 2 {
		t.Errorf("first-network + extras totals 2 networks (got %d); EndpointsConfig=%v extras=%v",
			got, keysOf(nc.EndpointsConfig), keysOf(extras))
	}
	if len(extras) != 1 {
		t.Errorf("extras: want exactly 1 (second-and-later networks for NetworkConnect), got %d (%v)",
			len(extras), keysOf(extras))
	}
	// The extras map and NetworkingConfig must be disjoint — same network
	// in both would double-attach when the caller iterates.
	for k := range nc.EndpointsConfig {
		if _, dup := extras[k]; dup {
			t.Errorf("network %q present in BOTH NetworkingConfig and extras (double-attach risk)", k)
		}
	}
}

// ----------------------------------------------------------------------------
// Test 8: Aliases pass through but the auto-generated short-ID alias is
// FILTERED — Translation Table row 9 / gotcha #4.
//
// The daemon auto-adds the container's short-ID (12 hex chars) as an
// alias on every endpoint. Compose strips it on recreate; we should too,
// otherwise the alias list pollutes every recreated container.
// ----------------------------------------------------------------------------

func TestTranslate_FiltersShortIDAlias(t *testing.T) {
	t.Parallel()
	const shortID = "abcd12345678"
	inspect := container.InspectResponse{
		ID:     shortID + "ffffffffffffffffffffffffffff", // longer than short-form
		Config: &container.Config{Image: "x:latest"},
		NetworkSettings: &container.NetworkSettings{
			Networks: map[string]*network.EndpointSettings{
				"net-a": {Aliases: []string{"svc-a", shortID}},
			},
		},
	}
	_, _, nc, _ := Translate(inspect)
	if nc == nil {
		t.Fatalf("nil NetworkingConfig")
	}
	ep, ok := nc.EndpointsConfig["net-a"]
	if !ok {
		t.Fatalf("net-a missing from EndpointsConfig")
	}
	for _, a := range ep.Aliases {
		if a == shortID {
			t.Errorf("Aliases still contain short-ID %q (gotcha #4: must be filtered)", shortID)
		}
	}
	// Operator alias must survive.
	var seenSvcAlias bool
	for _, a := range ep.Aliases {
		if a == "svc-a" {
			seenSvcAlias = true
		}
	}
	if !seenSvcAlias {
		t.Errorf("Aliases missing operator alias 'svc-a'; got %v", ep.Aliases)
	}
}

// ----------------------------------------------------------------------------
// Test 9: Operator-pinned IP (IPAMConfig.IPv4Address set) is preserved —
// Translation Table row 10 / gotcha #5 (positive case).
// ----------------------------------------------------------------------------

func TestTranslate_NetworkSettings_PinnedIPPreserved(t *testing.T) {
	t.Parallel()
	addr := netip.MustParseAddr("10.0.0.42")
	inspect := container.InspectResponse{
		Config: &container.Config{Image: "x:latest"},
		NetworkSettings: &container.NetworkSettings{
			Networks: map[string]*network.EndpointSettings{
				"net-a": {
					IPAMConfig: &network.EndpointIPAMConfig{IPv4Address: addr},
				},
			},
		},
	}
	_, _, nc, _ := Translate(inspect)
	if nc == nil {
		t.Fatalf("nil NetworkingConfig")
	}
	ep, ok := nc.EndpointsConfig["net-a"]
	if !ok {
		t.Fatalf("net-a missing from EndpointsConfig")
	}
	if ep.IPAMConfig == nil {
		t.Fatalf("IPAMConfig: want non-nil (pinned by operator), got nil")
	}
	if got := ep.IPAMConfig.IPv4Address; got != addr {
		t.Errorf("IPAMConfig.IPv4Address: want %s (operator-pinned, preserved), got %s", addr, got)
	}
}

// ----------------------------------------------------------------------------
// Test 10: Daemon-auto-assigned IP (IPAMConfig == nil, IPAddress populated)
// must NOT be re-pinned in the returned NetworkingConfig — Translation Table
// row 10 / gotcha #5 (negative case).
//
// If we accidentally re-pin a daemon-assigned IP, the recreated container
// can't be placed (the same IP is still held by the doomed-but-still-extant
// old container during the recreate window).
// ----------------------------------------------------------------------------

func TestTranslate_NetworkSettings_AutoAssignedIPNotRePinned(t *testing.T) {
	t.Parallel()
	addr := netip.MustParseAddr("10.0.0.42")
	inspect := container.InspectResponse{
		Config: &container.Config{Image: "x:latest"},
		NetworkSettings: &container.NetworkSettings{
			Networks: map[string]*network.EndpointSettings{
				"net-a": {
					IPAMConfig: nil, // operator did NOT pin
					IPAddress:  addr, // daemon auto-assigned
				},
			},
		},
	}
	_, _, nc, _ := Translate(inspect)
	if nc == nil {
		t.Fatalf("nil NetworkingConfig")
	}
	ep, ok := nc.EndpointsConfig["net-a"]
	if !ok {
		t.Fatalf("net-a missing from EndpointsConfig")
	}
	// Either IPAMConfig is nil OR (if non-nil) IPv4Address must be zero.
	// Anything else means we re-pinned the daemon-assigned IP.
	if ep.IPAMConfig != nil && ep.IPAMConfig.IPv4Address.IsValid() {
		t.Errorf("IPAMConfig.IPv4Address: want zero/unset (auto-assigned, not re-pinned), got %s (gotcha #5)",
			ep.IPAMConfig.IPv4Address)
	}
}

// ----------------------------------------------------------------------------
// Test 11: Config.Image is the operator-supplied REFERENCE (e.g.
// "flutter:latest"), NOT the daemon-resolved image ID — Translation Table
// row 11 / gotcha #6.
//
// inspect.Image (top-level) holds the resolved sha256 ID. inspect.Config.Image
// holds the reference the operator wrote in compose. Translate must round-
// trip the REFERENCE (the orchestrator overrides the resolved digest
// post-pull; Translate's job is just to give the right starting point).
// ----------------------------------------------------------------------------

func TestTranslate_Config_Image_NotImageID(t *testing.T) {
	t.Parallel()
	inspect := container.InspectResponse{
		Image: "sha256:abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890",
		Config: &container.Config{
			Image: "flutter:latest",
		},
	}
	cfg, _, _, _ := Translate(inspect)
	if cfg == nil {
		t.Fatalf("nil Config")
	}
	if cfg.Image != "flutter:latest" {
		t.Errorf("Config.Image: want %q (operator reference per gotcha #6), got %q (must NOT be the resolved image ID %q)",
			"flutter:latest", cfg.Image, inspect.Image)
	}
}

// ----------------------------------------------------------------------------
// Test 12: HostConfig.Init pointer-bool tri-state — Translation Table row
// 12. nil, &false, &true must all round-trip distinctly.
// ----------------------------------------------------------------------------

func TestTranslate_HostConfig_Init_PointerTriState(t *testing.T) {
	t.Parallel()
	f := false
	tr := true
	cases := []struct {
		name string
		in   *bool
	}{
		{"nil", nil},
		{"explicit-false", &f},
		{"explicit-true", &tr},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			inspect := container.InspectResponse{
				Config: &container.Config{Image: "x:latest"},
				HostConfig: &container.HostConfig{
					Init: tc.in,
				},
			}
			_, hc, _, _ := Translate(inspect)
			if hc == nil {
				t.Fatalf("nil HostConfig")
			}
			switch {
			case tc.in == nil:
				if hc.Init != nil {
					t.Errorf("Init: want nil, got %+v (flatten-by-accident risk)", hc.Init)
				}
			case tc.in != nil:
				if hc.Init == nil {
					t.Errorf("Init: want non-nil pointer to %v, got nil (flatten-by-accident risk)", *tc.in)
				} else if *hc.Init != *tc.in {
					t.Errorf("*Init: want %v, got %v", *tc.in, *hc.Init)
				}
			}
		})
	}
}

// ----------------------------------------------------------------------------
// Test 13: Labels preserve both compose.* and hmi-update.* namespaces —
// Translation Table row 14 / Pitfall 1.
//
// CRITICAL: the orchestrator's post-recreate lookupContainerIDByService
// filters ContainerList by com.docker.compose.service=<svc>. If the
// recreated container loses that label, the lookup returns zero results
// and every successful recreate surfaces as "no container found for service"
// (the exact bug class Pitfall 1 documents).
// ----------------------------------------------------------------------------

func TestTranslate_Config_Labels_PreservesComposeAndHmiUpdateNamespaces(t *testing.T) {
	t.Parallel()
	in := map[string]string{
		"com.docker.compose.service":              "svc",
		"com.docker.compose.project":              "p",
		"com.docker.compose.project.working_dir":  "/home/operator",
		"hmi-update.watch":                        "true",
	}
	inspect := container.InspectResponse{
		Config: &container.Config{
			Image:  "x:latest",
			Labels: in,
		},
	}
	cfg, _, _, _ := Translate(inspect)
	if cfg == nil {
		t.Fatalf("nil Config")
	}
	for k, want := range in {
		got, ok := cfg.Labels[k]
		if !ok {
			t.Errorf("Labels[%q] missing (Pitfall 1: must be passed through verbatim)", k)
			continue
		}
		if got != want {
			t.Errorf("Labels[%q]: want %q, got %q", k, want, got)
		}
	}
}

// ----------------------------------------------------------------------------
// Test 14: SC-6 (ii) — Translate has NO dependency on the
// COMPOSE_PROJECT_NAME process environment variable.
//
// In the pre-Phase-9 codebase, internal/compose/runner.go shelled out to
// `docker compose up -d --force-recreate <svc>` and the compose CLI relied
// on COMPOSE_PROJECT_NAME (env var OR derived from the compose file's
// directory name) to identify the project. That env-var coupling produced
// the "compose project name collision" bug class (SC-6 ii).
//
// Phase 9 deletes the compose CLI dependency: recreate.Translate is a PURE
// function with input == inspect, output == Create inputs. It must not read
// process env. This test unsets COMPOSE_PROJECT_NAME and asserts:
//
//  1. Translate does not panic.
//  2. No resulting Config.Env entry references COMPOSE_PROJECT_NAME (the
//     inspect fixture has no env, so the output's env must be empty).
//
// Regression guard: if a future Translate implementation accidentally
// imports `os` and reads COMPOSE_PROJECT_NAME, this test catches it.
// ----------------------------------------------------------------------------

func TestRecreate_NoComposeProjectNameEnvDependency(t *testing.T) {
	// NOT t.Parallel — uses t.Setenv which is incompatible with parallel
	// tests in the same package (it sets process state).
	t.Setenv("COMPOSE_PROJECT_NAME", "")
	if v := os.Getenv("COMPOSE_PROJECT_NAME"); v != "" {
		t.Fatalf("seed: COMPOSE_PROJECT_NAME not cleared (got %q)", v)
	}
	inspect := container.InspectResponse{
		Config: &container.Config{
			Image: "x:latest",
			// NO env entries at all — Translate must not synthesise any.
			Env: nil,
		},
		HostConfig: &container.HostConfig{},
	}
	// 1. No panic on empty env.
	cfg, _, _, _ := Translate(inspect)
	if cfg == nil {
		t.Fatalf("nil Config")
	}
	// 2. No COMPOSE_PROJECT_NAME entries in Config.Env (would indicate
	//    Translate is reading process env and injecting it — SC-6 ii
	//    regression).
	for _, e := range cfg.Env {
		// Env entries are "KEY=VALUE" or "KEY" form; check both.
		if e == "COMPOSE_PROJECT_NAME" ||
			len(e) > len("COMPOSE_PROJECT_NAME=") && e[:len("COMPOSE_PROJECT_NAME=")] == "COMPOSE_PROJECT_NAME=" {
			t.Errorf("Config.Env contains COMPOSE_PROJECT_NAME entry %q — Translate must be a pure function, not read process env (SC-6 ii)", e)
		}
	}
}

// ----------------------------------------------------------------------------
// helpers
// ----------------------------------------------------------------------------

func keysOf[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
