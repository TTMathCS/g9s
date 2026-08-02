package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStorageObjectsPageSizeDefaultsTo500(t *testing.T) {
	var nilConfig *Config
	if got := nilConfig.StorageObjectsPageSize(); got != 500 {
		t.Errorf("nil config page size = %d, want 500", got)
	}
	if got := (&Config{}).StorageObjectsPageSize(); got != 500 {
		t.Errorf("zero config page size = %d, want 500", got)
	}
}

func TestStorageObjectsPageSizeSurvivesStrictLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "g9s.yaml")
	err := os.WriteFile(path, []byte(`
defaults:
  storage_objects_page_size: 250
projects:
  - name: sandbox
    project_id: sandbox-123
`), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("strict loader rejected storage_objects_page_size: %v", err)
	}
	if got := cfg.StorageObjectsPageSize(); got != 250 {
		t.Errorf("page size = %d, want 250", got)
	}
}

func TestStorageObjectsPageSizeStaysWithinAllowedRange(t *testing.T) {
	for _, size := range []int{-1, 1001} {
		cfg := &Config{
			Defaults: Defaults{StorageObjectsPageSize: size},
			Projects: []Project{{Name: "sandbox", ProjectID: "sandbox-123"}},
		}
		cfg.applyDefaults()
		err := cfg.validate()
		if err == nil || !strings.Contains(err.Error(), "storage_objects_page_size") {
			t.Errorf("size %d validation error = %v", size, err)
		}
	}
}
