package gcp

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	clouderrorreporting "google.golang.org/api/clouderrorreporting/v1beta1"
	"google.golang.org/api/option"

	"github.com/TTMathCS/g9s/internal/config"
)

// errorGroupWindow is how far back the error listing looks.
//
// A day, because this table answers "what is breaking now" rather than "what
// has ever broken". A longer window buries a fault that started an hour ago
// under a month of noise that somebody already decided not to fix.
const errorGroupWindow = "PERIOD_1_DAY"

// ErrorGroupLister lists Error Reporting groups.
//
// Error Reporting is the one service that already knows what is wrong, and it
// is consulted least, because reaching it means leaving whatever you were
// looking at. The value of having it here is adjacency: the Cloud Run service
// on the previous screen and the exception it started throwing at 14:02 are the
// same investigation, and one keypress between them is the difference between
// noticing and not.
//
// Grouped errors rather than individual events. A stack trace repeated eleven
// thousand times is one fault, and a table with eleven thousand rows in it is
// not a table.
type ErrorGroupLister struct{}

func (ErrorGroupLister) Kind() Kind {
	return Kind{
		ID:    "errors",
		Title: "Error Groups",
		Columns: []Column{
			{Title: "ERROR", Width: 6},
			{Title: "SERVICE", Width: 3},
			{Title: "COUNT", Width: 1},
			{Title: "USERS", Width: 1},
			{Title: "LAST SEEN", Width: 2},
			{Title: "STATUS", Width: 2},
		},
	}
}

func (ErrorGroupLister) List(ctx context.Context, cfg *config.Config, p config.Project, opts []option.ClientOption) (Result, error) {
	svc, err := clouderrorreporting.NewService(ctx, opts...)
	if err != nil {
		return Result{}, fmt.Errorf("error reporting client: %w", err)
	}

	var result Result
	err = svc.Projects.GroupStats.List("projects/"+p.ProjectID).
		TimeRangePeriod(errorGroupWindow).
		// Most frequent first: the fault everything is hitting is the one to
		// look at, and alphabetical order by exception class is meaningless.
		Order("COUNT_DESC").
		Pages(ctx, func(page *clouderrorreporting.ListGroupStatsResponse) error {
			for _, g := range page.ErrorGroupStats {
				if g != nil {
					result.Resources = append(result.Resources, errorGroupResource(p, g))
				}
			}
			return nil
		})
	if err != nil {
		return result, err
	}

	// Not sorted here: the API's frequency order is the useful one, and
	// sortResources would replace it with something alphabetical.
	return result, nil
}

func errorGroupResource(p config.Project, g *clouderrorreporting.ErrorGroupStats) Resource {
	message := errorGroupMessage(g)

	return Resource{
		Name:     message,
		Location: "global",
		Status:   errorGroupStatus(g),
		Row: []string{
			message,
			errorGroupService(g),
			fmt.Sprint(g.Count),
			errorGroupUsers(g),
			errorGroupLastSeen(g),
			errorGroupStatus(g),
		},
		Raw: g,
		ConsoleURL: fmt.Sprintf("https://console.cloud.google.com/errors?project=%s",
			url.QueryEscape(p.ProjectID)),
	}
}

// errorGroupMessage is the first line of the representative stack trace, which
// is the exception and its message — the part anybody would recognise.
func errorGroupMessage(g *clouderrorreporting.ErrorGroupStats) string {
	if g.Representative == nil || g.Representative.Message == "" {
		if g.Group != nil && g.Group.GroupId != "" {
			return clip(g.Group.GroupId, 70)
		}
		return "(no message)"
	}
	first, _, _ := strings.Cut(g.Representative.Message, "\n")
	return clip(strings.TrimSpace(first), 70)
}

// errorGroupService names what is throwing it. An error group spanning two
// services is one fault reaching both, and which they are is the finding.
func errorGroupService(g *clouderrorreporting.ErrorGroupStats) string {
	if len(g.AffectedServices) == 0 {
		return "-"
	}
	names := make([]string, 0, len(g.AffectedServices))
	for _, s := range g.AffectedServices {
		if s == nil || s.Service == "" {
			continue
		}
		name := s.Service
		if s.Version != "" {
			name += "@" + s.Version
		}
		names = append(names, name)
	}
	if len(names) == 0 {
		return "-"
	}
	return clip(strings.Join(names, ", "), 40)
}

// errorGroupUsers is how many distinct users hit it.
//
// Zero is not the same as unknown: Error Reporting only counts users when the
// error report carried a user identifier, so an unreported count reads as `-`
// rather than as a fault nobody experienced.
func errorGroupUsers(g *clouderrorreporting.ErrorGroupStats) string {
	if g.AffectedUsersCount == 0 {
		return "-"
	}
	return fmt.Sprint(g.AffectedUsersCount)
}

func errorGroupLastSeen(g *clouderrorreporting.ErrorGroupStats) string {
	t, err := time.Parse(time.RFC3339, g.LastSeenTime)
	if err != nil {
		return "-"
	}
	return shortDuration(time.Since(t))
}

// errorGroupStatus separates what is happening now from what has stopped.
//
// A fault last seen five minutes ago and one last seen twenty hours ago are
// both in a one-day window and are not the same situation. FIRING is the row
// worth reading; the rest is history that happens to be recent.
func errorGroupStatus(g *clouderrorreporting.ErrorGroupStats) string {
	t, err := time.Parse(time.RFC3339, g.LastSeenTime)
	if err != nil {
		return "UNKNOWN"
	}
	if time.Since(t) < time.Hour {
		return "FIRING"
	}
	return "RESOLVED_OR_QUIET"
}
