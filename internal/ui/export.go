package ui

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/TTMathCS/g9s/internal/gcp"
)

// exportFormat is the shape a table is written in.
type exportFormat string

const (
	exportCSV  exportFormat = "csv"
	exportJSON exportFormat = "json"
)

// exportPayload is the JSON document an export produces.
//
// The metadata is the reason this is not just the rows. A table lifted out of
// g9s and pasted into a ticket loses everything the footer was saying about
// it — which project it came from, when, and whether it was the whole picture.
// Complete is the field that matters: a count taken from a listing that was
// missing two regions is a lower bound, and nothing downstream can tell unless
// the export says so.
type exportPayload struct {
	Kind       string              `json:"kind"`
	Project    string              `json:"project"`
	ProjectID  string              `json:"project_id"`
	ExportedAt string              `json:"exported_at"`
	Filter     string              `json:"filter,omitempty"`
	Complete   bool                `json:"complete"`
	Warnings   []exportWarning     `json:"warnings,omitempty"`
	Rows       []map[string]string `json:"rows"`
}

// exportWarning is one gap, kept structured for the same reason gcp.Warning is.
type exportWarning struct {
	Scope  string `json:"scope,omitempty"`
	Reason string `json:"reason"`
	Detail string `json:"detail"`
}

// safeFilename keeps generated names to characters every filesystem accepts.
var safeFilename = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// runExport writes the table currently on screen to a file.
//
// What is exported is what is displayed: the visible columns, the rows left
// after the active filter, in the order shown. Anything else would make the
// file disagree with the screen it was taken from, and the person reading it
// is not the person who ran it.
//
// Deliberately not the raw API objects. Those carry fields the detail pane
// redacts before rendering — a VPN tunnel's pre-shared key, a cluster's client
// key — and a file on disk is a worse place for them than a terminal. Someone
// who needs the full object has `d` and `y` for one resource at a time.
func (m Model) runExport(argument string) (tea.Model, tea.Cmd) {
	format := exportCSV
	switch strings.ToLower(strings.TrimSpace(argument)) {
	case "", "csv":
		format = exportCSV
	case "json":
		format = exportJSON
	default:
		return m, flash(fmt.Sprintf("unknown export format %q — csv or json", argument), flashWarn)
	}

	if !m.hasActive {
		return m, flash("select a project first", flashWarn)
	}
	if m.screen != screenResources {
		return m, flash("nothing to export here — open a resource table first", flashWarn)
	}

	kind := m.currentKind()
	rows := m.visibleResources()
	if len(rows) == 0 {
		return m, flash("no rows to export", flashWarn)
	}

	name := fmt.Sprintf("g9s-%s-%s-%s.%s",
		safeFilename.ReplaceAllString(m.active.Name, "-"),
		safeFilename.ReplaceAllString(kind.ID, "-"),
		time.Now().Format("20060102-150405"),
		format)

	warnings := m.warningsFor(kind)
	body, err := renderExport(format, kind, m.active.Name, m.active.ProjectID,
		strings.TrimSpace(m.filter.Value()), rows, warnings)
	if err != nil {
		return m, flash("export failed: "+err.Error(), flashError)
	}

	// 0600 rather than 0644: the rows name infrastructure, service accounts and
	// addresses, which is not secret but is not everyone's business either.
	if err := os.WriteFile(name, body, 0o600); err != nil {
		return m, flash("export failed: "+err.Error(), flashError)
	}

	where := name
	if abs, err := filepath.Abs(name); err == nil {
		where = abs
	}

	// An incomplete listing has to say so here as well as in the footer. The
	// file outlives the session that made it, and the person who opens it next
	// has no other way to learn that two regions were unreadable.
	if len(warnings) > 0 {
		return m, flash(fmt.Sprintf("wrote %d rows to %s — INCOMPLETE, %s",
			len(rows), where, warningCount(len(warnings))), flashWarn)
	}
	return m, flash(fmt.Sprintf("wrote %d rows to %s", len(rows), where), flashInfo)
}

// renderExport turns a table into the bytes for one format.
func renderExport(format exportFormat, kind gcp.Kind, project, projectID, filter string,
	rows []gcp.Resource, warnings []gcp.Warning) ([]byte, error) {

	titles := make([]string, 0, len(kind.Columns))
	for _, c := range kind.Columns {
		titles = append(titles, c.Title)
	}

	if format == exportJSON {
		payload := exportPayload{
			Kind:       kind.ID,
			Project:    project,
			ProjectID:  projectID,
			ExportedAt: time.Now().UTC().Format(time.RFC3339),
			Filter:     filter,
			Complete:   len(warnings) == 0,
			Rows:       make([]map[string]string, 0, len(rows)),
		}
		for _, w := range warnings {
			payload.Warnings = append(payload.Warnings, exportWarning{
				Scope: w.Scope, Reason: w.Reason.String(), Detail: w.Detail,
			})
		}
		for _, r := range rows {
			row := make(map[string]string, len(titles))
			for i, title := range titles {
				if i < len(r.Row) {
					row[title] = r.Row[i]
				}
			}
			payload.Rows = append(payload.Rows, row)
		}
		out, err := json.MarshalIndent(payload, "", "  ")
		if err != nil {
			return nil, err
		}
		return append(out, '\n'), nil
	}

	var b strings.Builder
	w := csv.NewWriter(&b)
	if err := w.Write(titles); err != nil {
		return nil, err
	}
	for _, r := range rows {
		record := make([]string, len(titles))
		for i := range titles {
			if i < len(r.Row) {
				// The em dash the tables use for "no value" is display, not
				// data: a spreadsheet column of them sorts and filters as text
				// where an empty cell reads as absent, which is what it is.
				if cell := r.Row[i]; cell != "—" {
					record[i] = cell
				}
			}
		}
		if err := w.Write(record); err != nil {
			return nil, err
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return nil, err
	}
	return []byte(b.String()), nil
}
