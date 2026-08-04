// Package config loads and validates the g9s configuration file.
package config

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config is the top-level configuration document.
type Config struct {
	Defaults Defaults  `yaml:"defaults"`
	Projects []Project `yaml:"projects"`

	// path is the file this was loaded from, so the UI can name it. Unexported
	// and not a YAML field: it describes where the document came from, which is
	// not something the document gets to declare about itself.
	path string
}

// Path is the file this config was loaded from, empty if it was not loaded
// from one.
func (c *Config) Path() string {
	if c == nil {
		return ""
	}
	return c.path
}

// Defaults supply values for any project that does not override them.
type Defaults struct {
	// Regions is the fallback region list for every region-scoped resource.
	Regions []string `yaml:"regions"`
	// DataprocRegions narrows the Dataproc fan-out for all projects.
	DataprocRegions []string `yaml:"dataproc_regions"`
	// ComposerLocations narrows the Composer fan-out for all projects.
	ComposerLocations []string `yaml:"composer_locations"`
	// CredentialDir is where per-project gcloud config directories live.
	CredentialDir string `yaml:"credential_dir"`
	// GcloudPath is the gcloud binary to shell out to for login.
	GcloudPath string `yaml:"gcloud_path"`
	// CredentialsFile points every project at an existing credentials file
	// instead of a per-project directory g9s logs into. See Project.
	CredentialsFile string `yaml:"credentials_file"`
	// Terraform is the fallback state location for projects that do not name
	// their own. Useful for an estate that keeps every workspace in one
	// bucket, and wrong for one that gives each project its own — which is
	// why the per-project setting exists and wins.
	Terraform Terraform `yaml:"terraform"`
	// LoginNoBrowser makes `l` always use gcloud's --no-browser flow, the same
	// as pressing `L` every time.
	//
	// Worth setting on a machine behind a proxy. gcloud's ordinary login ends
	// with the browser fetching http://localhost:<port>/ to hand the
	// authorization code back, and a browser that sends localhost through a
	// proxy never delivers it — the sign-in succeeds and the terminal waits
	// forever. Whether the browser does that is a property of this machine, not
	// of any one project, which is why this lives in defaults.
	LoginNoBrowser bool `yaml:"login_no_browser"`
	// ListTimeout bounds a single refresh across all regions.
	ListTimeout Duration `yaml:"list_timeout"`
	// BigQueryJobWindow is how far back the BigQuery jobs table looks. Jobs are
	// kept for six months, which is far more than a "what is running now"
	// table can show, so the window is what makes the listing a complete
	// answer rather than a truncated one.
	BigQueryJobWindow Duration `yaml:"bigquery_job_window"`
	// StorageObjectsPageSize is the number of object and folder rows shown in
	// one UI page while browsing a bucket. It is not a total cap: the UI retains
	// the continuation token and can load the next page.
	StorageObjectsPageSize int `yaml:"storage_objects_page_size"`
	// ClipboardLimit is the largest OSC 52 escape sequence, in bytes, that this
	// terminal is believed to accept.
	//
	// It exists because the failure it prevents is silent. `y` writes the
	// clipboard as one escape sequence, and a terminal that considers it too
	// long drops the whole thing without reporting anything — so g9s would say
	// "copied" over a clipboard that never changed. A stock xterm cuts off
	// around 8 KB and tmux refuses entirely unless set-clipboard is on, while
	// modern terminals take far more, and nothing in the protocol lets g9s ask
	// which it is talking to. So the default is the conservative one and the
	// number is yours to raise once you know your terminal.
	//
	// Zero uses the default. Negative removes the check, which means trusting
	// the terminal to say something when it truncates — most do not.
	ClipboardLimit int `yaml:"clipboard_limit"`

	// Limits raises or removes the per-kind row caps.
	Limits Limits `yaml:"limits"`
}

