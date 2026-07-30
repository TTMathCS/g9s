//go:build screenshot

// Renders the README screenshots from the real View() with example data.
//
//	go test -tags screenshot -run TestGenerateScreenshots ./internal/ui
//
// Living in the ui package is what makes this worth having: it drives the same
// unexported model the binary does, so a change to the layout shows up in the
// next regenerated image instead of quietly making the README a lie. The data
// is invented — see docs/README.md in this directory.
package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/TTMathCS/g9s/internal/auth"
	"github.com/TTMathCS/g9s/internal/config"
	"github.com/TTMathCS/g9s/internal/gcp"
)

// Terminal geometry for the captures. Wide enough that no column is truncated,
// short enough that the image stays readable inline in the README.
// shotWidth is shared so both captures come out the same width and sit flush
// when stacked in the README. Heights are per-shot: each is sized to leave one
// blank row above the footer, because a taller terminal just renders as a dead
// band between the content and the status line.
const shotWidth = 132

func TestGenerateScreenshots(t *testing.T) {
	// go test redirects stdout, so lipgloss would otherwise detect "no colour"
	// and render the styles away to plain text. Force the profile instead of
	// relying on CLICOLOR_FORCE, so the output does not depend on how the
	// command was invoked.
	lipgloss.SetColorProfile(termenv.TrueColor)
	lipgloss.SetHasDarkBackground(true)

	outDir := filepath.Join("..", "..", "docs")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatal(err)
	}

	for _, shot := range []struct {
		name  string
		model Model
	}{
		{"projects", projectsShot()},
		{"dashboard", dashboardShot()},
		{"resources", resourcesShot()},
	} {
		path := filepath.Join(outDir, shot.name+".ansi")
		if err := os.WriteFile(path, []byte(padToWidth(shot.model.View())), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("wrote %s", path)
	}
}

// padToWidth right-pads every line to the full terminal width. Views that stop
// at their content leave the image renderer sizing to the longest line, so
// without this the captures come out at different widths and look mismatched
// stacked in the README. Padding is measured in visible cells, not bytes, so
// the escape sequences in the line do not count against it.
func padToWidth(view string) string {
	lines := strings.Split(view, "\n")
	for i, line := range lines {
		if n := shotWidth - lipgloss.Width(line); n > 0 {
			lines[i] = line + strings.Repeat(" ", n)
		}
	}

	// The trailing spare line is a workaround, not decoration: the image
	// renderer lays the final line out inside its bottom padding, where the
	// window border clips it. Reproducible with a plain three-line file — only
	// two come out. Giving it a spare line to lose keeps the footer visible.
	// The line has to be whitespace rather than empty; an empty trailing line
	// is trimmed before layout and absorbs nothing.
	return strings.Join(lines, "\n") + "\n" + strings.Repeat(" ", shotWidth) + "\n"
}

// demoConfig is the fictional estate shown in the screenshots: ten projects
// across three environments, IDs following the usual GCP convention of
// lowercase <org>-<team>-<env>-<discriminator>, several reached through a
// different support account. All invented — no real project IDs here.
func demoConfig() *config.Config {
	return &config.Config{
		Projects: []config.Project{
			{Name: "prod-data", ProjectID: "acme-dataeng-prod-4471", Account: "svc-dataeng-prod@example.com"},
			{Name: "prod-ml", ProjectID: "acme-mlplat-prod-8823", Account: "svc-mlplat-prod@example.com"},
			{Name: "prod-datalake", ProjectID: "acme-datalake-prod-9902", Account: "svc-dataeng-prod@example.com"},
			{Name: "prod-orchestration", ProjectID: "acme-composer-prod-3318", Account: "svc-dataeng-prod@example.com"},
			{Name: "analytics-bi", ProjectID: "acme-analytics-prod-6610", Account: "svc-bi-support@example.com"},
			{Name: "audit-logs", ProjectID: "acme-security-audit-7150", Account: "svc-sec-readonly@example.com"},
			{Name: "shared-vpc-host", ProjectID: "acme-network-shared-0001"},
			{Name: "staging-data", ProjectID: "acme-dataeng-stg-4472"},
			{Name: "dev-data", ProjectID: "acme-dataeng-dev-4473"},
			{Name: "sandbox", ProjectID: "acme-sandbox-dev-1204"},
		},
	}
}

func baseShot(height int) Model {
	cfg := demoConfig()
	m := New(cfg, nil)
	m.width = shotWidth
	m.height = height
	return m
}

