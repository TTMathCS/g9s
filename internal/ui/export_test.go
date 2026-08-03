package ui

import (
	"encoding/csv"
	"encoding/json"
	"strings"
	"testing"

	"github.com/TTMathCS/g9s/internal/gcp"
)

func exportKind() gcp.Kind {
	return gcp.Kind{
		ID:      "vm",
		Title:   "VM Instances",
		Columns: []gcp.Column{{Title: "NAME"}, {Title: "ZONE"}, {Title: "EXTERNAL IP"}, {Title: "STATUS"}},
	}
}

func exportRows() []gcp.Resource {
	return []gcp.Resource{
		{Name: "etl-worker-01", Row: []string{"etl-worker-01", "us-central1-a", "—", "RUNNING"}},
		{Name: "bastion", Row: []string{"bastion", "us-central1-b", "34.72.1.9", "RUNNING"}},
	}
}

func TestCSVExportMatchesTheTableColumns(t *testing.T) {
	body, err := renderExport(exportCSV, exportKind(), "prod-data", "acme-prod-4471", "", exportRows(), nil)
	if err != nil {
		t.Fatal(err)
	}

	records, err := csv.NewReader(strings.NewReader(string(body))).ReadAll()
	if err != nil {
		t.Fatalf("output is not valid CSV: %v", err)
	}
	if len(records) != 3 {
		t.Fatalf("got %d records (header + rows), want 3", len(records))
	}
	if got := strings.Join(records[0], ","); got != "NAME,ZONE,EXTERNAL IP,STATUS" {
		t.Errorf("header = %q, want the table's own columns", got)
	}
	// The em dash the tables print for "no value" is display, not data: a
	// column full of them sorts and filters as text, where an empty cell reads
	// as absent — which is what it means.
	if records[1][2] != "" {
		t.Errorf("placeholder dash reached the file as %q, want an empty cell", records[1][2])
	}
	if records[2][2] != "34.72.1.9" {
		t.Errorf("real value = %q", records[2][2])
	}
}

// A table lifted into a ticket loses everything the footer was saying about
// it. A count taken from a listing that was missing two regions is a lower
// bound, and nothing downstream can tell unless the export says so.
func TestJSONExportCarriesCompletenessAndWarnings(t *testing.T) {
	warnings := []gcp.Warning{
		{Scope: "us-east4", Reason: gcp.ReasonDenied, Detail: "permission denied"},
		{Reason: gcp.ReasonCapped, Detail: "only the 500 most recent jobs are shown"},
	}
	body, err := renderExport(exportJSON, exportKind(), "prod-data", "acme-prod-4471", "worker", exportRows(), warnings)
	if err != nil {
		t.Fatal(err)
	}

	var payload exportPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}

	if payload.Complete {
		t.Error("an export of a listing with a denied scope declares itself complete")
	}
	if len(payload.Warnings) != 2 {
		t.Fatalf("got %d warnings, want 2", len(payload.Warnings))
	}
	// Structured, for the same reason gcp.Warning is: a reader deciding
	// whether the file is usable should not have to parse prose.
	if payload.Warnings[0].Reason != "denied" || payload.Warnings[0].Scope != "us-east4" {
		t.Errorf("warning did not survive as structure: %+v", payload.Warnings[0])
	}
	if payload.Filter != "worker" {
		t.Errorf("filter = %q — a filtered export that does not say so misrepresents the rows it left out", payload.Filter)
	}
	if payload.Project != "prod-data" || payload.ProjectID != "acme-prod-4471" {
		t.Errorf("payload does not name where the rows came from: %+v", payload)
	}
	if len(payload.Rows) != 2 {
		t.Fatalf("got %d rows, want 2", len(payload.Rows))
	}
	if payload.Rows[0]["NAME"] != "etl-worker-01" {
		t.Errorf("row keyed wrong: %+v", payload.Rows[0])
	}
}

func TestCompleteListingExportsAsComplete(t *testing.T) {
	body, err := renderExport(exportJSON, exportKind(), "p", "p-1", "", exportRows(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var payload exportPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatal(err)
	}
	if !payload.Complete {
		t.Error("a listing with no warnings exported as incomplete")
	}
	if len(payload.Warnings) != 0 {
		t.Errorf("warnings = %+v, want none", payload.Warnings)
	}
}

// A resource whose Row is shorter than the kind's columns must not panic or
// shift the remaining cells into the wrong headings.
func TestExportToleratesShortRows(t *testing.T) {
	rows := []gcp.Resource{{Name: "half", Row: []string{"half", "us-central1-a"}}}

	body, err := renderExport(exportCSV, exportKind(), "p", "p-1", "", rows, nil)
	if err != nil {
		t.Fatal(err)
	}
	records, err := csv.NewReader(strings.NewReader(string(body))).ReadAll()
	if err != nil {
		t.Fatalf("short row produced invalid CSV: %v", err)
	}
	if len(records[1]) != 4 {
		t.Errorf("row has %d cells, want 4 to stay aligned with the header", len(records[1]))
	}
	if records[1][0] != "half" || records[1][3] != "" {
		t.Errorf("cells shifted: %v", records[1])
	}
}

// Values containing commas and quotes are ordinary in GCP labels and
// descriptions, and a file that breaks on them is worse than no file.
func TestExportQuotesAwkwardValues(t *testing.T) {
	rows := []gcp.Resource{{
		Name: "odd",
		Row:  []string{`odd,name`, `has "quotes"`, "10.0.0.1", "RUNNING"},
	}}

	body, err := renderExport(exportCSV, exportKind(), "p", "p-1", "", rows, nil)
	if err != nil {
		t.Fatal(err)
	}
	records, err := csv.NewReader(strings.NewReader(string(body))).ReadAll()
	if err != nil {
		t.Fatalf("awkward values produced invalid CSV: %v", err)
	}
	if records[1][0] != `odd,name` || records[1][1] != `has "quotes"` {
		t.Errorf("values did not round-trip: %v", records[1])
	}
}

func TestExportRefusesAnUnknownFormat(t *testing.T) {
	m := populatedModel(t)
	m.screen = screenResources

	_, cmd := m.runExport("yaml")
	msg, ok := cmd().(flashMsg)
	if !ok || msg.level != flashWarn {
		t.Fatalf("unknown format produced %#v", cmd())
	}
	if !strings.Contains(msg.text, "csv or json") {
		t.Errorf("flash = %q, want it to name the formats that work", msg.text)
	}
}

func TestExportOutsideAResourceTableSaysSo(t *testing.T) {
	m := populatedModel(t)
	m.screen = screenProjects

	_, cmd := m.runExport("csv")
	msg, ok := cmd().(flashMsg)
	if !ok {
		t.Fatalf("got %#v", cmd())
	}
	if !strings.Contains(msg.text, "resource table") {
		t.Errorf("flash = %q, want it to say where export works", msg.text)
	}
}
