package ui

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/TTMathCS/g9s/internal/config"
)

// The bug this covers: the guard measured the raw text while the terminal
// limits the escape sequence, which carries base64 — four bytes out for every
// three in. A payload just under the raw limit produced a sequence a third
// larger, the terminal dropped the whole thing without a word, and g9s
// reported "copied" over a clipboard that never changed.
func TestClipboardGuardMeasuresTheEncodedSequence(t *testing.T) {
	// Sized to pass a raw-length check against an 8 KB limit and fail an
	// encoded one: 7 KB in becomes about 9.3 KB of base64.
	text := strings.Repeat("x", 7*1024)

	encoded := len(base64.StdEncoding.EncodeToString([]byte(text))) + osc52Overhead
	if len(text) >= 8*1024 {
		t.Fatal("fixture no longer passes a raw-length check; the test proves nothing")
	}
	if encoded <= 8*1024 {
		t.Fatalf("fixture encodes to %d bytes, which does not exceed the limit", encoded)
	}

	msg, tooBig := clipboardRefusal(text, base64.StdEncoding.EncodeToString([]byte(text)), 8*1024)
	if !tooBig {
		t.Fatal("a payload the terminal would silently drop was accepted")
	}
	if msg.level != flashWarn {
		t.Errorf("refusal reported as %v, want a warning", msg.level)
	}
	if !strings.Contains(msg.text, "too large") {
		t.Errorf("flash = %q, want it to refuse", msg.text)
	}
	// The user cannot fix this without knowing the knob exists.
	if !strings.Contains(msg.text, "clipboard_limit") {
		t.Errorf("flash = %q, want it to name the setting that raises the limit", msg.text)
	}
}

func TestClipboardLimitIsConfigurable(t *testing.T) {
	tests := []struct {
		name       string
		configured int
		want       int
	}{
		{"unset uses the conservative default", 0, defaultClipboardLimit},
		{"raised for a terminal known to take more", 1 << 20, 1 << 20},
		{"negative disables the check", -1, -1},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &config.Config{Defaults: config.Defaults{ClipboardLimit: tc.configured}}
			m := New(cfg, nil)
			if got := m.clipboardLimit(); got != tc.want {
				t.Errorf("clipboardLimit() = %d, want %d", got, tc.want)
			}
		})
	}
}

// A limit of -1 is a deliberate choice to trust the terminal, so the size
// check has to actually get out of the way rather than clamping to a default.
func TestNegativeClipboardLimitSkipsTheCheck(t *testing.T) {
	huge := strings.Repeat("x", 1<<20)
	if _, tooBig := clipboardRefusal(huge, base64.StdEncoding.EncodeToString([]byte(huge)), -1); tooBig {
		t.Error("the size check still fired with the limit disabled")
	}
}

// A payload that fits has to pass cleanly — a guard that refuses everything
// would "fix" the silent truncation by making the feature useless.
func TestOrdinaryPayloadsAreNotRefused(t *testing.T) {
	text := strings.Repeat("x", 1024)
	if _, tooBig := clipboardRefusal(text, base64.StdEncoding.EncodeToString([]byte(text)), defaultClipboardLimit); tooBig {
		t.Error("a 1 KB copy was refused against an 8 KB limit")
	}
}

func TestEmptyClipboardPayloadIsRefusedNotCopied(t *testing.T) {
	msg := copyToClipboard("", defaultClipboardLimit)().(flashMsg)
	if msg.level != flashWarn || !strings.Contains(msg.text, "nothing to copy") {
		t.Errorf("flash = %+v", msg)
	}
}

func TestByteSizeReadsWellInAFooter(t *testing.T) {
	tests := []struct {
		n    int
		want string
	}{
		{512, "512B"},
		{1024, "1.0KB"},
		{8 * 1024, "8.0KB"},
		{9532, "9.3KB"},
	}
	for _, tc := range tests {
		if got := byteSize(tc.n); got != tc.want {
			t.Errorf("byteSize(%d) = %q, want %q", tc.n, got, tc.want)
		}
	}
}
