package gcp

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	datafusion "google.golang.org/api/datafusion/v1"
	"google.golang.org/api/option"

	"github.com/TTMathCS/g9s/internal/config"
)

// DataFusionLister lists Cloud Data Fusion instances.
//
// Regional, reached with the `locations/-` wildcard on the same reasoning as
// Datastream: the response carries an Unreachable list, which only means
// something for a call that can span locations.
//
// Data Fusion is the most expensive thing in this table by an order of
// magnitude. An Enterprise instance bills per hour for as long as it exists,
// whether or not a pipeline has ever run on it, and there is no idle state —
// stopping the cost means deleting the instance. So the edition is the row: it
// is the difference between a few dollars a day and a few hundred, and a
// Developer instance left running is the cheap version of the same mistake.
type DataFusionLister struct{}

func (DataFusionLister) Kind() Kind {
	return Kind{
		ID:    "datafusion",
		Title: "Data Fusion",
		Columns: []Column{
			{Title: "NAME", Width: 4},
			{Title: "REGION", Width: 2},
			{Title: "EDITION", Width: 2},
			{Title: "VERSION", Width: 2},
			{Title: "PRIVATE", Width: 2},
			{Title: "STATE", Width: 2},
			{Title: "AGE", Width: 2},
		},
	}
}

func (DataFusionLister) List(ctx context.Context, _ *config.Config, p config.Project, opts []option.ClientOption) (Result, error) {
	svc, err := datafusion.NewService(ctx, opts...)
	if err != nil {
		return Result{}, fmt.Errorf("data fusion client: %w", err)
	}

	var (
		result      Result
		unreachable = map[string]bool{}
	)
	err = svc.Projects.Locations.Instances.List("projects/"+p.ProjectID+"/locations/-").
		Pages(ctx, func(page *datafusion.ListInstancesResponse) error {
			for _, loc := range page.Unreachable {
				unreachable[loc] = true
			}
			for _, inst := range page.Instances {
				if inst != nil {
					result.Resources = append(result.Resources, dataFusionResource(p, inst))
				}
			}
			return nil
		})
	if err != nil {
		return result, err
	}

	for _, loc := range sortedKeys(unreachable) {
		if w := describeFailure(lastSegment(loc), fmt.Errorf("location unreachable")); w != "" {
			result.Warnings = append(result.Warnings, w)
		}
	}

	sortResources(result.Resources)
	return result, nil
}

func dataFusionResource(p config.Project, i *datafusion.Instance) Resource {
	name := lastSegment(i.Name)
	region := instanceRegion(i.Name)

	return Resource{
		Name:     name,
		Location: region,
		Status:   dataFusionStatus(i),
		Row: []string{
			name,
			region,
			dataFusionEdition(i),
			dataFusionVersion(i),
			dataFusionPrivate(i),
			dataFusionState(i),
			age(i.CreateTime),
		},
		Raw: i,
		ConsoleURL: fmt.Sprintf(
			"https://console.cloud.google.com/data-fusion/locations/%s/instances/%s?project=%s",
			url.PathEscape(region), url.PathEscape(name), url.QueryEscape(p.ProjectID)),
	}
}

// dataFusionStatus surfaces a Developer-edition instance the way Bigtable
// surfaces a DEVELOPMENT one: it has no SLA and is not meant to carry
// production pipelines, and it reports the same RUNNING as an Enterprise
// instance costing fifty times as much.
func dataFusionStatus(i *datafusion.Instance) string {
	state := dataFusionState(i)
	if state == "RUNNING" && strings.EqualFold(i.Type, "DEVELOPER") {
		return "DEVELOPER_EDITION"
	}
	return state
}

func dataFusionState(i *datafusion.Instance) string {
	if i.State == "" || i.State == "STATE_UNSPECIFIED" {
		return "UNKNOWN"
	}
	return i.State
}

// dataFusionEdition is the cost lever, and the API calls it Type.
func dataFusionEdition(i *datafusion.Instance) string {
	switch strings.ToUpper(i.Type) {
	case "BASIC":
		return "basic"
	case "ENTERPRISE":
		return "enterprise"
	case "DEVELOPER":
		return "developer"
	case "", "TYPE_UNSPECIFIED":
		return "-"
	default:
		return strings.ToLower(i.Type)
	}
}

func dataFusionVersion(i *datafusion.Instance) string {
	if i.Version == "" {
		return "-"
	}
	return i.Version
}

// dataFusionPrivate says whether the instance has a public endpoint. A public
// one is reachable from outside the VPC, which is the setting worth seeing on
// a row rather than three clicks into the network config.
func dataFusionPrivate(i *datafusion.Instance) string {
	if i.PrivateInstance {
		return "private"
	}
	return "public"
}
