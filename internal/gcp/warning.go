package gcp

import (
	"fmt"
	"sort"
	"strings"
)

// Reason classifies why a listing is not the whole picture.
//
// The point of the classification is that it can be counted and compared
// without parsing prose. A string like "us-east4: permission denied" answers a
// reader looking at one table and nothing else: it cannot be totalled across
// ten projects, cannot be told apart from a row cap, and cannot be used to
// decide whether a comparison between two projects is even meaningful. Every
// one of those is something g9s is meant to grow into, and each would
// otherwise start by pattern-matching sentences this package wrote.
type Reason int

const (
	// ReasonUnknown is a failure that was not recognised. The detail carries
	// the server's own message.
	ReasonUnknown Reason = iota
	// ReasonDenied is a scope the caller may not read.
	ReasonDenied
	// ReasonUnauthenticated is a credential that is missing or expired.
	ReasonUnauthenticated
	// ReasonUnreachable is a scope the API itself could not answer for.
	ReasonUnreachable
	// ReasonCapped is a configured row limit that stopped the listing.
	ReasonCapped
	// ReasonPartial is a listing that succeeded while some of its sub-items
	// could not be read — key rings whose keys are denied, service accounts
	// whose keys are denied.
	ReasonPartial
	// ReasonNarrowed is a listing the configuration deliberately restricted,
	// such as a region sweep that covered only `global`.
	ReasonNarrowed
	// ReasonInternal is a recovered panic. A g9s bug, not the user's problem
	// to fix, and worth telling apart from every failure that is.
	ReasonInternal
)

func (r Reason) String() string {
	switch r {
	case ReasonDenied:
		return "denied"
	case ReasonUnauthenticated:
		return "unauthenticated"
	case ReasonUnreachable:
		return "unreachable"
	case ReasonCapped:
		return "capped"
	case ReasonPartial:
		return "partial"
	case ReasonNarrowed:
		return "narrowed"
	case ReasonInternal:
		return "internal"
	default:
		return "unknown"
	}
}

// Warning is one reason a listing is incomplete.
//
// Every warning means something is missing. There is no advisory level here on
// purpose: a message that does not change what the table is claiming would be
// noise in a footer that has room for very little.
type Warning struct {
	// Scope names the region, zone, key ring or other partition the problem
	// belongs to. Empty when it applies to the whole listing, which is the
	// normal case for a row cap.
	Scope string
	// Reason classifies the problem.
	Reason Reason
	// Detail is the human-facing explanation, without the scope prefix.
	Detail string
}

// String renders the warning the way the footer shows it.
func (w Warning) String() string {
	if w.Scope == "" {
		return w.Detail
	}
	if w.Detail == "" {
		return w.Scope
	}
	return w.Scope + ": " + w.Detail
}

// scopeWarning builds a warning about one partition of a listing.
func scopeWarning(scope string, reason Reason, detail string) Warning {
	return Warning{Scope: scope, Reason: reason, Detail: detail}
}

// cappedWarning builds a warning about a configured row limit being reached.
//
// The format string carries the cap because the wording that helps differs by
// kind: a job history stops for a different reason than a zone's record sets,
// and the sentence that tells you what to change is not the same one.
func cappedWarning(format string, args ...any) Warning {
	return Warning{Reason: ReasonCapped, Detail: fmt.Sprintf(format, args...)}
}

// narrowedWarning builds a warning about a sweep the configuration limited.
func narrowedWarning(detail string) Warning {
	return Warning{Reason: ReasonNarrowed, Detail: detail}
}

// partialWarning builds a warning about sub-items that could not be read.
func partialWarning(scope, format string, args ...any) Warning {
	return Warning{Scope: scope, Reason: ReasonPartial, Detail: fmt.Sprintf(format, args...)}
}

// Complete reports whether the listing is the whole picture.
//
// This is the question the string warnings could never answer, and the one
// that matters most when a result is used for anything beyond looking at it:
// a count drawn from an incomplete listing is a lower bound, and a comparison
// between two projects where either side is incomplete is not a comparison.
func (r Result) Complete() bool { return len(r.Warnings) == 0 }

// Incomplete returns the warnings matching a reason, for callers that care
// about one kind of gap — a denied scope is a permission to request, while a
// cap is a setting to raise, and they are acted on by different people.
func (r Result) Incomplete(reason Reason) []Warning {
	var out []Warning
	for _, w := range r.Warnings {
		if w.Reason == reason {
			out = append(out, w)
		}
	}
	return out
}

// WarningStrings renders the warnings for display, in order.
func WarningStrings(warnings []Warning) []string {
	out := make([]string, 0, len(warnings))
	for _, w := range warnings {
		out = append(out, w.String())
	}
	return out
}

// dedupeSortWarnings removes duplicates and orders warnings for display.
//
// Sorted by rendered text rather than by reason: the footer shows them in a
// row, and a reader scanning for a region name should find it where the
// alphabet says it is.
func dedupeSortWarnings(r *Result) {
	if len(r.Warnings) == 0 {
		return
	}
	seen := make(map[Warning]bool, len(r.Warnings))
	unique := make([]Warning, 0, len(r.Warnings))
	for _, w := range r.Warnings {
		if seen[w] {
			continue
		}
		seen[w] = true
		unique = append(unique, w)
	}
	sortWarnings(unique)
	r.Warnings = unique
}

func sortWarnings(warnings []Warning) {
	sort.SliceStable(warnings, func(i, j int) bool {
		return strings.ToLower(warnings[i].String()) < strings.ToLower(warnings[j].String())
	})
}
