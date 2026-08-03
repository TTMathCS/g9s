package gcp

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// A panic in one of these goroutines used to end the process. bubbletea
// recovers a panic in the command goroutine it started and restores the
// terminal on the way out, but recover only reaches its own goroutine's stack,
// so a fan-out leg bypassed it entirely: the process died with the terminal
// still in raw mode and on the alternate screen, leaving a shell that no longer
// echoes and no message saying why.
//
// If this regresses, the test binary crashes rather than failing — which is
// exactly the symptom being guarded against.
func TestFanOutSurvivesAPanickingLeg(t *testing.T) {
	result := fanOut(context.Background(), []string{"good-1", "boom", "good-2"},
		func(_ context.Context, loc string) (Result, error) {
			if loc == "boom" {
				var nilMap map[string]string
				nilMap["write to nil map"] = "" // the shape a bad response takes
			}
			return Result{Resources: []Resource{{Name: "cluster", Location: loc}}}, nil
		})

	// The other legs' rows are the whole point: a panic in one scope must cost
	// only that scope, exactly as a permission error does.
	if len(result.Resources) != 2 {
		t.Errorf("got %d resources, want the 2 from the healthy scopes", len(result.Resources))
	}
	if len(result.Warnings) != 1 {
		t.Fatalf("got %d warnings, want 1: %v", len(result.Warnings), result.Warnings)
	}
	if !strings.Contains(result.Warnings[0], "boom") {
		t.Errorf("warning = %q, want it to name the scope that failed", result.Warnings[0])
	}
	// "internal error" rather than a bare panic value: this is a g9s bug, not
	// something the user can fix by changing a permission, and the wording is
	// what tells them the difference.
	if !strings.Contains(result.Warnings[0], "internal error") {
		t.Errorf("warning = %q, want it to read as an internal error", result.Warnings[0])
	}
}

func TestSafelyPassesNormalResultsThrough(t *testing.T) {
	if err := safely("scope", func() error { return nil }); err != nil {
		t.Errorf("safely returned %v for a clean run", err)
	}

	// An ordinary error must arrive unchanged, so describeFailure can still
	// classify it as permission denied, not-found and so on.
	sentinel := status.Error(codes.PermissionDenied, "nope")
	err := safely("scope", func() error { return sentinel })
	if err != sentinel {
		t.Errorf("safely rewrote an ordinary error: %v", err)
	}
}

// The stack is the only thing that makes a recovered panic diagnosable, and it
// cannot go to stderr: stderr is the terminal bubbletea is drawing on, so a
// stack trace there would scribble across the UI.
func TestCrashLogIsWrittenOnlyWhenConfigured(t *testing.T) {
	path := filepath.Join(t.TempDir(), "g9s-debug.log")
	t.Setenv(debugLogEnv, path)

	if err := safely("us-central1", func() error { panic("boom") }); err == nil {
		t.Fatal("safely swallowed a panic without returning an error")
	}

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("no crash log written: %v", err)
	}
	for _, want := range []string{"us-central1", "boom", "safely"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("crash log does not contain %q:\n%s", want, body)
		}
	}
}

func TestNoCrashLogWithoutTheEnvVar(t *testing.T) {
	// g9s writes nothing outside its credential directory unless asked.
	dir := t.TempDir()
	t.Setenv(debugLogEnv, "")

	if err := safely("scope", func() error { panic("boom") }); err == nil {
		t.Fatal("safely swallowed a panic without returning an error")
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("files were written with no debug log configured: %v", entries)
	}
}