// Limits are the row caps each listing stops at.
//
// Every one of these was a constant compiled into a lister, which made the
// bound the tool's opinion rather than the reader's. The defaults are unchanged
// and are sized for a table a person scrolls; the point of the block is that a
// project big enough to hit one is no longer stuck at a number it did not pick.
//
// Zero means "use the default". A negative value means no cap at all, which is
// a real choice with real consequences — a zone with 90,000 record sets will
// fetch all 90,000 — so it has to be asked for explicitly rather than being
// what an empty field happens to mean.
type Limits struct {
	// BigQueryJobs caps the BigQuery jobs table. The window bounds it first;
	// this is the backstop for a busy hour inside that window.
	BigQueryJobs int `yaml:"bigquery_jobs"`
	// DataflowJobs caps the Dataflow jobs table.
	DataflowJobs int `yaml:"dataflow_jobs"`
	// DataprocJobsPerRegion caps each region's Dataproc job listing. It is per
	// region, so the table can hold this many times your region count.
	DataprocJobsPerRegion int `yaml:"dataproc_jobs_per_region"`
	// ClusterJobs caps the per-cluster Dataproc job drill-down.
	ClusterJobs int `yaml:"cluster_jobs"`
	// BigQueryTables caps one dataset's table listing.
	BigQueryTables int `yaml:"bigquery_tables"`
	// DNSRecordSets caps one zone's record listing.
	DNSRecordSets int `yaml:"dns_record_sets"`
	// BackendGroups caps the getHealth calls one backend-health drill-down
	// makes. Unlike the others this bounds *requests*, not rows: each group is
	// its own round trip, so raising it costs latency rather than memory.
	BackendGroups int `yaml:"backend_groups"`
	// ServiceAccountKeyLookups caps how many accounts get their key ages read.
	// Also a request bound: one keys.list per account.
	ServiceAccountKeyLookups int `yaml:"service_account_key_lookups"`
	// KMSKeyRings caps how many key rings per location get their keys read.
	// One cryptoKeys.list per ring, so this is a request bound too.
	KMSKeyRings int `yaml:"kms_key_rings"`
	// CloudBuilds caps the build history table. Build records are kept
	// indefinitely, so without a bound this is the one listing that grows
	// without limit for the life of the project.
	CloudBuilds int `yaml:"cloud_builds"`
	// TerraformStateObjects caps how many state files the overlay reads in one
	// load. A prefix pointed at the root of a bucket can match hundreds, and
	// each one is a download — so this is a request and bandwidth bound rather
	// than a row bound.
	TerraformStateObjects int `yaml:"terraform_state_objects"`
}

// Default row caps. These are the numbers that used to be compiled into each
// lister, kept exactly as they were so upgrading changes nothing on its own.
const (
	DefaultBigQueryJobs             = 500
	DefaultDataflowJobs             = 500
	DefaultDataprocJobsPerRegion    = 200
	DefaultClusterJobs              = 200
	DefaultBigQueryTables           = 1000
	DefaultDNSRecordSets            = 1000
	DefaultBackendGroups            = 40
	DefaultServiceAccountKeyLookups = 200
	DefaultKMSKeyRings              = 100
	DefaultCloudBuilds              = 200
	DefaultTerraformStateObjects    = 25
)

// limit resolves one configured cap: unset falls back to the default, and a
// negative value means uncapped, which callers see as a very large number so
// none of them needs a special case for it.
func limit(configured, fallback int) int {
	switch {
	case configured == 0:
		return fallback
	case configured < 0:
		return maxInt
	default:
		return configured
	}
}

// maxInt stands in for "no cap". Comparing a row count against it is false for
// any listing that fits in memory, which is the only property callers need.
const maxInt = int(^uint(0) >> 1)

// limits reads the configured caps, tolerating a nil Config.
//
// Nil is not hypothetical: a drill-down that needs no configuration is called
// with a nil *Config in tests and would panic here on the first field access.
// A nil config means "nothing configured", which is exactly the zero value.
func (c *Config) limits() Limits {
	if c == nil {
		return Limits{}
	}
	return c.Defaults.Limits
}

// Cap accessors. One per limit rather than a map lookup, so a typo is a compile
// error and every call site names which bound it is asking about.

func (c *Config) LimitCloudBuilds() int {
	return limit(c.limits().CloudBuilds, DefaultCloudBuilds)
}

func (c *Config) LimitTerraformStateObjects() int {
	return limit(c.limits().TerraformStateObjects, DefaultTerraformStateObjects)
}

func (c *Config) LimitBigQueryJobs() int {
	return limit(c.limits().BigQueryJobs, DefaultBigQueryJobs)
}

func (c *Config) LimitDataflowJobs() int {
	return limit(c.limits().DataflowJobs, DefaultDataflowJobs)
}

func (c *Config) LimitDataprocJobsPerRegion() int {
	return limit(c.limits().DataprocJobsPerRegion, DefaultDataprocJobsPerRegion)
}

