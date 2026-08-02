package gcp

import (
	"strings"
	"testing"
	"time"

	"cloud.google.com/go/storage"
)

func TestStorageObjectResourceShape(t *testing.T) {
	attrs := testStorageObject()
	r := storageObjectResource(testProject(), testBucket().Name, "exports/2026/", attrs)

	if r.Name != attrs.Name || r.Location != testBucket().Name {
		t.Errorf("name=%q location=%q", r.Name, r.Location)
	}
	if r.Row[0] != "orders.parquet" || r.Row[1] != "object" {
		t.Errorf("name/type cells = %q/%q", r.Row[0], r.Row[1])
	}
	if r.Row[2] != "64.0MB" || r.Row[3] != "STANDARD" {
		t.Errorf("size/class cells = %q/%q", r.Row[2], r.Row[3])
	}
	if r.Row[5] != "42" {
		t.Errorf("generation = %q, want 42", r.Row[5])
	}
	if !strings.Contains(r.ConsoleURL, "storage/browser/_details/") ||
		!strings.Contains(r.ConsoleURL, "exports/2026/orders.parquet") {
		t.Errorf("console URL = %q", r.ConsoleURL)
	}
}

func TestStorageFolderIsAnImmediateBrowsablePrefix(t *testing.T) {
	r := storageObjectResource(testProject(), testBucket().Name, "exports/",
		&storage.ObjectAttrs{Prefix: "exports/2026/"})

	if r.Row[0] != "2026/" || r.Row[1] != "folder" || r.Status != "FOLDER" {
		t.Errorf("folder row = %#v status=%q", r.Row, r.Status)
	}
	folder, ok := r.Raw.(*StorageObjectPrefix)
	if !ok || folder.Prefix != "exports/2026/" {
		t.Fatalf("folder raw value = %#v", r.Raw)
	}
	if strings.Contains(r.ConsoleURL, "/_details/") {
		t.Errorf("folder URL opens object details: %q", r.ConsoleURL)
	}
}

func TestStorageObjectDirectoryAndSearchQueriesDiffer(t *testing.T) {
	directory := (StorageObjectLister{prefix: "exports/"}).query()
	if directory.Prefix != "exports/" || directory.Delimiter != "/" || !directory.IncludeFoldersAsPrefixes {
		t.Errorf("directory query = %#v", directory)
	}

	search := (StorageObjectLister{
		prefix:    "exports/",
		matchGlob: "exports/**/*.parquet",
	}).query()
	if search.Prefix != "exports/" || search.MatchGlob != "exports/**/*.parquet" {
		t.Errorf("search query = %#v", search)
	}
	if search.Delimiter != "" || search.IncludeFoldersAsPrefixes {
		t.Errorf("search should be flat, got delimiter=%q include-folders=%v",
			search.Delimiter, search.IncludeFoldersAsPrefixes)
	}
}

func TestStorageObjectBrowserNavigatesWithoutChangingItsCacheKey(t *testing.T) {
	bucket := bucketResource(testProject(), testBucket())
	root := BindChild(StorageObjectLister{}, bucket)

	inside, err := ChangeStorageObjectPath(root, "exports/2026")
	if err != nil {
		t.Fatal(err)
	}
	state, ok := StorageObjectState(inside)
	if !ok || state.Prefix != "exports/2026/" {
		t.Fatalf("state after cd = %#v, ok=%v", state, ok)
	}
	if inside.Kind().ID != root.Kind().ID {
		t.Errorf("cd changed cache key from %q to %q", root.Kind().ID, inside.Kind().ID)
	}

	folder := storageObjectPrefixResource(testProject(), testBucket().Name,
		"exports/2026/", "exports/2026/08/")
	inside, opened := OpenStorageObjectFolder(inside, folder)
	if !opened {
		t.Fatal("folder row did not open")
	}
	state, _ = StorageObjectState(inside)
	if state.Prefix != "exports/2026/08/" {
		t.Errorf("opened prefix = %q", state.Prefix)
	}

	inside, moved := ParentStorageObjectPath(inside)
	if !moved {
		t.Fatal("parent path did not move")
	}
	state, _ = StorageObjectState(inside)
	if state.Prefix != "exports/2026/" {
		t.Errorf("parent prefix = %q", state.Prefix)
	}
}