// resourcesShot is the VM table: three status colours, and a partial-result
// warning holding the footer, which is the behaviour most worth advertising.
func resourcesShot() Model {
	m := baseShot(9 + 3 + chromeHeight)
	m.screen = screenResources
	m.active = m.cfg.Projects[0]
	m.hasActive = true
	m.cursor = 1
	// The header shows the live credential state while inside a project.
	m.authStatus[m.active.Name] = auth.Status{State: auth.StateValid, Expiry: time.Now().Add(38 * time.Minute)}

	// Zone names are kept to us-* here so nothing truncates at shotWidth. A
	// real northamerica-northeast1-a is 25 characters and the ZONE column only
	// earns that much space past ~195 columns — accurate, but it makes for a
	// screenshot full of ellipses.
	rows := [][]string{
		{"etl-worker-01", "us-central1-a", "n2-standard-8", "10.0.0.12", "—", "RUNNING", "14d"},
		{"etl-worker-02", "us-central1-a", "n2-standard-8", "10.0.0.13", "—", "RUNNING", "14d"},
		{"etl-worker-03", "us-central1-a", "n2-standard-8", "10.0.0.14", "—", "STAGING", "2m"},
		{"feature-store", "us-central1-c", "n2-highmem-4", "10.1.0.7", "—", "RUNNING", "63d"},
		{"airflow-db-proxy", "us-east1-b", "e2-small", "10.1.0.21", "—", "RUNNING", "121d"},
		{"dbt-runner-01", "us-central1-a", "c3-standard-4", "10.0.0.31", "—", "RUNNING", "9d"},
		{"dbt-runner-02", "us-central1-a", "c3-standard-4", "10.0.0.32", "—", "STOPPING", "9d"},
		{"bastion", "us-central1-b", "e2-medium", "10.0.1.4", "34.72.1.9", "RUNNING", "204d"},
		{"legacy-etl", "us-east1-b", "n1-standard-4", "10.1.0.8", "—", "TERMINATED", "331d"},
	}

	resources := make([]gcp.Resource, 0, len(rows))
	for _, r := range rows {
		resources = append(resources, gcp.Resource{
			Name:     r[0],
			Location: r[1],
			Status:   r[5],
			Row:      r,
		})
	}

	m.cache[m.currentKind().ID] = gcp.Result{
		Resources: resources,
		Warnings:  []string{"us-east4: permission denied"},
	}
	// The other kinds are cached too, so the tab bar shows a count per tab and
	// the All Resources total is the sum rather than just the VM count.
	m.cache["gke"] = gcp.Result{
		Resources: statusOnly(kindByID("gke"), map[string]int{"RUNNING": 2}),
	}
	m.cache["sql"] = gcp.Result{
		Resources: statusOnly(kindByID("sql"), map[string]int{"RUNNABLE": 3, "MAINTENANCE": 1}),
	}
	m.cache["vpc"] = gcp.Result{
		Resources: statusOnly(kindByID("vpc"), map[string]int{"ACTIVE": 3}),
	}
	m.cache["fw"] = gcp.Result{
		Resources: statusOnly(kindByID("fw"), map[string]int{"ENABLED": 22, "DISABLED": 2}),
	}
	m.cache["lb"] = gcp.Result{
		Resources: statusOnly(kindByID("lb"), map[string]int{"ACTIVE": 4}),
	}
	m.cache["dns"] = gcp.Result{
		Resources: statusOnly(kindByID("dns"), map[string]int{"ACTIVE": 2}),
	}
	m.cache["vpn"] = gcp.Result{
		Resources: statusOnly(kindByID("vpn"), map[string]int{"ESTABLISHED": 3, "NO_INCOMING_PACKETS": 1}),
	}
	m.cache["interconnect"] = gcp.Result{
		Resources: statusOnly(kindByID("interconnect"), map[string]int{"ACTIVE": 2}),
	}
	m.cache["psc"] = gcp.Result{
		Resources: statusOnly(kindByID("psc"), map[string]int{"ACTIVE": 1}),
	}
	m.cache["gcs"] = gcp.Result{
		Resources: statusOnly(kindByID("gcs"), map[string]int{"ACTIVE": 5}),
	}
	m.cache["dataproc"] = gcp.Result{
		Resources: statusOnly(kindByID("dataproc"), map[string]int{"RUNNING": 2, "UPDATING": 1}),
	}
	m.cache["composer"] = gcp.Result{
		Resources: statusOnly(kindByID("composer"), map[string]int{"RUNNING": 1}),
	}
	return m
}

