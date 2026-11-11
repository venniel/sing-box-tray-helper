package main

import (
	"strings"
	"testing"
)

func TestCaptureBufferText(t *testing.T) {
	tests := []struct {
		name   string
		writes []string
		want   string
	}{
		{"empty", nil, ""},
		{"no newline", []string{"partial"}, ""},
		{"complete lines", []string{"first\nsecond\n"}, "first\nsecond"},
		{"drops partial tail", []string{"alpha\nbeta"}, "alpha"},
		{"trims blank edges", []string{"\nvalue\n\n"}, "value"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			b := &captureBuffer{}
			for _, write := range tc.writes {
				if _, err := b.Write([]byte(write)); err != nil {
					t.Fatal(err)
				}
			}
			if got := b.text(); got != tc.want {
				t.Errorf("text() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestCaptureBufferWrapsToTail(t *testing.T) {
	b := &captureBuffer{}
	_, _ = b.Write([]byte("dropped\n"))
	_, _ = b.Write([]byte(strings.Repeat("x", maxCaptureBytes)))
	_, _ = b.Write([]byte("\nkept\n"))

	if got := b.text(); got != "kept" {
		t.Errorf("text() = %q, want %q", got, "kept")
	}
}

func TestCaptureBufferOversizedWrite(t *testing.T) {
	b := &captureBuffer{}
	_, _ = b.Write([]byte(strings.Repeat("a", maxCaptureBytes) + "\nlast\n"))

	if got := b.text(); got != "last" {
		t.Errorf("text() = %q, want %q", got, "last")
	}
}

func TestStripSingBoxTimestamp(t *testing.T) {
	cases := []struct {
		line string
		want string
	}{
		{"+0800 2024-01-02 15:04:05 INFO[0001] ready", "INFO[0001] ready"},
		{"-0700 2024-01-02 15:04:05 ERROR boot", "ERROR boot"},
		{"INFO[0001] ready", "INFO[0001] ready"},
		{"+0800 2024-01-02 15:04:05", "+0800 2024-01-02 15:04:05"},
	}

	for _, tc := range cases {
		if got := stripSingBoxTimestamp(tc.line); got != tc.want {
			t.Errorf("stripSingBoxTimestamp(%q) = %q, want %q", tc.line, got, tc.want)
		}
	}
}

func TestClassifyLogEntry(t *testing.T) {
	cases := []struct {
		line string
		want logEntryKind
	}{
		{"TRACE[0000] trace", lekTrace},
		{"DEBUG debug", lekDebug},
		{"INFO[0000] info", lekInfo},
		{"+0800 2024-01-02 15:04:05 WARN[0001] warn", lekWarn},
		{"ERROR[0000] error", lekError},
		{"FATAL[0000] fatal", lekFatal},
		{"PANIC[0000] panic", lekPanic},
		{"panic: boom", lekPanic},
		{"fatal error: boom", lekFatal},
		{"plain text", lekNone},
		{" INFO[0000] indented", lekNone},
	}

	for _, tc := range cases {
		if got := classifyLogEntry(tc.line); got != tc.want {
			t.Errorf("classifyLogEntry(%q) = %v, want %v", tc.line, got, tc.want)
		}
	}
}

func TestExtractCriticalError(t *testing.T) {
	if got := extractCriticalError(""); got != "" {
		t.Errorf("empty input = %q, want empty", got)
	}
	if got := extractCriticalError("INFO[0000] ok\nWARN[0001] hmm"); got != "" {
		t.Errorf("non-critical input = %q, want empty", got)
	}
	if got := extractCriticalError("FATAL[0000] early\nINFO[0001] later"); got != "" {
		t.Errorf("non-critical last entry = %q, want empty", got)
	}

	fatal := "INFO[0000] start\n+0800 2024-01-02 15:04:05 FATAL[0001] cannot start\n  listen tcp: address in use"
	wantFatal := "FATAL[0001] cannot start\r\n  listen tcp: address in use"
	if got := extractCriticalError(fatal); got != wantFatal {
		t.Errorf("fatal entry = %q, want %q", got, wantFatal)
	}

	panicText := "panic: runtime error\n\ngoroutine 1 [running]:\nmain.main()"
	wantPanic := "panic: runtime error\r\n\r\ngoroutine 1 [running]:\r\nmain.main()"
	if got := extractCriticalError(panicText); got != wantPanic {
		t.Errorf("panic entry = %q, want %q", got, wantPanic)
	}
}
