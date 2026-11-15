package main

import (
	"errors"
	"testing"
)

func TestFlagMigrationDecision(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		flag      bool
		wantApply bool
		wantErr   error
	}{
		{"flag given applies the migration", true, true, nil},
		{"no flag stops the update", false, false, errMigrationNotApproved},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			apply, err := flagMigrationDecision(tt.flag)()
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("expected error %v, got %v", tt.wantErr, err)
			}
			if apply != tt.wantApply {
				t.Errorf("expected apply=%v, got %v", tt.wantApply, apply)
			}
		})
	}
}
