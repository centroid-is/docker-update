package api

// registerDebugRoutes is a no-op in production builds; the build-tag
// gated counterpart in debug_compose.go (Task 2) wires the actual route.
// The full doc comment lives on the Task 2 file.
func (s *Server) registerDebugRoutes() {}