func TestStorageObjectFindIsRelativeAndEscClearsItFirst(t *testing.T) {
	bucket := bucketResource(testProject(), testBucket())
	root := BindChild(StorageObjectLister{}, bucket)
	inside, err := ChangeStorageObjectPath(root, "exports")
	if err != nil {
		t.Fatal(err)
	}
	search, err := FindStorageObjects(inside, "**/*.json")
	if err != nil {
		t.Fatal(err)
	}
	state, _ := StorageObjectState(search)
	if state.MatchGlob != "exports/**/*.json" {
		t.Errorf("glob = %q", state.MatchGlob)
	}

	cleared, moved := ParentStorageObjectPath(search)
	if !moved {
		t.Fatal("back from search did not clear it")
	}
	state, _ = StorageObjectState(cleared)
	if state.MatchGlob != "" || state.Prefix != "exports/" {
		t.Errorf("state after clearing search = %#v", state)
	}
}

func TestStorageObjectCDParentMovesEvenWhenSearchIsActive(t *testing.T) {
	bucket := bucketResource(testProject(), testBucket())
	root := BindChild(StorageObjectLister{}, bucket)
	inside, err := ChangeStorageObjectPath(root, "exports/2026")
	if err != nil {
		t.Fatal(err)
	}
	search, err := FindStorageObjects(inside, "**/*.json")
	if err != nil {
		t.Fatal(err)
	}

	parent, err := ChangeStorageObjectPath(search, "..")
	if err != nil {
		t.Fatal(err)
	}
	state, _ := StorageObjectState(parent)
	if state.MatchGlob != "" || state.Prefix != "exports/" {
		t.Errorf(":cd .. state = %#v", state)
	}
}

func TestStorageObjectAbsolutePathChecksTheBucket(t *testing.T) {
	root := BindChild(StorageObjectLister{}, bucketResource(testProject(), testBucket()))

	inside, err := ChangeStorageObjectPath(root,
		"gs://"+testBucket().Name+"/logs/2026/08")
	if err != nil {
		t.Fatal(err)
	}
	state, _ := StorageObjectState(inside)
	if state.Prefix != "logs/2026/08/" {
		t.Errorf("absolute prefix = %q", state.Prefix)
	}

	if _, err := ChangeStorageObjectPath(root, "gs://another-bucket/logs/"); err == nil {
		t.Error("cd accepted a path from another bucket")
	}
}

func TestStorageObjectContinuationIsOneShot(t *testing.T) {
	root := BindChild(StorageObjectLister{}, bucketResource(testProject(), testBucket()))
	next, ok := ContinueStorageObjects(root, "next-page")
	if !ok {
		t.Fatal("continuation token was rejected")
	}
	_, objects, ok := storageObjectLister(next)
	if !ok || objects.pageToken != "next-page" {
		t.Errorf("continued lister = %#v, ok=%v", objects, ok)
	}
	_, original, _ := storageObjectLister(root)
	if original.pageToken != "" {
		t.Errorf("continuation mutated the active browser: %q", original.pageToken)
	}
}

func TestStorageBucketsOfferObjectsBeforeLifecycle(t *testing.T) {
	children := ChildrenOf("gcs")
	if len(children) != 2 {
		t.Fatalf("Storage has %d child listings, want Objects and Lifecycle", len(children))
	}
	if children[0].Kind().ID != "objects" || children[1].Kind().ID != "lifecycle" {
		t.Errorf("Storage children = %q, %q", children[0].Kind().ID, children[1].Kind().ID)
	}
}

func testStorageObject() *storage.ObjectAttrs {
	return &storage.ObjectAttrs{
		Name:         "exports/2026/orders.parquet",
		Size:         64 << 20,
		StorageClass: "STANDARD",
		Updated:      time.Now().Add(-2 * time.Hour),
		Generation:   42,
	}
}
