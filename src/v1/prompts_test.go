package main

import (
	"bufio"
	"errors"
	"strings"
	"testing"
)

// These tests swap the package-level reader and nonInteractive flag, so they
// must not run in parallel.

func withInput(t *testing.T, input string, nonInteractiveMode bool) {
	t.Helper()
	oldReader, oldMode := reader, nonInteractive
	reader = bufio.NewReader(strings.NewReader(input))
	nonInteractive = nonInteractiveMode
	t.Cleanup(func() { reader, nonInteractive = oldReader, oldMode })
}

func TestReadLine(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr error
	}{
		{"answer with newline", "yes\n", "yes", nil},
		{"answer is trimmed", "  dominion  \n", "dominion", nil},
		{"empty answer is not an error", "\n", "", nil},
		{"last line without newline", "y", "y", nil},
		{"closed input", "", "", errInputClosed},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			withInput(t, tt.input, false)
			got, err := readLine()
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("expected error %v, got %v", tt.wantErr, err)
			}
			if got != tt.want {
				t.Errorf("expected %q, got %q", tt.want, got)
			}
		})
	}
}

func TestReadLine_ClosedAfterAnswers(t *testing.T) {
	withInput(t, "1\n", false)
	if got, err := readLine(); err != nil || got != "1" {
		t.Fatalf("expected first answer \"1\", got %q, %v", got, err)
	}
	if _, err := readLine(); !errors.Is(err, errInputClosed) {
		t.Errorf("expected errInputClosed once answers run out, got %v", err)
	}
}

func TestReadLine_NonInteractiveNeverReads(t *testing.T) {
	withInput(t, "y\n", true)
	if _, err := readLine(); !errors.Is(err, errNonInteractive) {
		t.Fatalf("expected errNonInteractive, got %v", err)
	}
	// The pending answer must still be unread.
	nonInteractive = false
	if got, _ := readLine(); got != "y" {
		t.Errorf("expected the input to be left untouched, got %q", got)
	}
}
