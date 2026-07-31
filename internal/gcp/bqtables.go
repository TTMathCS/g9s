package gcp

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	bigquery "google.golang.org/api/bigquery/v2"
	"google.golang.org/api/option"

	"github.com/TTMathCS/g9s/internal/config"
)

// maxTables caps one dataset's listing. A dataset holding a table per day per
// source runs to tens of thousands, which is a scroll buffer rather than a
// table.
const maxTables = 1000

// BigQueryTableLister is the tables inside one dataset.
//
// The datasets listing carries name, location, type and labels and nothing
// about contents — a row that only tells you the dataset exists. This is one
// paginated call made when you open it.
//
// No row counts or byte sizes: tables.list does not return them, and a `Get`
// per table to find out would be thousands of calls for a listing that is
// mostly scrolled past. The cost question this table can answer without them is
// the one that actually bites — whether a partitioned table requires a filter.
type BigQueryTableLister struct{}

func (BigQueryTableLister) ParentKind() string { return "bq" }

func (BigQueryTableLister) Kind() Kind {
	return Kind{
		ID:    "bqtables",
		Title: "Tables",
		Columns: []Column{
			{Title: "NAME", Width: 5},
			{Title: "TYPE", Width: 2},
			{Title: "PARTITIONING", Width: 4},
			{Title: "CLUSTERING", Width: 3},
			{Title: "CREATED", Width: 2},
			{Title: "EXPIRES", Width: 2},
		},
	}
}

func (BigQueryTableLister) List(ctx context.Context, _ *config.Config, p config.Project, parent Resource, opts []option.ClientOption) (Result, error) {
	dataset, ok := parent.Raw.(*bigquery.DatasetListDatasets)
	if !ok || dataset.DatasetReference == nil {
		return Result{}, fmt.Errorf("no dataset data for %s", parent.Name)
	}

	svc, err := bigquery.NewService(ctx, opts...)
	if err != nil {
		return Result{}, fmt.Errorf("bigquery client: %w", err)
	}

	// The dataset's own project, not the configured one: a dataset shared from
	// another project lists under the project that owns it.
	owner := dataset.DatasetReference.ProjectId
	if owner == "" {
		owner = p.ProjectID
	}
	datasetID := dataset.DatasetReference.DatasetId

	var (
		result Result
		capped bool
	)
	err = svc.Tables.List(owner, datasetID).Pages(ctx, func(page *bigquery.TableList) error {
		for _, t := range page.Tables {
			if t == nil {
				continue
			}
			if len(result.Resources) >= maxTables {
				capped = true
				return errStopPaging
			}
			result.Resources = append(result.Resources, tableResource(p, owner, datasetID, t))
		}
		return nil
	})
	if err != nil && !errors.Is(err, errStopPaging) {
		return result, err
	}

	if capped {
		result.Warnings = append(result.Warnings, fmt.Sprintf(
			"first %d tables only — the dataset holds more than this table shows", maxTables))
	}

	sortResources(result.Resources)
	return result, nil
}

func tableResource(p config.Project, owner, datasetID string, t *bigquery.TableListTables) Resource {
	name := ""
	if t.TableReference != nil {
		name = t.TableReference.TableId
	}
	if name == "" {
		// The id is "project:dataset.table"; the table is what is left.
		name = afterLast(t.Id, ".")
	}

	return Resource{
		Name:     name,
		Location: datasetID,
		Status:   tableStatus(t),
		Row: []string{
			name,
			tableType(t),
			partitioningSummary(t),
			clusteringSummary(t),
			millisAge(t.CreationTime),
			expiryFromMillis(t.ExpirationTime),
		},
		Raw: t,
		ConsoleURL: fmt.Sprintf(
			"https://console.cloud.google.com/bigquery?project=%s&ws=!1m5!1m4!4m3!1s%s!2s%s!3s%s",
			url.QueryEscape(p.ProjectID), url.PathEscape(owner),
			url.PathEscape(datasetID), url.PathEscape(name)),
	}
}

// tableType names what the row actually is. A view costs nothing to store and
// everything to query; a table is the reverse, and the two are indistinguishable
// by name.
func tableType(t *bigquery.TableListTables) string {
	if t.Type == "" {
		return "TABLE"
	}
	return t.Type
}

// partitioningSummary reports how the table is split, and whether a query is
// forced to say which partition it wants.
//
// That last part is the cost question this listing can actually answer: a
// partitioned table without a required filter is how a `SELECT *` scans four
// years of history and arrives as a bill.
func partitioningSummary(t *bigquery.TableListTables) string {
	var summary string
	switch {
	case t.TimePartitioning != nil:
		summary = t.TimePartitioning.Type
		if summary == "" {
			summary = "TIME"
		}
		if f := t.TimePartitioning.Field; f != "" {
			summary += " on " + f
		} else {
			// No field means the pseudo-column, which is a real difference:
			// queries have to filter on _PARTITIONTIME rather than a column.
			summary += " on _PARTITIONTIME"
		}
	case t.RangePartitioning != nil:
		summary = "RANGE"
		if f := t.RangePartitioning.Field; f != "" {
			summary += " on " + f
		}
	default:
		return "-"
	}

	// The flag lives in two places depending on how the table was created.
	if t.RequirePartitionFilter || (t.TimePartitioning != nil && t.TimePartitioning.RequirePartitionFilter) {
		return summary + " (required)"
	}
	return summary
}

func clusteringSummary(t *bigquery.TableListTables) string {
	if t.Clustering == nil || len(t.Clustering.Fields) == 0 {
		return "-"
	}
	return strings.Join(t.Clustering.Fields, ",")
}

// tableStatus flags a table that will delete itself.
//
// BigQuery has no lifecycle state for a table, so this is the one thing worth
// saying: a table with an expiry is scratch space, and a table whose expiry has
// passed is already gone in all but name.
func tableStatus(t *bigquery.TableListTables) string {
	if t.ExpirationTime == 0 {
		return "ACTIVE"
	}
	if millisToTime(t.ExpirationTime).Before(time.Now()) {
		return "EXPIRED"
	}
	return "EXPIRING"
}

// millisAge renders an epoch-milliseconds timestamp as an age.
func millisAge(millis int64) string {
	if millis == 0 {
		return "-"
	}
	return shortDuration(timeSince(millisToTime(millis)))
}

// expiryFromMillis says how long is left, or that there is no expiry at all.
func expiryFromMillis(millis int64) string {
	if millis == 0 {
		return "never"
	}
	if d := time.Until(millisToTime(millis)); d > 0 {
		return shortDuration(d)
	}
	return "expired"
}
