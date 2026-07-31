package gcp

import (
	"testing"
	"time"

	bigquery "google.golang.org/api/bigquery/v2"
)

func TestBigQueryTableResourceShape(t *testing.T) {
	r := tableResource(testProject(), "sandbox-123", "analytics", testBigQueryTable())

	if r.Name != "events" {
		t.Errorf("Name = %q", r.Name)
	}
	// The dataset is the location here: it is what the listing is scoped to and
	// what the drill trail names.
	if r.Location != "analytics" {
		t.Errorf("Location = %q, want the dataset", r.Location)
	}
	if r.Row[1] != "TABLE" {
		t.Errorf("type cell = %q", r.Row[1])
	}
	if r.Row[3] != "customer_id,event_type" {
		t.Errorf("clustering cell = %q", r.Row[3])
	}
	if r.Row[5] != "never" {
		t.Errorf("expires cell = %q, want never for a table with no expiry", r.Row[5])
	}
}

func TestPartitioningNamesTheRequiredFilter(t *testing.T) {
	// The cost question this listing can answer without row counts: a
	// partitioned table that does not require a filter is how a SELECT * scans
	// four years of history and arrives as a bill.
	required := partitioningSummary(testBigQueryTable())
	if required != "DAY on event_date (required)" {
		t.Errorf("partitioning cell = %q", required)
	}

	optional := partitioningSummary(&bigquery.TableListTables{
		TimePartitioning: &bigquery.TimePartitioning{Type: "DAY", Field: "event_date"},
	})
	if optional != "DAY on event_date" {
		t.Errorf("without the flag = %q", optional)
	}
}

func TestPartitioningVariants(t *testing.T) {
	tests := []struct {
		name string
		in   *bigquery.TableListTables
		want string
	}{
		{"unpartitioned", &bigquery.TableListTables{}, "-"},
		// No field means the pseudo-column, which is a real difference:
		// queries have to filter on _PARTITIONTIME rather than on a column.
		{"ingestion time", &bigquery.TableListTables{
			TimePartitioning: &bigquery.TimePartitioning{Type: "DAY"},
		}, "DAY on _PARTITIONTIME"},
		{"range", &bigquery.TableListTables{
			RangePartitioning: &bigquery.RangePartitioning{Field: "customer_id"},
		}, "RANGE on customer_id"},
		// The flag lives on the table for range-partitioned tables and on the
		// time partitioning for older time-partitioned ones.
		{"required on the table", &bigquery.TableListTables{
			RangePartitioning:      &bigquery.RangePartitioning{Field: "customer_id"},
			RequirePartitionFilter: true,
		}, "RANGE on customer_id (required)"},
		{"type absent", &bigquery.TableListTables{
			TimePartitioning: &bigquery.TimePartitioning{Field: "ts"},
		}, "TIME on ts"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := partitioningSummary(tt.in); got != tt.want {
				t.Errorf("partitioningSummary = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestTableTypeDefaultsToTable(t *testing.T) {
	// A view costs nothing to store and everything to query; the two are
	// indistinguishable by name, so the column must never be blank.
	if got := tableType(&bigquery.TableListTables{}); got != "TABLE" {
		t.Errorf("tableType = %q, want TABLE", got)
	}
	if got := tableType(&bigquery.TableListTables{Type: "VIEW"}); got != "VIEW" {
		t.Errorf("tableType = %q", got)
	}
}

func TestTableExpiryIsFlagged(t *testing.T) {
	// BigQuery has no lifecycle state for a table, so the expiry is the one
	// thing worth saying: a table with one is scratch space.
	// Offset past the day boundary: exactly 72h is a hair under three days by
	// the time it is read back, and renders as "2d".
	expiring := &bigquery.TableListTables{
		ExpirationTime: time.Now().Add(78 * time.Hour).UnixMilli(),
	}
	if got := tableStatus(expiring); got != "EXPIRING" {
		t.Errorf("Status = %q, want EXPIRING", got)
	}
	if got := expiryFromMillis(expiring.ExpirationTime); got != "3d" {
		t.Errorf("expires cell = %q, want 3d", got)
	}

	gone := &bigquery.TableListTables{
		ExpirationTime: time.Now().Add(-time.Hour).UnixMilli(),
	}
	if got := tableStatus(gone); got != "EXPIRED" {
		t.Errorf("Status = %q, want EXPIRED", got)
	}
	if got := expiryFromMillis(gone.ExpirationTime); got != "expired" {
		t.Errorf("expires cell = %q", got)
	}

	if got := tableStatus(&bigquery.TableListTables{}); got != "ACTIVE" {
		t.Errorf("Status = %q, want ACTIVE with no expiry", got)
	}
}

func TestTableNameFallsBackToTheID(t *testing.T) {
	// The id is "project:dataset.table". Without a reference there is nothing
	// else identifying the row, and a blank NAME is worse than a long one.
	r := tableResource(testProject(), "sandbox-123", "analytics", &bigquery.TableListTables{
		Id: "sandbox-123:analytics.orphan",
	})
	if r.Name != "orphan" {
		t.Errorf("Name = %q, want it recovered from the id", r.Name)
	}
}

func TestTableDrillDownRejectsAParentThatIsNotADataset(t *testing.T) {
	_, err := (BigQueryTableLister{}).List(t.Context(), nil, testProject(),
		Resource{Name: "not-a-dataset", Raw: testInstance()}, nil)
	if err == nil {
		t.Error("a non-dataset parent was accepted")
	}

	// A dataset row with no reference on it cannot be listed either, and must
	// say so rather than querying the wrong project.
	_, err = (BigQueryTableLister{}).List(t.Context(), nil, testProject(),
		Resource{Name: "bare", Raw: &bigquery.DatasetListDatasets{}}, nil)
	if err == nil {
		t.Error("a dataset with no reference was accepted")
	}
}

func TestTablesAreNotSSHOrAirflowTargets(t *testing.T) {
	r := tableResource(testProject(), "sandbox-123", "analytics", testBigQueryTable())
	if _, _, ok := SSHTarget(r); ok {
		t.Error("a table is an ssh target")
	}
	if _, ok := AirflowURI(r); ok {
		t.Error("a table has an Airflow URI")
	}
}
