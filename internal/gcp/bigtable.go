package gcp

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	bigtableadmin "google.golang.org/api/bigtableadmin/v2"
	"google.golang.org/api/option"

	"github.com/TTMathCS/g9s/internal/config"
)

// BigtableLister lists Cloud Bigtable instances.
//
// Global: the parent is `projects/{p}` and one paginated call returns every
// instance. Locations that did not answer come back in FailedLocations — a
// different field name from the Unreachable the newer APIs use, and the same
// idea, so a partial listing says which part is missing rather than reading as
// a project with fewer instances than it has.
//
// An instance is a container: it holds no data and has no size. The nodes, the
// storage type and the zone all belong to its clusters, which is what the
// drill-down is for. What the instance row can say is the thing that changes
// what every cluster under it means — a DEVELOPMENT instance has no SLA and no
// replication, which is a deliberate choice for a sandbox and a discovery in
// production.
type BigtableLister struct{}

func (BigtableLister) Kind() Kind {
	return Kind{
		ID:    "bigtable",
		Title: "Bigtable Instances",
		Columns: []Column{
			{Title: "NAME", Width: 4},
			{Title: "DISPLAY NAME", Width: 4},
			{Title: "TYPE", Width: 2},
			{Title: "EDITION", Width: 2},
			{Title: "STATE", Width: 2},
		},
	}
}

func (BigtableLister) List(ctx context.Context, _ *config.Config, p config.Project, opts []option.ClientOption) (Result, error) {
	svc, err := bigtableadmin.NewService(ctx, opts...)
	if err != nil {
		return Result{}, fmt.Errorf("bigtable client: %w", err)
	}

	var (
		result Result
		failed = map[string]bool{}
	)
	err = svc.Projects.Instances.List("projects/"+p.ProjectID).
		Pages(ctx, func(page *bigtableadmin.ListInstancesResponse) error {
			for _, loc := range page.FailedLocations {
				failed[loc] = true
			}
			for _, inst := range page.Instances {
				if inst != nil {
					result.Resources = append(result.Resources, bigtableResource(p, inst))
				}
			}
			return nil
		})
	if err != nil {
		return result, err
	}

	for _, loc := range sortedKeys(failed) {
		if w, ok := describeFailure(lastSegment(loc), fmt.Errorf("location unreachable")); ok {
			result.Warnings = append(result.Warnings, w)
		}
	}

	sortResources(result.Resources)
	return result, nil
}

func bigtableResource(p config.Project, i *bigtableadmin.Instance) Resource {
	name := lastSegment(i.Name)

	display := i.DisplayName
	if display == "" || display == name {
		display = "-"
	}

	return Resource{
		Name: name,
		// An instance has no location of its own — its clusters do, and they
		// can be in different regions. Claiming one here would be a guess.
		Location: "",
		Status:   bigtableStatus(i),
		Row: []string{
			name,
			display,
			bigtableType(i),
			bigtableEdition(i),
			bigtableState(i),
		},
		Raw: i,
		ConsoleURL: fmt.Sprintf(
			"https://console.cloud.google.com/bigtable/instances/%s/overview?project=%s",
			url.PathEscape(name), url.QueryEscape(p.ProjectID)),
	}
}

// bigtableStatus leads with the instance type when it is DEVELOPMENT.
//
// A development instance runs on a single node with no SLA and no replication.
// It reports READY exactly like a production one, and the difference only shows
// up when something depends on it staying available.
func bigtableStatus(i *bigtableadmin.Instance) string {
	state := bigtableState(i)
	if state == "READY" && strings.EqualFold(i.Type, "DEVELOPMENT") {
		return "DEVELOPMENT"
	}
	return state
}

func bigtableState(i *bigtableadmin.Instance) string {
	switch i.State {
	case "", "STATE_NOT_KNOWN":
		return "UNKNOWN"
	default:
		return i.State
	}
}

func bigtableType(i *bigtableadmin.Instance) string {
	switch strings.ToUpper(i.Type) {
	case "PRODUCTION":
		return "production"
	case "DEVELOPMENT":
		return "development"
	case "", "TYPE_UNSPECIFIED":
		return "-"
	default:
		return strings.ToLower(i.Type)
	}
}

func bigtableEdition(i *bigtableadmin.Instance) string {
	if i.Edition == "" || i.Edition == "EDITION_UNSPECIFIED" {
		return "-"
	}
	return strings.ToLower(i.Edition)
}
