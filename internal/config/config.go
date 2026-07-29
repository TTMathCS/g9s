// Package config loads and validates the g9s configuration file.
package config

import (
	"errors"
	"fmt"
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
	// ListTimeout bounds a single refresh across all regions.
	ListTimeout Duration `yaml:"list_timeout"`
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

	Regions           []string `yaml:"regions"`
	DataprocRegions   []string `yaml:"dataproc_regions"`
	ComposerLocations []string `yaml:"composer_locations"`
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

// ComposerLocations returns the locations to sweep for Composer environments.
func (c *Config) ComposerLocations(p Project) []string {
	return firstNonEmpty(p.ComposerLocations, p.Regions, c.Defaults.ComposerLocations, c.Defaults.Regions)
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

// Load reads and validates the config at path.
func Load(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if err := checkPermissions(path); err != nil {
		return nil, err
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
	return &cfg, nil
}

// checkPermissions refuses a config that anyone else can write.
//
// This is not paranoia about the project IDs in the file: defaults.gcloud_path
// names the binary g9s executes when you press `l`, so write access to the
// config is code execution as you. Readable-by-others is left alone — that is
// the default umask on plenty of systems and the contents are not secret.
func checkPermissions(path string) error {
	if runtime.GOOS == "windows" {
		// File modes here do not mean what they mean on Unix.
		return nil
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if mode := info.Mode().Perm(); mode&0o022 != 0 {
		return fmt.Errorf(
			"%s is writable by group or others (mode %04o); "+
				"defaults.gcloud_path decides which binary g9s runs, so fix it with: chmod 600 %s",
			path, mode, path)
	}
	return nil
}

func (c *Config) applyDefaults() {
	if c.Defaults.GcloudPath == "" {
		c.Defaults.GcloudPath = "gcloud"
	}
	if c.Defaults.ListTimeout == 0 {
		c.Defaults.ListTimeout = Duration(90 * time.Second)
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

// Project looks up a project by name.
func (c *Config) Project(name string) (Project, bool) {
	for _, p := range c.Projects {
		if p.Name == name {
			return p, true
		}
	}
	return Project{}, false
}

func expandHome(path string) string {
	if !strings.HasPrefix(path, "~") {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	return filepath.Join(home, strings.TrimPrefix(path, "~"))
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
