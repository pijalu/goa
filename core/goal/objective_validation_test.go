package goal

import (
	"strings"
	"testing"
)

func assertObjectiveCase(t *testing.T, objective string, wantErr bool) {
	t.Helper()
	err := ValidateObjective(objective)
	if wantErr && err == nil {
		t.Fatal("expected rejection, got nil")
	}
	if !wantErr && err != nil {
		t.Fatalf("unexpected rejection: %v", err)
	}
	if wantErr {
		for _, text := range []string{"markdown", "4000"} {
			if !strings.Contains(err.Error(), text) {
				t.Errorf("rejection must state %q: %v", text, err)
			}
		}
	}
}
