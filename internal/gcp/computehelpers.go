package gcp

import (
	"strings"
)

// computeScopeWarning turns a warning embedded in an aggregated Compute
// response into the same short, scoped warning the rest of the package uses.
//
// Compute includes NO_RESULTS_ON_PAGE for every empty region or zone. Empty is
// the normal answer for almost every scope in almost every project, so showing
// those messages would bury an actual partial-result warning in dozens of
// false alarms.
func computeScopeWarning(scope, code, message string) (Warning, bool) {
	if strings.EqualFold(code, "NO_RESULTS_ON_PAGE") || message == "" {
		return Warning{}, false
	}
	label := lastSegment(scope)
	if label == "" {
		label = "compute"
	}
	// The API's own code is what says whether this is a permission problem or
	// something else, and it is more reliable than reading its prose.
	reason := ReasonUnknown
	switch strings.ToUpper(code) {
	case "UNREACHABLE":
		reason = ReasonUnreachable
	case "FORBIDDEN", "PERMISSION_DENIED":
		reason = ReasonDenied
	}
	return scopeWarning(label, reason, clip(message, 120)), true
}

// appendComputeUnreachables preserves the scopes that an aggregated list
// could not read even when the API does not attach a human-readable warning.
// Without this, a partial inventory is indistinguishable from a complete one.
func appendComputeUnreachables(result *Result, scopes []string) {
	for _, scope := range scopes {
		label := lastSegment(scope)
		if label == "" {
			label = "compute"
		}
		result.Warnings = append(result.Warnings, scopeWarning(label, ReasonUnreachable, "unreachable"))
	}
}

// segmentAfter extracts the name immediately following a collection in a GCP
// resource URL. It is used for the one value lastSegment cannot recover: the
// zone from an instance URL whose final segment is the instance name.
func segmentAfter(ref, collection string) string {
	parts := strings.Split(strings.Trim(ref, "/"), "/")
	for i := 0; i+1 < len(parts); i++ {
		if parts[i] == collection {
			return parts[i+1]
		}
	}
	return ""
}
