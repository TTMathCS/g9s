// Command g9s is a terminal UI for browsing Google Cloud resources.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/TTMathCS/g9s/internal/auth"
	"github.com/TTMathCS/g9s/internal/config"
	"github.com/TTMathCS/g9s/internal/doctor"
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
	// Accept `g9s doctor -offline` as well as `g9s -doctor -offline`.
	args, subcommandDoctor := stripSubcommand(os.Args[1:], "doctor")
	os.Args = append(os.Args[:1], args...)

	var (
		configPath  = flag.String("config", "", "path to config file (default $G9S_CONFIG or ~/.config/g9s/config.yaml)")
		showVersion = flag.Bool("version", false, "print version and exit")
		doInit      = flag.Bool("init", false, "write a starter config file and exit")
		doDoctor    = flag.Bool("doctor", false, "check config, gcloud, credentials and identity, then exit")
		offline     = flag.Bool("offline", false, "with -doctor, skip the checks that make network calls")
	)
	flag.Usage = usage
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

	// `g9s doctor` as well as `-doctor`: a subcommand is what people try first,
	// and being right about the tool while wrong about its spelling is a bad
	// reason to get an unhelpful error.
	if *doDoctor || subcommandDoctor {
		return runDoctor(path, *offline)
	}

	cfg, err := config.Load(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("no config at %s — run `g9s -init` to create one", path)
		}
		return diagnosable(err)
	}

	mgr, err := auth.NewManager(cfg)
	if err != nil {
		return diagnosable(err)
	}
	// Login and SSH both shell out to gcloud, so a missing binary is worth
	// catching before the user hits it mid-session — unless every project reads
	// a credentials_file, in which case there is no login to run and refusing
	// to start would block the one setup that works without gcloud.
	if mgr.ManagesAnyCredentials(cfg) {
		if err := mgr.Available(); err != nil {
			return diagnosable(err)
		}
	}

	program := tea.NewProgram(ui.New(cfg, mgr), tea.WithAltScreen())
	_, err = program.Run()
	return err
}

// usage replaces Go's default flag listing.
//
// The default prints the flags and nothing else: not what g9s is, not that a
// config has to exist first, not that `doctor` is the thing to run when
// something is wrong. Help is what people reach for immediately after an
// error, so it is the second-best chance to answer the question that error
// raised — and for a tool whose first run needs a config file that does not
// exist yet, a bare flag list answers none of it.
func usage() {
	out := flag.CommandLine.Output()
	fmt.Fprint(out, `g9s — a read-only terminal console for Google Cloud.

Browse resources across your GCP projects without changing your gcloud
configuration. Every call is a list or a get; g9s never modifies anything.

USAGE
  g9s [flags]              start the console
  g9s doctor [-offline]    check the setup and exit, without starting the UI
  g9s -init                write a starter config, then edit it
  g9s -version             print the version

GETTING STARTED
  1. g9s -init                     writes ~/.config/g9s/config.yaml
  2. edit that file                replace the example project_id values
  3. g9s doctor                    confirms gcloud, config and credentials
  4. g9s                           select a project, press l to log in

  gcloud must already be installed; g9s runs it for login and SSH.

WHEN SOMETHING IS WRONG
  g9s doctor            reports every problem it finds, each with a remedy
  g9s doctor -offline   the same, without the checks that leave the machine

  Sign-in that hangs, or an error naming redirect_uri, is usually a proxy in
  front of localhost. g9s doctor says so, and the login screen can finish the
  login from the address your browser got stuck on.

FLAGS
`)
	flag.PrintDefaults()
	fmt.Fprint(out, `
DOCUMENTATION
  https://github.com/TTMathCS/g9s
`)
}

// diagnosable appends the one command that turns a refusal to start into a
// list of what is wrong.
//
// Startup failures are the ones with the least context around them: there is
// no UI yet, the message is a single line on a terminal that is about to sit
// there, and "gcloud not found" on its own tells someone what happened but
// nothing about what to do — while `g9s doctor` has a remedy for that exact
// finding and for everything else it would have hit next.
func diagnosable(err error) error {
	return fmt.Errorf("%w\n\nRun `g9s doctor` to check the whole setup and get a remedy for each problem", err)
}

// valueFlags are the flags that consume the argument after them, so a
// subcommand-shaped word in that position can be told apart from a subcommand.
var valueFlags = map[string]bool{"-config": true, "--config": true}

// stripSubcommand removes a subcommand word from an argument list.
//
// Go's flag package stops parsing at the first non-flag argument, so leaving
// the word in place makes every flag after it silently ignored — `g9s doctor
// -offline` would run the network checks anyway. Removing it first means the
// subcommand can appear anywhere: before the flags, after them, or between.
//
// A word is only a subcommand if it is not the value of a flag that takes one,
// which is what keeps `-config doctor` pointing at a file called doctor.
func stripSubcommand(args []string, name string) ([]string, bool) {
	var (
		kept  = make([]string, 0, len(args))
		found bool
		// expectValue is set when the previous argument was a flag whose value
		// comes as the next argument rather than after an `=`.
		expectValue bool
	)
	for _, arg := range args {
		switch {
		case expectValue:
			expectValue = false
		case arg == name && !found:
			found = true
			continue
		case valueFlags[arg]:
			expectValue = true
		}
		kept = append(kept, arg)
	}
	return kept, found
}

// runDoctor reports on the setup and exits non-zero if anything is broken.
//
// Non-zero on failure so it is usable in a script or an onboarding check,
// rather than only by eye. Warnings do not fail: "not logged in yet" is the
// expected state on a fresh machine, and a check that cries wolf on first run
// gets ignored on the run that matters.
func runDoctor(path string, offline bool) error {
	report := doctor.Run(context.Background(), doctor.Options{
		ConfigPath:  path,
		SkipNetwork: offline,
		Timeout:     2 * time.Minute,
	})

	fmt.Printf("g9s %s — checking %s\n\n", version, path)
	doctor.Write(os.Stdout, report)

	if report.Failed() {
		return errors.New("doctor found problems that will stop g9s working")
	}
	return nil
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

  # Read credentials from an existing file instead of logging in per project.
  # Set this when the login handshake cannot complete at all — behind a proxy
  # that swallows the loopback redirect, both gcloud flows end at a localhost
  # your browser cannot reach. g9s only ever reads this file; keeping it fresh
  # is then yours. Can also be set per project.
  #credentials_file: ~/.config/gcloud/application_default_credentials.json

  # Always use gcloud's --no-browser flow for l, the same as pressing L
  # every time. Worth setting behind a proxy: the ordinary login ends with your
  # browser fetching http://localhost:<port>/ to hand the authorization code
  # back, and a browser that sends localhost through a proxy never delivers it —
  # the sign-in succeeds and the terminal waits forever.
  login_no_browser: false

  # Upper bound on a single refresh across all regions.
  list_timeout: 90s

  # How far back the BigQuery jobs table looks. Jobs are kept for six months,
  # so this window is what makes that listing a complete answer rather than a
  # truncated one; it is also capped at 500 rows, and says so when it hits it.
  bigquery_job_window: 24h

  # Immediate objects/folders per Storage browser page. Press space for more.
  storage_objects_page_size: 500

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
