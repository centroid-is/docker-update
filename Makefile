.PHONY: types check-types

# Regenerate ui/src/lib/types.d.ts from internal/api/types.go.
types:
	tygo generate

# CI fail-on-diff: regenerate, then refuse to proceed if there is drift.
# Per RESEARCH.md tygo has no --check flag; git diff --exit-code is the canonical pattern.
check-types: types
	@git diff --exit-code ui/src/lib/types.d.ts || \
	  (echo "ERROR: types.d.ts is out of date. Run 'make types' and commit." && exit 1)
