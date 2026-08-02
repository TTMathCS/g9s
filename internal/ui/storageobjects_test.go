package ui

import (
	"errors"
	"strings"
	"testing"

	"cloud.google.com/go/storage"

	"github.com/TTMathCS/g9s/internal/auth"
	"github.com/TTMathCS/g9s/internal/config"
	"github.com/TTMathCS/g9s/internal/gcp"
)

func storageObjectModel(t *testing.T) Model {
	t.Helper()
	cfg := &config.Config{
		Defaults: config.Defaults{StorageObjectsPageSize: 500},
		Projects: []config.Project{{Name: "sandbox", ProjectID: "sandbox-123"}},
	}
	m := New(cfg, nil)
	m.width, m.height = 132, 30
	m.active, m.hasActive = cfg.Projects[0], true
	m.authStatus["sandbox"] = auth.Status{State: auth.StateValid}
	m.screen = screenResources
	for i, kind := range m.tabs() {
		if kind.ID == "gcs" {
			m.kindIdx = i
			break
		}
	}
	m.cache["gcs"] = gcp.Result{Resources: []gcp.Resource{{
		Name:     "sample-bucket",
		Location: "US",
		Status:   "ACTIVE",
		KindID:   "gcs",
		Row:      []string{"sample-bucket", "US", "STANDARD", "off", "10d"},
		Raw:      &storage.BucketAttrs{Name: "sample-bucket", Location: "US"},
	}}}

	m = press(m, "enter")
	if m.drill == nil || m.currentKind().ID != "objects/sample-bucket" {
		t.Fatal("bucket did not open its Objects listing")
	}
	id := m.currentKind().ID
	m.loading[id] = false
	m.cache[id] = gcp.Result{
		Resources: []gcp.Resource{
			{
				Name:     "logs/",
				Location: "sample-bucket",
				Status:   "FOLDER",
				KindID:   id,
				Row:      []string{"logs/", "folder", "-", "-", "-", "-"},
				Raw: &gcp.StorageObjectPrefix{
					Bucket: "sample-bucket",
					Prefix: "logs/",
					Type:   "folder prefix",
				},
			},
			{
				Name:     "README.txt",
				Location: "sample-bucket",
				Status:   "LIVE",
				KindID:   id,
				Row:      []string{"README.txt", "object", "12B", "STANDARD", "1d", "1"},
				Raw:      &storage.ObjectAttrs{Name: "README.txt", Size: 12},
			},
		},
		NextPageToken: "page-2",
	}
	return m
}

func TestStorageFolderEnterAndEscNavigatePaths(t *testing.T) {
	m := storageObjectModel(t)
	m = press(m, "enter")

	state, ok := gcp.StorageObjectState(m.drill.lister)
	if !ok || state.Prefix != "logs/" {
		t.Fatalf("state after enter = %#v, ok=%v", state, ok)
	}
	if _, stale := m.cache[m.currentKind().ID]; stale {
		t.Error("folder navigation left the root page under the new breadcrumb")
	}

	m = press(m, "esc")
	if m.drill == nil {
		t.Fatal("esc from a folder left the bucket instead of moving to root")
	}
	state, _ = gcp.StorageObjectState(m.drill.lister)
	if state.Prefix != "" {
		t.Errorf("prefix after esc = %q, want root", state.Prefix)
	}

	m = press(m, "esc")
	if m.drill != nil {
		t.Error("esc at bucket root did not return to the bucket table")
	}
}

func TestStorageObjectCommandsChangeServerQuery(t *testing.T) {
	m := storageObjectModel(t)
	m = typeCommand(t, m, "cd logs/2026/08")
	state, _ := gcp.StorageObjectState(m.drill.lister)
	if state.Prefix != "logs/2026/08/" {
		t.Errorf(":cd prefix = %q", state.Prefix)
	}

	m.loading[m.currentKind().ID] = false
	m = typeCommand(t, m, "find **/*.json")
	state, _ = gcp.StorageObjectState(m.drill.lister)
	if state.MatchGlob != "logs/2026/08/**/*.json" {
		t.Errorf(":find glob = %q", state.MatchGlob)
	}

	m.loading[m.currentKind().ID] = false
	m = press(m, "esc")
	state, _ = gcp.StorageObjectState(m.drill.lister)
	if state.MatchGlob != "" || state.Prefix != "logs/2026/08/" {
		t.Errorf("esc should clear find before moving paths: %#v", state)
	}
}

