// Command g9s is a terminal UI for browsing Google Cloud resources.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/TTMathCS/g9s/internal/auth"
	"github.com/TTMathCS/g9s/internal/config"
	"github.com/TTMathCS/g9s/internal/ui"
)

// version is overridden at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "g9s: "+err.Error())
		os.Exit(1)
	}
}

func run() error {
	var (
		configPath  = flag.String("config", "", "path to config file (default $G9S_CONFIG or ~/.config/g9s/config.yaml)")
		showVersion = flag.Bool("version", false, "print version and exit")
		doInit      = flag.Bool("init", false, "write a starter config file and exit")
	)
	flag.Parse()

	if *showVersion {
		fmt.Println("g9s " + version)
		return nil
	}

	path := *configPath
	if path == "" {
		path = config.DefaultPath()
	}

	if *doInit {
		return writeStarterConfig(path)
	}

	cfg, err := config.Load(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("no config at %s — run `g9s -init` to create one", path)
		}
		return err
	}

	mgr, err := auth.NewManager(cfg)
	if err != nil {
		return err
	}
	if err := mgr.Available(); err != nil {
		// Login and SSH both shell out to gcloud, so a missing binary is worth
		// catching before the user hits it mid-session.
		return err
	}

	program := tea.NewProgram(ui.New(cfg, mgr), tea.WithAltScreen())
	_, err = program.Run()
	return err
}

func writeStarterConfig(path string) error {
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("%s already exists", path)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	// 0600, not 0644. The file names your project IDs and support accounts,
	// and gcloud_path decides which binary g9s executes — anyone who can edit
	// it can run code as you the next time you press `l`.
	if err := os.WriteFile(path, []byte(starterConfig), 0o600); err != nil {
		return err
	}
	fmt.Printf("wrote %s — edit it to add your projects, then run g9s\n", path)
	return nil
}

const starterConfig = `# g9s configuration
#
# Each project gets its own isolated gcloud configuration directory under
# defaults.credential_dir, so credentials for different projects never collide
# and switching projects in the UI never mutates global gcloud state.

defaults:
  # Regions swept for region-scoped resources (Dataproc, Composer) unless a
  # project overrides them. Keep this list tight: every region is a separate
  # API call on every refresh.
  regions:
    - northamerica-northeast1
    - us-central1

  # Where per-project credentials live.
  credential_dir: ~/.local/share/g9s/credentials

  # gcloud binary used for login and SSH.
  gcloud_path: gcloud

  # Upper bound on a single refresh across all regions.
  list_timeout: 90s

  # How far back the BigQuery jobs table looks. Jobs are kept for six months,
  # so this window is what makes that listing a complete answer rather than a
  # truncated one; it is also capped at 500 rows, and says so when it hits it.
  bigquery_job_window: 24h

projects:
  - name: sandbox
    project_id: my-sandbox-project
    description: personal access, read-only
    # Omit account to use whatever identity gcloud logs in as.

  - name: prod-data
    project_id: my-prod-data-project
    description: support account required
    account: svc-prod-support@example.com
    # Override the default sweep for this project only.
    regions:
      - northamerica-northeast1
`