func (c *Config) LimitClusterJobs() int {
	return limit(c.limits().ClusterJobs, DefaultClusterJobs)
}

func (c *Config) LimitBigQueryTables() int {
	return limit(c.limits().BigQueryTables, DefaultBigQueryTables)
}

func (c *Config) LimitDNSRecordSets() int {
	return limit(c.limits().DNSRecordSets, DefaultDNSRecordSets)
}

func (c *Config) LimitBackendGroups() int {
	return limit(c.limits().BackendGroups, DefaultBackendGroups)
}

func (c *Config) LimitServiceAccountKeyLookups() int {
	return limit(c.limits().ServiceAccountKeyLookups, DefaultServiceAccountKeyLookups)
}

func (c *Config) LimitKMSKeyRings() int {
	return limit(c.limits().KMSKeyRings, DefaultKMSKeyRings)
}

// Project is one GCP project as the user works with it: the project ID plus
// the identity used to reach it and the regions worth scanning.
type Project struct {
	// Name is the label shown in the picker. Also names the credential dir.
	Name string `yaml:"name"`
	// ProjectID is the real GCP project ID.
	ProjectID string `yaml:"project_id"`
	// Account is the support account email passed to `gcloud --account`.
	// Empty means "whatever gcloud picks", which is fine for a personal login.
	Account string `yaml:"account"`
	// Description is free text shown alongside the project in the picker.
	Description string `yaml:"description"`

	// CredentialsFile is an existing credentials file to read for this
	// project, instead of the per-project directory g9s logs into.
	//
	// This is the way in when the login handshake cannot complete at all —
	// behind a proxy that swallows the loopback redirect, gcloud's browser
	// flow and its --no-browser flow both end at a localhost the browser
	// cannot reach. Point this at credentials obtained any way that does
	// work, most often the ordinary ~/.config/gcloud ADC, and g9s reads them
	// rather than demanding a login it cannot finish.
	//
	// g9s only ever reads this file. Keeping it fresh is then yours, which is
	// the trade: no isolation between projects that share one file, and no
	// login for g9s to run.
	CredentialsFile string `yaml:"credentials_file"`

	// Production marks a project where a mistake is expensive.
	//
	// It only ever adds friction: a project marked production makes every
	// action confirmation louder and demands the resource's name be typed,
	// including for actions that are not otherwise destructive. It cannot
	// make anything easier, which is what makes it safe to guess at — see
	// IsProduction.
	//
	// A pointer so that "unset" and "explicitly false" are different answers.
	// Someone who writes `production: false` on a project called prod-data has
	// said something deliberate, and guessing over the top of it would make
	// the setting a lie.
	Production *bool `yaml:"production"`

	Regions           []string `yaml:"regions"`
	DataprocRegions   []string `yaml:"dataproc_regions"`
	ComposerLocations []string `yaml:"composer_locations"`

	// Terraform is where this project's state lives. Almost always
	// per-project, since each project usually has its own state bucket.
	Terraform Terraform `yaml:"terraform"`
}

// Terraform locates a project's state in its GCS backend.
//
// The same two values the `backend "gcs"` block in the Terraform config
// already has, so this is copied rather than worked out. g9s only ever reads
// these objects: it never writes state, never locks it, and never runs
// Terraform.
type Terraform struct {
	// StateBucket is the GCS bucket the state lives in, without the gs://.
	StateBucket string `yaml:"state_bucket"`
	// StatePrefix is the backend's prefix. Empty reads the whole bucket,
	// which is fine for a bucket that holds only state and slow for one that
	// does not.
	StatePrefix string `yaml:"state_prefix"`
}

// productionHints are the substrings that make a project look like production.
//
// Guessing is defensible here only because of the direction of the error. A
// false positive costs an extra confirmation on a sandbox; a false negative
// costs the quiet path on a production instance. Those are not comparable, so
// the guess leans hard one way and the config overrides it either way.
var productionHints = []string{"prod", "prd", "live"}

// IsProduction reports whether a project should be treated as production.
//
// An explicit setting always wins. Otherwise the name and project ID are
// checked for the usual markers, because the common case is that nobody set
// the flag and the project is called something like acme-dataeng-prod-4471 —
// and the first time anyone thinks about this setting should not be after
// stopping the wrong instance.
func (p Project) IsProduction() bool {
	if p.Production != nil {
		return *p.Production
	}
	haystack := strings.ToLower(p.Name + " " + p.ProjectID)
	for _, hint := range productionHints {
		if strings.Contains(haystack, hint) {
			return true
		}
	}
	return false
}

