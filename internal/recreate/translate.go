// Package recreate implements the socket-only container recreate
// primitive that Phase 9 (a) introduced to replace the deleted
// compose.Runner subprocess. The package has two files:
//
//   - translate.go (this file) — pure data transform from the daemon's
//     ContainerInspect response into the Create-side Config / HostConfig /
//     NetworkingConfig + extras map.
//   - recreate.go — composes the Stop → Remove → Create → NetworkConnect
//     → Start sequence around Translate.
//
// Translate is intentionally a pure function (no daemon calls, no
// os.Getenv, no file I/O) so it can be exhaustively unit-tested without
// fixtures. See translate_test.go for one test case per row of the
// translation table in 09-RESEARCH.md § Architecture Patterns / Pattern 2.
//
// Import discipline: this is the ONE legitimate exception (alongside
// internal/docker and translate_test.go) to the "no moby/moby/api/types
// imports outside internal/docker" gate — translate.go must construct
// the SDK's Config / HostConfig / NetworkingConfig / EndpointSettings
// shapes that the docker.Client facade does NOT re-export at the
// constituent-type level. The Phase 9 plan explicitly accepts this
// scope expansion for the recreate package.
package recreate

import (
	"net/netip"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
)

// shortIDLen is the canonical short-form length the docker daemon uses
// for auto-generated network aliases (the first 12 hex chars of the
// container's full ID). Gotcha #4 in the RESEARCH.md translation table:
// compose strips this alias on recreate and we mirror that behavior so
// the alias list does not accumulate every recreated container's history.
const shortIDLen = 12

// Translate converts a daemon InspectResponse into the four Create-side
// inputs required by docker.ContainerCreate + NetworkConnect (extras).
//
// Returned tuple:
//
//	cfg       — *container.Config        passed verbatim into Create
//	hostCfg   — *container.HostConfig    passed verbatim into Create (Binds, Mounts, etc.)
//	netCfg    — *network.NetworkingConfig
//	            EndpointsConfig has at most ONE entry (the first network in
//	            iteration order over inspect.NetworkSettings.Networks). Pre-API-1.44
//	            daemons reject multi-endpoint Create; the safer "Create with first,
//	            NetworkConnect the rest" pattern (RESEARCH.md Pattern 1) covers all
//	            engine versions back to Docker 1.21.
//	extraNets — map[string]*network.EndpointSettings
//	            The SECOND-AND-LATER networks. recreate.Service calls
//	            NetworkConnect once per extras entry after Create succeeds.
//
// Translate is a PURE function: no daemon I/O, no env reads, no clock
// reads. The corresponding unit test
// TestRecreate_NoComposeProjectNameEnvDependency confirms no
// os.Getenv("COMPOSE_PROJECT_NAME") creeps in via a future "convenience"
// import — Translate's input is the InspectResponse, and that is the
// only input.
//
// Translation table is canonical at 09-RESEARCH.md § Pattern 2; every
// gotcha number in the comments below cross-references that table.
func Translate(inspect container.InspectResponse) (
	*container.Config,
	*container.HostConfig,
	*network.NetworkingConfig,
	map[string]*network.EndpointSettings,
) {
	cfg := translateConfig(inspect)
	hostCfg := translateHostConfig(inspect)
	netCfg, extras := translateNetworks(inspect)
	return cfg, hostCfg, netCfg, extras
}

// translateConfig copies inspect.Config (whole struct) and applies the
// Healthcheck preservation rule per gotcha #2:
//
//   - inspect.Config.Healthcheck == nil        → output .Healthcheck = nil
//     ("no healthcheck override")
//   - inspect.Config.Healthcheck = &HC{Test: nil} → preserve as a non-nil
//     pointer with Test == nil ("use image's HEALTHCHECK")
//   - inspect.Config.Healthcheck = &HC{Test: T}   → preserve as non-nil
//     pointer with same Test
//
// Config.Image, Config.User, Config.Env, Config.Labels all pass through
// verbatim — Pattern 2 rows 11, 12 (synthetic), 14 (compose+hmi-update
// label namespaces) and inspect.Config.Env passthrough.
//
// If inspect.Config is nil (unusual but defensively guarded — a daemon
// that returned a 0-byte body would surface this), return a zero
// *container.Config rather than panicking.
func translateConfig(inspect container.InspectResponse) *container.Config {
	if inspect.Config == nil {
		return &container.Config{}
	}
	// Copy the struct value so the caller cannot mutate the daemon's
	// returned pointer. Healthcheck deep-copy preserves the nil-vs-empty
	// distinction per gotcha #2 (we already preserve via pointer copy;
	// no special handling needed since *HealthConfig is the same pointer
	// the daemon emitted).
	out := *inspect.Config
	return &out
}

// translateHostConfig copies inspect.HostConfig and applies the two
// normalizations the Create endpoint requires:
//
//   - RestartPolicy.Name empty-string → "no" per gotcha #1 (some Engine
//     versions reject empty restart-policy names; container.RestartPolicyDisabled
//     is the typed constant for the string "no").
//
//   - Init pointer-tri-state preserved as-is per row 12 — nil = daemon
//     default, &false = explicit off, &true = explicit on. We do NOT
//     flatten by accident; the struct copy preserves the pointer.
//
// Binds, Mounts, NetworkMode, Resources are all daemon-resolved and
// pass through verbatim (rows 3, 4, 5, embedded Resources). HostConfig.Binds
// in particular carries ABSOLUTE host paths post-daemon-resolution —
// this is the unit-level fix for SC-2 (b) / SC-6 (iii) (relative-bind-mount).
func translateHostConfig(inspect container.InspectResponse) *container.HostConfig {
	if inspect.HostConfig == nil {
		return &container.HostConfig{
			RestartPolicy: container.RestartPolicy{Name: container.RestartPolicyDisabled},
		}
	}
	out := *inspect.HostConfig
	if out.RestartPolicy.Name == "" {
		out.RestartPolicy.Name = container.RestartPolicyDisabled
	}
	return &out
}

