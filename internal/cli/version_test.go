package cli

import "testing"

func TestVersion(t *testing.T) {
	// Should not error; default values are "dev" / "unknown".
	if err := Version(nil); err != nil {
		t.Fatalf("Version() failed: %v", err)
	}
}
