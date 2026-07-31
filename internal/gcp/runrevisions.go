package gcp

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"google.golang.org/api/option"
	run "google.golang.org/api/run/v2"

	"github.com/TTMathCS/g9s/internal/config"
)

// CloudRunRevisionLister is the revisions behind one Cloud Run service.
//
// The question is "which revision is actually serving", and it takes both
// halves to answer: the revisions come from a list call, but the traffic split
// is a field on the *service*, not on any revision. So this joins the two —
// list the revisions, then read the percentages off the parent row that opened
// the drill-down.
//
// That join is the reason the service table alone cannot answer it. A service
// row can say READY while the revision serving all its traffic is three
// deploys old, which is most of "I deployed but nothing changed".
type CloudRunRevisionLister struct{}

func (CloudRunRevisionLister) ParentKind() string { return "run" }

func (CloudRunRevisionLister) Kind() Kind {
	return Kind{
		ID:    "runrevs",
		Title: "Revisions",
		Columns: []Column{
			{Title: "NAME", Width: 4},
			{Title: "TRAFFIC", Width: 1},
			{Title: "IMAGE", Width: 5},
			{Title: "SCALING", Width: 2},
			{Title: "STATE", Width: 2},
			{Title: "AGE", Width: 2},
		},
	}
}

func (CloudRunRevisionLister) List(ctx context.Context, _ *config.Config, p config.Project, parent Resource, opts []option.ClientOption) (Result, error) {
	service, ok := parent.Raw.(*run.GoogleCloudRunV2Service)
	if !ok {
		return Result{}, fmt.Errorf("no service data for %s", parent.Name)
	}

	svc, err := run.NewService(ctx, opts...)
	if err != nil {
		return Result{}, fmt.Errorf("cloud run client: %w", err)
	}

	traffic := trafficByRevision(service)

	var result Result
	err = svc.Projects.Locations.Services.Revisions.List(service.Name).
		Pages(ctx, func(page *run.GoogleCloudRunV2ListRevisionsResponse) error {
			for _, rev := range page.Revisions {
				if rev == nil {
					continue
				}
				result.Resources = append(result.Resources, revisionResource(p, parent, rev, traffic))
			}
			return nil
		})
	if err != nil {
		return result, err
	}

	sortRevisions(result.Resources, traffic)
	return result, nil
}

// trafficByRevision maps revision name to the percentage it serves.
//
// Built from the service rather than the revisions because that is where the
// split lives — a revision has no idea whether anything is routed to it.
func trafficByRevision(s *run.GoogleCloudRunV2Service) map[string]int64 {
	out := map[string]int64{}
	for _, t := range s.TrafficStatuses {
		if t == nil || t.Revision == "" {
			continue
		}
		// Percentages add up across entries for the same revision when a tag
		// and the main route both point at it.
		out[lastSegment(t.Revision)] += t.Percent
	}
	return out
}

func revisionResource(p config.Project, parent Resource, rev *run.GoogleCloudRunV2Revision, traffic map[string]int64) Resource {
	name := lastSegment(rev.Name)

	// A revision with no traffic is the normal state for everything except the
	// current one, so it reads better as a dash than as a hard zero.
	share := "-"
	if pct, ok := traffic[name]; ok && pct > 0 {
		share = fmt.Sprintf("%d%%", pct)
	}

	return Resource{
		Name:     name,
		Location: parent.Location,
		Status:   revisionState(rev),
		Row: []string{
			name,
			share,
			revisionImage(rev),
			scalingSummary(rev.Scaling),
			revisionState(rev),
			age(rev.CreateTime),
		},
		Raw: rev,
		ConsoleURL: fmt.Sprintf(
			"https://console.cloud.google.com/run/detail/%s/%s/revisions?project=%s",
			url.PathEscape(parent.Location), url.PathEscape(lastSegment(rev.Service)),
			url.QueryEscape(p.ProjectID)),
	}
}

// revisionImage is what the revision actually runs.
//
// The registry host and project path are the same on every row and would cost
// most of the column; the image name and its tag are what differ, and the tag
// is what tells you whether two revisions are the same build.
func revisionImage(rev *run.GoogleCloudRunV2Revision) string {
	for _, c := range rev.Containers {
		if c != nil && c.Image != "" {
			return lastSegment(c.Image)
		}
	}
	return "-"
}

// scalingSummary reports the instance bounds. A minimum above zero is the
// difference between a service that costs nothing idle and one that does not,
// which is invisible anywhere else in the tool.
func scalingSummary(s *run.GoogleCloudRunV2RevisionScaling) string {
	if s == nil {
		return "-"
	}
	if s.MaxInstanceCount == 0 {
		if s.MinInstanceCount == 0 {
			return "-"
		}
		return fmt.Sprintf("%d-", s.MinInstanceCount)
	}
	return fmt.Sprintf("%d-%d", s.MinInstanceCount, s.MaxInstanceCount)
}

// revisionState reads the Ready condition.
//
// A revision carries a list of conditions rather than one terminal condition
// like a service does, so the one that matters has to be picked out by name.
func revisionState(rev *run.GoogleCloudRunV2Revision) string {
	for _, c := range rev.Conditions {
		if c != nil && strings.EqualFold(c.Type, "Ready") {
			return conditionState(c)
		}
	}
	return "UNKNOWN"
}

// sortRevisions puts the serving revision first, then the newest.
//
// Traffic leads because it is the answer to the question the table was opened
// with; among revisions serving nothing, the newest is the one just deployed
// and the one being wondered about.
func sortRevisions(resources []Resource, traffic map[string]int64) {
	stableSortBy(resources, func(a, b Resource) bool {
		ta, tb := traffic[a.Name], traffic[b.Name]
		if ta != tb {
			return ta > tb
		}
		ca, cb := revisionCreateTime(a), revisionCreateTime(b)
		if ca != cb {
			return ca > cb
		}
		return a.Name < b.Name
	})
}

func revisionCreateTime(r Resource) string {
	rev, ok := r.Raw.(*run.GoogleCloudRunV2Revision)
	if !ok {
		return ""
	}
	return rev.CreateTime
}
