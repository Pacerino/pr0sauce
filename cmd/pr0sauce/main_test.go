package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestRunExitCodes(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		code    int
		wantErr string
	}{
		{"no args", nil, 2, "usage:"},
		{"unknown", []string{"nope"}, 2, "usage:"},
		{"stub error", []string{"migrate"}, 1, "not implemented"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			if got := run(context.Background(), tt.args, &buf); got != tt.code {
				t.Fatalf("exit = %d, want %d", got, tt.code)
			}
			if !strings.Contains(buf.String(), tt.wantErr) {
				t.Fatalf("stderr %q missing %q", buf.String(), tt.wantErr)
			}
		})
	}
}
