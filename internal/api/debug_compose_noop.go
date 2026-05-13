//go:build !debug

package api

// registerDebugRoutes is a no-op in production builds. The debug route
// /debug/compose-stat is registered only when the binary is built with
// `go build -tags=debug` (see debug_compose.go).
//
// We keep this no-op stub in the default build so production binaries
// do not silently miss the route table — the build-tag-gated counterpart
// in debug_compose.go has the same signature, so the compiler picks one
// or the other based on whether `-tags=debug` is passed.
//
// Why a build tag, not an env-var feature flag? An env-var flag would
// keep the handler code (and its compose.Reader.CheckUnchanged call
// path) in the production binary, making it part of the production
// attack surface even when disabled. The build tag excludes the handler
// from `go build ./...` entirely — `grep` over the production binary
// for "/debug/compose-stat" returns no matches. T-02-04-02 mitigation.
func (s *Server) registerDebugRoutes() {}