func TestStorageObjectLoadMoreAppendsAndKeepsContinuation(t *testing.T) {
	m := storageObjectModel(t)
	id := m.currentKind().ID
	firstCount := len(m.cache[id].Resources)

	m = press(m, " ")
	if !m.loading[id] {
		t.Fatal("space did not start the continuation page")
	}
	token := m.refreshToken[id]
	landed, _ := m.handleResources(resourcesMsg{
		project:    "sandbox",
		kind:       id,
		token:      token,
		appendPage: true,
		result: gcp.Result{
			Resources:    []gcp.Resource{{Name: "third.txt", Row: []string{"third.txt", "object", "1B", "STANDARD", "1m", "2"}}},
			NextPageToken: "page-3",
		},
	})
	m = landed.(Model)

	if got := len(m.cache[id].Resources); got != firstCount+1 {
		t.Errorf("rows after load more = %d, want %d", got, firstCount+1)
	}
	if got := m.cache[id].NextPageToken; got != "page-3" {
		t.Errorf("next token = %q", got)
	}
}

func TestStorageObjectLoadMoreFailureKeepsLoadedPages(t *testing.T) {
	m := storageObjectModel(t)
	id := m.currentKind().ID
	want := m.cache[id]
	m = press(m, " ")

	landed, _ := m.handleResources(resourcesMsg{
		project:    "sandbox",
		kind:       id,
		token:      m.refreshToken[id],
		appendPage: true,
		err:        errors.New("temporary failure"),
	})
	m = landed.(Model)

	got := m.cache[id]
	if len(got.Resources) != len(want.Resources) || got.NextPageToken != want.NextPageToken {
		t.Errorf("failed continuation changed cached page: got %#v, want %#v", got, want)
	}
}

func TestStorageObjectPathSurvivesSiblingTabs(t *testing.T) {
	m := storageObjectModel(t)
	m = press(m, "enter")
	m.loading[m.currentKind().ID] = false

	m = press(m, "tab")
	if m.currentKind().ID != "lifecycle/sample-bucket" {
		t.Fatalf("tab landed on %q, want Lifecycle", m.currentKind().ID)
	}
	m = press(m, "tab")
	state, ok := gcp.StorageObjectState(m.drill.lister)
	if !ok || state.Prefix != "logs/" {
		t.Errorf("Objects path after sibling round trip = %#v, ok=%v", state, ok)
	}
}

func TestStorageObjectViewShowsPathAndIncompleteCount(t *testing.T) {
	m := storageObjectModel(t)
	view := m.View()
	for _, want := range []string{"gs://sample-bucket/", "2+", "space next page"} {
		if !strings.Contains(view, want) {
			t.Errorf("object browser does not show %q:\n%s", want, view)
		}
	}
}

func TestStorageObjectCacheIsDiscardedWhenLeavingTheDrill(t *testing.T) {
	m := storageObjectModel(t)
	id := m.currentKind().ID
	m = press(m, "esc")
	if _, cached := m.cache[id]; cached {
		t.Error("leaving Objects retained a query-shaped cache entry")
	}
}

func TestStorageObjectCacheIsDiscardedByProjectsCommand(t *testing.T) {
	m := storageObjectModel(t)
	id := m.currentKind().ID
	m = typeCommand(t, m, "projects")
	if m.screen != screenProjects {
		t.Fatalf(":projects landed on screen %v", m.screen)
	}
	if _, cached := m.cache[id]; cached {
		t.Error(":projects retained a query-shaped object cache entry")
	}
}
