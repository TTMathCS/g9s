package gcp

import (
	"fmt"
	"strings"
)

// computeScopeWarning turns a warning embedded in an aggregated Compute
// response into the same short, scoped warning the rest of the package uses.
//
// Compute includes NO_RESULTS_ON_PAGE for every empty region or zone. Empty is
// the normal answer for almost every scope in almost every project, so showing
// those messages would bury an actual partial-result warning in dozens of
// false alarms.
func computeScopeWarning(scope, code, message string) string {
	if strings.EqualFold(code, "NO_RESULTS_ON_PAGE") || message == "" {
		return ""
	}
	label := lastSegment(scope)
	if label == "" {
		label = "compute"
	}
	return fmt.Sprintf("%s: %s", label, clip(message, 120))
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
		result.Warnings = append(result.Warnings, label+": unreachable")
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
