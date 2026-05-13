package state

// persist is a temporary stub. The real implementation — renameio.WriteFile
// + an explicit parent-directory fsync wrapper per research correction A5
// (Pitfall 7, renameio issue #11) — is written by Task 2 of plan 01-02 in
// the next commit. This stub exists only to make `go build
// ./internal/state/...` succeed for Task 1's verify gate; it does not
// satisfy STATE-02 atomicity and the contention test will fail against it.
func (s *Store) persist() error {
	return nil
}