// Duration is a time.Duration that unmarshals from a YAML string like "90s".
type Duration time.Duration

func (d *Duration) UnmarshalYAML(node *yaml.Node) error {
	var s string
	if err := node.Decode(&s); err != nil {
		return err
	}
	parsed, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", s, err)
	}
	*d = Duration(parsed)
	return nil
}

func (d Duration) Duration() time.Duration { return time.Duration(d) }

// dataprocRegions returns the regions to sweep for Dataproc clusters, most
// specific setting first. "global" is always included because clusters can be
// created there and it is easy to forget.
func (c *Config) DataprocRegions(p Project) []string {
	regions := firstNonEmpty(p.DataprocRegions, p.Regions, c.Defaults.DataprocRegions, c.Defaults.Regions)
	return withGlobal(regions)
}

// KMSLocations returns the locations to sweep for KMS key rings.
//
// Regions plus "global", always, and for a stronger reason than Dataproc's: a
// key ring in the global location is not an edge case, it is where most
// projects put their first one. Sweeping only the configured regions would show
// an empty KMS table to a project whose keys all exist.
func (c *Config) KMSLocations(p Project) []string {
	return withGlobal(c.Regions(p))
}

// ComposerLocations returns the locations to sweep for Composer environments.
func (c *Config) ComposerLocations(p Project) []string {
	return firstNonEmpty(p.ComposerLocations, p.Regions, c.Defaults.ComposerLocations, c.Defaults.Regions)
}

// Regions returns the regions to sweep for a kind with no override of its own,
// most specific setting first. Cloud Run uses this: its API refuses the "-"
// wildcard, so the sweep is only ever as wide as this list.
func (c *Config) Regions(p Project) []string {
	return firstNonEmpty(p.Regions, c.Defaults.Regions)
}

// TerraformBackend returns the bucket and prefix holding a project's state.
//
// The bucket and prefix travel together: a project that names its own bucket
// gets its own prefix too, empty or not. Mixing one project's bucket with
// another's prefix would read the wrong state and report every resource in the
// project as unmanaged, which is the worst answer this feature can give.
func (c *Config) TerraformBackend(p Project) (bucket, prefix string) {
	if p.Terraform.StateBucket != "" {
		return p.Terraform.StateBucket, p.Terraform.StatePrefix
	}
	return c.Defaults.Terraform.StateBucket, c.Defaults.Terraform.StatePrefix
}

// CredentialsFile returns the existing credentials file to read for a project,
// or "" when g9s manages the credentials itself.
func (c *Config) CredentialsFile(p Project) string {
	if p.CredentialsFile != "" {
		return expandHome(p.CredentialsFile)
	}
	if c.Defaults.CredentialsFile != "" {
		return expandHome(c.Defaults.CredentialsFile)
	}
	return ""
}

// BigQueryJobWindow is how far back to list BigQuery jobs.
func (c *Config) BigQueryJobWindow() time.Duration {
	if c.Defaults.BigQueryJobWindow > 0 {
		return c.Defaults.BigQueryJobWindow.Duration()
	}
	return defaultBigQueryJobWindow
}

// StorageObjectsPageSize returns the human-sized page used by the object
// browser. A nil or hand-built Config gets the same default as one loaded from
// YAML; tests and embedded callers do not necessarily pass through
// applyDefaults.
func (c *Config) StorageObjectsPageSize() int {
	if c != nil && c.Defaults.StorageObjectsPageSize > 0 {
		return c.Defaults.StorageObjectsPageSize
	}
	return defaultStorageObjectsPageSize
}

// HasDataprocRegions reports whether any region setting applies to Dataproc
// for this project. When false, DataprocRegions falls back to just "global",
// and the lister says so rather than letting the narrow sweep look complete.
func (c *Config) HasDataprocRegions(p Project) bool {
	return firstNonEmpty(p.DataprocRegions, p.Regions, c.Defaults.DataprocRegions, c.Defaults.Regions) != nil
}

func firstNonEmpty(lists ...[]string) []string {
	for _, l := range lists {
		if len(l) > 0 {
			return l
		}
	}
	return nil
}

