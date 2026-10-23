package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
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
		{"api without config", []string{"api"}, 1, "DATABASE_URL is required"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("DATABASE_URL", "")
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

func TestHealthcheck(t *testing.T) {
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer ok.Close()
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer bad.Close()
	slow := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer slow.Close()

	tests := []struct {
		name string
		args []string
		code int
	}{
		{"2xx", []string{"healthcheck", ok.URL}, 0},
		{"5xx", []string{"healthcheck", bad.URL}, 1},
		{"unreachable", []string{"healthcheck", "http://127.0.0.1:9"}, 1},
		{"no url", []string{"healthcheck"}, 1},
		{"timeout", []string{"healthcheck", slow.URL}, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			start := time.Now()
			if got := run(context.Background(), tt.args, &buf); got != tt.code {
				t.Fatalf("exit = %d, want %d (stderr %q)", got, tt.code, buf.String())
			}
			if tt.code == 0 && buf.Len() != 0 {
				t.Fatalf("expected no output, got %q", buf.String())
			}
			if time.Since(start) > 5*time.Second {
				t.Fatalf("took %v, timeout not applied", time.Since(start))
			}
		})
	}
}