// dashboardShot is the per-project overview: every category with its count and
// status breakdown, including the merged row, plus a partial listing so the
// warning treatment is visible.
func dashboardShot() Model {
	m := baseShot(len(gcp.Listers()) + 2 + chromeHeight)
	m.screen = screenOverview
	m.active = m.cfg.Projects[0]
	m.hasActive = true
	m.ovCursor = 0
	m.authStatus[m.active.Name] = auth.Status{State: auth.StateValid, Expiry: time.Now().Add(38 * time.Minute)}

	m.cache["vm"] = gcp.Result{
		Resources: statusOnly(kindByID("vm"), map[string]int{"RUNNING": 6, "STAGING": 1, "TERMINATED": 2}),
		Warnings:  []string{"us-east4: permission denied"},
	}
	m.cache["gke"] = gcp.Result{
		Resources: statusOnly(kindByID("gke"), map[string]int{"RUNNING": 2}),
	}
	m.cache["sql"] = gcp.Result{
		Resources: statusOnly(kindByID("sql"), map[string]int{"RUNNABLE": 3, "MAINTENANCE": 1}),
	}
	m.cache["vpc"] = gcp.Result{
		Resources: statusOnly(kindByID("vpc"), map[string]int{"ACTIVE": 3}),
	}
	m.cache["fw"] = gcp.Result{
		Resources: statusOnly(kindByID("fw"), map[string]int{"ENABLED": 22, "DISABLED": 2}),
	}
	m.cache["lb"] = gcp.Result{
		Resources: statusOnly(kindByID("lb"), map[string]int{"ACTIVE": 4}),
	}
	m.cache["dns"] = gcp.Result{
		Resources: statusOnly(kindByID("dns"), map[string]int{"ACTIVE": 2}),
	}
	m.cache["vpn"] = gcp.Result{
		Resources: statusOnly(kindByID("vpn"), map[string]int{"ESTABLISHED": 3, "NO_INCOMING_PACKETS": 1}),
	}
	m.cache["interconnect"] = gcp.Result{
		Resources: statusOnly(kindByID("interconnect"), map[string]int{"ACTIVE": 2}),
	}
	m.cache["psc"] = gcp.Result{
		Resources: statusOnly(kindByID("psc"), map[string]int{"ACTIVE": 1}),
	}
	m.cache["gcs"] = gcp.Result{
		Resources: statusOnly(kindByID("gcs"), map[string]int{"ACTIVE": 5}),
	}
	m.cache["dataproc"] = gcp.Result{
		Resources: statusOnly(kindByID("dataproc"), map[string]int{"RUNNING": 2, "UPDATING": 1}),
	}
	m.cache["composer"] = gcp.Result{
		Resources: statusOnly(kindByID("composer"), map[string]int{"RUNNING": 1}),
	}
	return m
}

// statusOnly builds placeholder resources for a kind, carrying a status and a
// correctly-shaped row. The dashboard only reads the status, but the row width
// has to match the kind's columns so the fixture stays valid if a capture ever
// renders one of these tables.
func statusOnly(kind gcp.Kind, counts map[string]int) []gcp.Resource {
	// Sorted so the fixture is deterministic; the view does its own ordering.
	statuses := make([]string, 0, len(counts))
	for status := range counts {
		statuses = append(statuses, status)
	}
	sort.Strings(statuses)

	var out []gcp.Resource
	for _, status := range statuses {
		for i := 0; i < counts[status]; i++ {
			name := fmt.Sprintf("%s-%d", strings.ToLower(status), i)
			row := make([]string, len(kind.Columns))
			for c := range row {
				row[c] = "—"
			}
			row[0] = name
			out = append(out, gcp.Resource{Name: name, Status: status, Row: row})
		}
	}
	return out
}

// kindByID looks a kind up among the registered listers.
func kindByID(id string) gcp.Kind {
	for _, l := range gcp.Listers() {
		if l.Kind().ID == id {
			return l.Kind()
		}
	}
	panic("unknown kind " + id)
}

// projectsShot is the picker with every credential state visible at once,
// which is the thing a static description of the auth model never conveys.
func projectsShot() Model {
	// projectsView emits the column header plus one row per project, then pads
	// to bodyHeight; +1 leaves exactly one blank row above the footer.
	m := baseShot(len(demoConfig().Projects) + 1 + chromeHeight)
	m.screen = screenProjects
	m.projCursor = 0

	// A realistic mid-morning spread: the production accounts have gone stale
	// on the IdP's session policy, the low-privilege ones are still good, and
	// two were never logged in on this machine.
	m.authStatus = map[string]auth.Status{
		"prod-data":          {State: auth.StateExpired},
		"prod-ml":            {State: auth.StateExpired},
		"prod-datalake":      {State: auth.StateValid, Expiry: time.Now().Add(38 * time.Minute)},
		"prod-orchestration": {State: auth.StateExpired},
		"analytics-bi":       {State: auth.StateValid, Expiry: time.Now().Add(52 * time.Minute)},
		"audit-logs":         {State: auth.StateMissing},
		"shared-vpc-host":    {State: auth.StateMissing},
		"staging-data":       {State: auth.StateValid, Expiry: time.Now().Add(3 * time.Hour)},
		"dev-data":           {State: auth.StateValid, Expiry: time.Now().Add(2 * time.Hour)},
		"sandbox":            {State: auth.StateValid, Expiry: time.Now().Add(4 * time.Hour)},
	}
	return m
}