func withGlobal(regions []string) []string {
	for _, r := range regions {
		if r == "global" {
			return regions
		}
	}
	out := make([]string, 0, len(regions)+1)
	out = append(out, regions...)
	return append(out, "global")
}

// maxConfigBytes bounds the config file. A hand-edited list of projects is
// kilobytes; anything past this is a mistake or a pipe, and reading it
// unbounded is an easy way to be handed /dev/zero.
const maxConfigBytes = 1 << 20

// Load reads and validates the config at path.
func Load(path string) (*Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	// Stat the open file rather than the path: checking the path and then
	// reading it are two lookups, and a config that was safe for the first and
	// swapped before the second is exactly what the check is meant to catch.
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if err := checkPermissions(path, info); err != nil {
		return nil, err
	}

	raw, err := io.ReadAll(io.LimitReader(f, maxConfigBytes+1))
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	if len(raw) > maxConfigBytes {
		return nil, fmt.Errorf("%s is larger than %d bytes", path, maxConfigBytes)
	}

	var cfg Config
	dec := yaml.NewDecoder(strings.NewReader(string(raw)))
	dec.KnownFields(true) // typo in a key should be an error, not a silent default
	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}

	cfg.applyDefaults()
	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("invalid config %s: %w", path, err)
	}
	// Kept so the UI can name the file it is describing. A message telling
	// someone their config has no projects is close to useless if they have to
	// guess which of $G9S_CONFIG, -config and the default was in effect.
	cfg.path = path
	return &cfg, nil
}

// checkPermissions refuses a config that anyone else can write, or that sits in
// a directory anyone else can write.
//
// This is not paranoia about the project IDs in the file: defaults.gcloud_path
// names the binary g9s executes when you press `l`, so write access to the
// config is code execution as you. Write access to the directory is the same
// thing one step removed — the file can simply be replaced. Readable-by-others
// is left alone: that is the default umask on plenty of systems and the
// contents are not secret.
func checkPermissions(path string, info os.FileInfo) error {
	if runtime.GOOS == "windows" {
		// File modes here do not mean what they mean on Unix.
		return nil
	}

	// A fifo would block the read forever and a device would return something
	// that is not a config; neither is a mistake worth following.
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", path)
	}
	if mode := info.Mode().Perm(); mode&0o022 != 0 {
		return fmt.Errorf(
			"%s is writable by group or others (mode %04o); "+
				"defaults.gcloud_path decides which binary g9s runs, so fix it with: chmod 600 %s",
			path, mode, path)
	}

	dir := filepath.Dir(path)
	dirInfo, err := os.Stat(dir)
	if err != nil {
		return err
	}
	// The sticky bit is the exception that makes /tmp usable: it stops one user
	// removing or renaming another's files, so a world-writable sticky
	// directory cannot be used to swap the config out.
	if mode := dirInfo.Mode().Perm(); mode&0o022 != 0 && dirInfo.Mode()&os.ModeSticky == 0 {
		return fmt.Errorf(
			"%s is writable by group or others (mode %04o), so anyone who can write it "+
				"can replace %s; move the config somewhere private or fix it with: chmod go-w %s",
			dir, mode, filepath.Base(path), dir)
	}
	return nil
}

// defaultBigQueryJobWindow is a working day's worth of jobs: long enough to
// cover "what ran overnight", short enough that a busy project's listing is
// still a complete answer rather than a capped one.
const defaultBigQueryJobWindow = 24 * time.Hour

// Cloud Storage recommends no more than 1,000 combined object and prefix rows
// in one response. Half of that keeps an interactive page responsive while
// making a bucket with ordinary directory sizes a compact first view.
const defaultStorageObjectsPageSize = 500

func (c *Config) applyDefaults() {
	if c.Defaults.GcloudPath == "" {
		c.Defaults.GcloudPath = "gcloud"
	}
	if c.Defaults.ListTimeout == 0 {
		c.Defaults.ListTimeout = Duration(90 * time.Second)
	}
	if c.Defaults.BigQueryJobWindow == 0 {
		c.Defaults.BigQueryJobWindow = Duration(defaultBigQueryJobWindow)
	}
	if c.Defaults.StorageObjectsPageSize == 0 {
		c.Defaults.StorageObjectsPageSize = defaultStorageObjectsPageSize
	}
	if c.Defaults.CredentialDir == "" {
		if home, err := os.UserHomeDir(); err == nil {
			c.Defaults.CredentialDir = filepath.Join(home, ".local", "share", "g9s", "credentials")
		}
	}
	c.Defaults.CredentialDir = expandHome(c.Defaults.CredentialDir)
}

