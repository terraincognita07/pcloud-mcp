package main

import (
	"strings"
	"testing"
)

// TestServerInstructions_DeleteWordingIsAccurate locks the server-level
// instruction against regressing to the old, inaccurate "permanent / cannot be
// undone" claim. Deletions move to pCloud Trash and are recoverable, so the
// guidance must say so — otherwise a host over-warns the user before a routine,
// reversible delete. See serverInstructions in main.go.
func TestServerInstructions_DeleteWordingIsAccurate(t *testing.T) {
	got := strings.ToLower(serverInstructions)

	if !strings.Contains(got, "trash") {
		t.Errorf("server instructions must tell hosts that deletes go to Trash; got: %q", serverInstructions)
	}
	for _, banned := range []string{"cannot be undone", "permanent and", "permanently delete"} {
		if strings.Contains(got, banned) {
			t.Errorf("server instructions overstate delete irreversibility (found %q); deletes are recoverable from Trash", banned)
		}
	}
}
