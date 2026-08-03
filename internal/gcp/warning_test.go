package gcp

import (
	"strings"
	"testing"
)

// The question the string warnings could not answer. A count drawn from an
// incomplete listing is a lower bound, and a comparison between two projects
// where either side is incomplete is not a comparison — so a caller has to be
// able to ask, rather than infer it from whether some prose happens to be
// present.
func TestCompleteDistinguishesAWholeListingFromAPartialOne(t *testing.T) {
	whole := Result{Resources: []Resource{{Name: "vm-1"}}}
	if !whole.Complete() {
		t.Error("a listing with no warnings reports itself incomplete")
	}

	// Zero rows is not the same as incomplete: an empty project is a complete
	// answer, and treating it as suspect would make every empty table look
	// like a failure.
	empty := Result{}
	if !empty.Complete() {
		t.Error("an empty but successful listing reports itself incomplete")
	}

	partial := Result{
		Resources: []Resource{{Name: "vm-1"}},
		Warnings:  []Warning{scopeWarning("us-east4", ReasonDenied, "permission denied")},
	}
	if partial.Complete() {
		t.Error("a listing with a denied scope reports itself complete")
	}
}

// A denied scope is a permission to request and a cap is a setting to raise.
// Different people act on them, so a caller has to be able to separate them
// without matching on sentences this package wrote.
func TestIncompleteSeparatesReasonsThatDifferentPeopleActOn(t *testing.T) {
	r := Result{Warnings: []Warning{
		scopeWarning("us-east4", ReasonDenied, "permission denied"),
		scopeWarning("europe-west1", ReasonDenied, "permission denied"),
		cappedWarning("only the %d most recent jobs are shown", 500),
		scopeWarning("us-west2", ReasonUnreachable, "unreachable"),
	}}

	if got := len(r.Incomplete(ReasonDenied)); got != 2 {
		t.Errorf("got %d denied scopes, want 2", got)
	}
	if got := len(r.Incomplete(ReasonCapped)); got != 1 {
		t.Errorf("got %d caps, want 1", got)
	}
	if got := len(r.Incomplete(ReasonUnreachable)); got != 1 {
		t.Errorf("got %d unreachable scopes, want 1", got)
	}
	if got := len(r.Incomplete(ReasonInternal)); got != 0 {
		t.Errorf("got %d internal errors, want none", got)
	}

	// The scope has to survive the classification: "which regions" is the
	// question a permission request is written from.
	denied := r.Incomplete(ReasonDenied)
	for _, w := range denied {
		if w.Scope == "" {
			t.Errorf("denied warning lost its scope: %+v", w)
		}
	}
}

func TestWarningStringRendersWithAndWithoutAScope(t *testing.T) {
	tests := []struct {
		name string
		in   Warning
		want string
	}{
		{
			name: "scoped",
			in:   scopeWarning("us-east4", ReasonDenied, "permission denied"),
			want: "us-east4: permission denied",
		},
		{
			// A row cap belongs to the listing, not to any one region, so
			// prefixing it with a scope would invent a fact.
			name: "listing-wide",
			in:   cappedWarning("only the %d most recent jobs are shown", 500),
			want: "only the 500 most recent jobs are shown",
		},
		{
			name: "scope with no detail",
			in:   Warning{Scope: "us-west1"},
			want: "us-west1",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.in.String(); got != tc.want {
				t.Errorf("String() = %q, want %q", got, tc.want)
			}
		})
	}
}

// Two warnings that read alike but classify differently are two different
// facts, and collapsing them would lose the one that mattered.
func TestDedupeKeepsWarningsThatDifferOnlyByReason(t *testing.T) {
	r := Result{Warnings: []Warning{
		scopeWarning("us-east4", ReasonDenied, "unavailable"),
		scopeWarning("us-east4", ReasonUnreachable, "unavailable"),
		scopeWarning("us-east4", ReasonDenied, "unavailable"),
	}}
	dedupeSortWarnings(&r)

	if len(r.Warnings) != 2 {
		t.Fatalf("got %d warnings, want 2: %+v", len(r.Warnings), r.Warnings)
	}
}

// A recovered panic is a g9s bug. Sorting it in with permission errors would
// send someone to their administrator for a problem no grant can fix.
func TestPanicsAreClassifiedAsInternalNotAsPermissionProblems(t *testing.T) {
	err := safely("us-central1", func() error { panic("nil map write") })

	got, ok := describeFailure("us-central1", err)
	if !ok {
		t.Fatal("a recovered panic produced no warning")
	}
	if got.Reason != ReasonInternal {
		t.Errorf("reason = %v, want internal", got.Reason)
	}
	if got.Scope != "us-central1" {
		t.Errorf("scope = %q, want the failing scope", got.Scope)
	}
	if !strings.Contains(got.String(), "internal error") {
		t.Errorf("warning = %q, want it to read as an internal error", got)
	}
}

func TestReasonNamesAreStableForDisplayAndLogs(t *testing.T) {
	for reason, want := range map[Reason]string{
		ReasonUnknown:         "unknown",
		ReasonDenied:          "denied",
		ReasonUnauthenticated: "unauthenticated",
		ReasonUnreachable:     "unreachable",
		ReasonCapped:          "capped",
		ReasonPartial:         "partial",
		ReasonNarrowed:        "narrowed",
		ReasonInternal:        "internal",
	} {
		if got := reason.String(); got != want {
			t.Errorf("Reason(%d).String() = %q, want %q", reason, got, want)
		}
	}
}

func TestWarningStringsRendersInOrder(t *testing.T) {
	got := WarningStrings([]Warning{
		scopeWarning("a", ReasonDenied, "denied"),
		cappedWarning("capped at %d", 10),
	})
	want := []string{"a: denied", "capped at 10"}
	if len(got) != len(want) {
		t.Fatalf("got %d strings, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}
