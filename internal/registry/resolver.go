// Package registry resolves the upstream digest of a `:tag` ref against the
// OCI registry it lives in (default GHCR for Centroid HMIs).
//
// Phase 1 ships the interface only; the body lands in phase 3 (DETECT-01..08).
package registry

// Resolver wraps `google/go-containerregistry`'s remote API so plan-04's
// internal/api server can ask "what is the upstream digest of image:tag?"
// without depending on go-containerregistry directly.
//
// TODO(phase-3): implement — multi-arch index resolution and bearer-token flow
// land here.
type Resolver interface{}