// translateNetworks splits inspect.NetworkSettings.Networks into the
// (NetworkingConfig.EndpointsConfig, extras) tuple per gotcha #3.
//
// The first network in iteration order goes into NetworkingConfig (so
// Create has a network to attach to immediately); subsequent networks
// land in extras for recreate.Service to wire up via NetworkConnect.
//
// Map iteration order in Go is randomized, but the test contracts only
// care that:
//
//	(a) NetworkingConfig has exactly ONE entry (regardless of which
//	    one wins the iteration race);
//	(b) NetworkingConfig.EndpointsConfig and extras are DISJOINT
//	    (no double-attach risk on the recreate.Service side);
//	(c) the combined size equals len(inspect.NetworkSettings.Networks).
//
// All three are asserted by translate_test.go (TestTranslate_NetworkSettings_*).
//
// For each EndpointSettings (whether in NetworkingConfig or extras) we
// apply two scrubs:
//
//   - Filter the short-ID alias per gotcha #4 (the daemon auto-adds the
//     container's first 12 hex chars as an alias; compose strips it on
//     recreate and so do we).
//   - Strip daemon-auto-assigned IPs per gotcha #5: if IPAMConfig is nil
//     (operator did NOT pin), do not synthesize an IPAMConfig that would
//     accidentally re-pin the daemon's auto-assigned IPAddress. If IPAMConfig
//     is non-nil (operator pinned), preserve it verbatim.
func translateNetworks(inspect container.InspectResponse) (
	*network.NetworkingConfig,
	map[string]*network.EndpointSettings,
) {
	out := &network.NetworkingConfig{
		EndpointsConfig: map[string]*network.EndpointSettings{},
	}
	extras := map[string]*network.EndpointSettings{}

	if inspect.NetworkSettings == nil || len(inspect.NetworkSettings.Networks) == 0 {
		return out, extras
	}

	shortID := ""
	if len(inspect.ID) >= shortIDLen {
		shortID = inspect.ID[:shortIDLen]
	}

	first := true
	for name, ep := range inspect.NetworkSettings.Networks {
		scrubbed := scrubEndpoint(ep, shortID)
		if first {
			out.EndpointsConfig[name] = scrubbed
			first = false
			continue
		}
		extras[name] = scrubbed
	}
	return out, extras
}

// scrubEndpoint applies the two per-endpoint cleanups from the
// translation table: short-ID alias filtering (gotcha #4) and the
// daemon-auto-assigned-IP guard (gotcha #5). Returns a NEW
// *EndpointSettings so the daemon's returned pointer is not mutated.
//
// A nil input returns a non-nil empty *EndpointSettings — defensive
// against a malformed inspect payload; the caller (ContainerCreate /
// NetworkConnect) treats an empty EndpointSettings as "default
// attachment".
func scrubEndpoint(ep *network.EndpointSettings, shortID string) *network.EndpointSettings {
	if ep == nil {
		return &network.EndpointSettings{}
	}
	out := *ep // shallow copy; we rebuild the slices below

	// Gotcha #4: filter the short-ID alias. Copy operator aliases into
	// a fresh slice so the daemon's returned slice is not mutated.
	if len(ep.Aliases) > 0 && shortID != "" {
		aliases := make([]string, 0, len(ep.Aliases))
		for _, a := range ep.Aliases {
			if a == shortID {
				continue
			}
			// Some daemon versions emit the FULL short-id form
			// (12 hex chars exactly); others emit a longer prefix if
			// the container's first 12 chars collide with another
			// container. We only filter the canonical 12-char form
			// here — compose's own filter is also exact-string match
			// per the analogous docker/cli#1854 behaviour cited in
			// gotcha #4.
			aliases = append(aliases, a)
		}
		out.Aliases = aliases
	}

	// Gotcha #5: do not re-pin daemon-auto-assigned IPs. If IPAMConfig
	// is nil at inspect time, the operator did NOT pin the IP — leave
	// it nil on the output. If IPAMConfig is non-nil, the operator
	// pinned it (or the daemon emitted it for a CIDR-assigned address)
	// and we preserve it.
	//
	// IPAddress at the top level of EndpointSettings is the daemon's
	// runtime-assigned value (netip.Addr in moby v0.4.1); leaving it
	// populated on the Create side would re-pin the IP if IPAMConfig
	// is also non-nil. We clear it here when IPAMConfig is nil (operator
	// did NOT pin) so the new container gets a fresh daemon-assignment
	// instead of fighting the doomed-but-still-extant old container for
	// the same IP. netip.Addr{} (zero value) is the typed "unset" sentinel.
	if ep.IPAMConfig == nil {
		out.IPAddress = netip.Addr{}
	}

	return &out
}

// Design note (SC-6 ii regression anchor): Translate must remain a PURE
// function. No os.Getenv (especially COMPOSE_PROJECT_NAME), no file I/O,
// no clock reads. The corresponding test
// TestRecreate_NoComposeProjectNameEnvDependency asserts the behavioural
// invariant: Translate with an empty input env must not synthesize a
// COMPOSE_PROJECT_NAME entry in Config.Env. If a future contributor
// reaches for os.Getenv to inject COMPOSE_PROJECT_NAME, they will see
// this comment and reconsider.