func (c *Config) validate() error {
	if len(c.Projects) == 0 {
		return errors.New("no projects defined")
	}
	if c.Defaults.CredentialDir == "" {
		return errors.New("defaults.credential_dir is unset and the home directory could not be determined")
	}
	// applyDefaults fills these when they are zero, so only a negative value
	// reaches here — a refresh that times out before it starts, or a job window
	// that ends in the future and matches nothing.
	if c.Defaults.ListTimeout <= 0 {
		return fmt.Errorf("defaults.list_timeout must be positive, got %s", c.Defaults.ListTimeout.Duration())
	}
	if c.Defaults.BigQueryJobWindow <= 0 {
		return fmt.Errorf("defaults.bigquery_job_window must be positive, got %s", c.Defaults.BigQueryJobWindow.Duration())
	}
	if c.Defaults.StorageObjectsPageSize <= 0 || c.Defaults.StorageObjectsPageSize > 1000 {
		return fmt.Errorf("defaults.storage_objects_page_size must be between 1 and 1000, got %d",
			c.Defaults.StorageObjectsPageSize)
	}

	seenName := map[string]bool{}
	for i, p := range c.Projects {
		where := fmt.Sprintf("projects[%d]", i)
		if p.Name == "" {
			return fmt.Errorf("%s: name is required", where)
		}
		if p.ProjectID == "" {
			return fmt.Errorf("%s (%s): project_id is required", where, p.Name)
		}
		if seenName[p.Name] {
			return fmt.Errorf("%s: duplicate project name %q", where, p.Name)
		}
		seenName[p.Name] = true

		// No regions configured is allowed — the global kinds (Compute, GKE,
		// Storage) need none. The region-scoped listers surface it as a
		// warning in the UI instead, so a narrowed sweep never reads as
		// "nothing is running here".
	}
	return nil
}

// PlaceholderProjectIDs are the project IDs `g9s -init` writes.
//
// They exist here rather than only in the template so the two cannot drift:
// the UI needs to recognise an unedited config, and recognising it by a string
// copied into another file is how that check quietly stops working.
var PlaceholderProjectIDs = []string{"my-sandbox-project", "my-prod-data-project"}

// IsPlaceholder reports whether a project is one `g9s -init` wrote and nobody
// has edited yet.
//
// Worth detecting because an unedited config looks exactly like a working one
// until the first login, which then fails against a project that does not
// exist — and the error comes back from Google talking about permissions,
// which sends people looking in entirely the wrong place.
func IsPlaceholder(p Project) bool {
	for _, id := range PlaceholderProjectIDs {
		if p.ProjectID == id {
			return true
		}
	}
	return false
}

// AllPlaceholders reports whether every configured project is still a
// placeholder, which means the config has not been edited at all.
func (c *Config) AllPlaceholders() bool {
	if len(c.Projects) == 0 {
		return false
	}
	for _, p := range c.Projects {
		if !IsPlaceholder(p) {
			return false
		}
	}
	return true
}

// Project looks up a project by name.
func (c *Config) Project(name string) (Project, bool) {
	for _, p := range c.Projects {
		if p.Name == name {
			return p, true
		}
	}
	return Project{}, false
}

// expandHome resolves a leading ~ to the user's home directory.
//
// Only "~" itself and a "~/" prefix expand. "~other/x" names another user's
// home to a shell, and nothing here can resolve that, so rewriting it to
// "$HOME/other/x" would silently point at a path the user did not ask for.
func expandHome(path string) string {
	if path != "~" && !strings.HasPrefix(path, "~/") {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	if path == "~" {
		return home
	}
	return filepath.Join(home, path[len("~/"):])
}

// DefaultPath resolves the config location: $G9S_CONFIG, then
// $XDG_CONFIG_HOME/g9s/config.yaml, then ~/.config/g9s/config.yaml.
func DefaultPath() string {
	if p := os.Getenv("G9S_CONFIG"); p != "" {
		return expandHome(p)
	}
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return filepath.Join(dir, "g9s", "config.yaml")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "config.yaml"
	}
	return filepath.Join(home, ".config", "g9s", "config.yaml")
}
