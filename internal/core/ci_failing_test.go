package core

import "testing"

// Deliberately failing test to prove CI fails (#20). Reverted next commit.
func TestCIDeliberateFailure(t *testing.T) {
	t.Fatal("deliberate failure to verify CI (#20)")
}
