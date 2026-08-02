package gcp

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"unicode"

	"cloud.google.com/go/storage"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"

	"github.com/TTMathCS/g9s/internal/config"
)

// StorageObjectLister is a paged, directory-shaped view of one bucket.
//
// It is deliberately a bucket drill-down rather than a top-level resource
// kind. A project can contain billions of objects, so fetching them while the
// dashboard loads would be both slow and the wrong operational question. The
// bucket row supplies the scope; this lister fetches one path and one page only
// when that bucket is opened.
type StorageObjectLister struct {
	prefix    string
	matchGlob string
	pageToken string
}

func (StorageObjectLister) ParentKind() string { return "gcs" }

func (StorageObjectLister) Kind() Kind {
	return Kind{
		ID:    "objects",
		Title: "Objects",
		Columns: []Column{
			{Title: "NAME", Width: 7},
			{Title: "TYPE", Width: 2},
			{Title: "SIZE", Width: 2},
			{Title: "STORAGE CLASS", Width: 3},
			{Title: "UPDATED", Width: 2},
			{Title: "GENERATION", Width: 3},
		},
	}
}

func (l StorageObjectLister) List(ctx context.Context, cfg *config.Config, p config.Project, parent Resource, opts []option.ClientOption) (Result, error) {
	bucket, ok := parent.Raw.(*storage.BucketAttrs)
	if !ok {
		return Result{}, fmt.Errorf("no bucket data for %s", parent.Name)
	}

	client, err := storage.NewClient(ctx, opts...)
	if err != nil {
		return Result{}, fmt.Errorf("storage objects client: %w", err)
	}
	defer client.Close()

	it := client.Bucket(bucket.Name).Objects(ctx, l.query())
	pager := iterator.NewPager(it, cfg.StorageObjectsPageSize(), l.pageToken)

	var attrs []*storage.ObjectAttrs
	nextPageToken, err := pager.NextPage(&attrs)
	if err != nil {
		return Result{}, err
	}

	result := Result{NextPageToken: nextPageToken}
	for _, attr := range attrs {
		if attr == nil {
			continue
		}
		result.Resources = append(result.Resources,
			storageObjectResource(p, bucket.Name, l.prefix, attr))
	}
	return result, nil
}

// query uses directory mode for ordinary browsing and a flat result set for
// glob search. Delimiter="/" makes immediate child prefixes appear as rows;
// omitting it during a search lets ** cross any number of path components.
func (l StorageObjectLister) query() *storage.Query {
	q := &storage.Query{
		Prefix:     l.prefix,
		Projection: storage.ProjectionNoACL,
	}
	if l.matchGlob != "" {
		q.MatchGlob = l.matchGlob
		return q
	}
	q.Delimiter = "/"
	q.IncludeFoldersAsPrefixes = true
	return q
}

// StorageObjectPrefix is the metadata shown when describing a folder row.
// Prefix rows are returned by objects.list but are not ObjectAttrs: in a flat
// namespace they may be only a shared name prefix, while hierarchical and
// managed folders are real resources. Keeping that distinction explicit is
// more honest than manufacturing object metadata for them.
type StorageObjectPrefix struct {
	Bucket string `yaml:"bucket"`
	Prefix string `yaml:"prefix"`
	Type   string `yaml:"type"`
}

func storageObjectResource(p config.Project, bucket, currentPrefix string, attrs *storage.ObjectAttrs) Resource {
	if attrs.Prefix != "" {
		return storageObjectPrefixResource(p, bucket, currentPrefix, attrs.Prefix)
	}

	name := strings.TrimPrefix(attrs.Name, currentPrefix)
	if name == "" {
		name = attrs.Name
	}
	ageCell := "-"
	if !attrs.Updated.IsZero() {
		ageCell = shortDuration(timeSince(attrs.Updated))
	}
	generation := "-"
	if attrs.Generation != 0 {
		generation = fmt.Sprintf("%d", attrs.Generation)
	}
	class := attrs.StorageClass
	if class == "" {
		class = "-"
	}

	return Resource{
		Name:     attrs.Name,
		Location: bucket,
		Status:   "LIVE",
		Row: []string{
			name,
			"object",
			humanBytes(attrs.Size),
			class,
			ageCell,
			generation,
		},
		Raw:        attrs,
		ConsoleURL: storageObjectDetailsURL(p.ProjectID, bucket, attrs.Name),
	}
}

func storageObjectPrefixResource(p config.Project, bucket, currentPrefix, prefix string) Resource {
	name := strings.TrimPrefix(prefix, currentPrefix)
	if name == "" {
		name = prefix
	}

	return Resource{
		Name:     prefix,
		Location: bucket,
		Status:   "FOLDER",
		Row: []string{
			name,
			"folder",
			"-",
			"-",
			"-",
			"-",
		},
		Raw: &StorageObjectPrefix{
			Bucket: bucket,
			Prefix: prefix,
			Type:   "folder prefix",
		},
		ConsoleURL: storageObjectBrowserURL(p.ProjectID, bucket, prefix),
	}
}

func storageObjectDetailsURL(projectID, bucket, name string) string {
	return storageObjectConsoleURL(projectID, "_details", bucket, name)
}

func storageObjectBrowserURL(projectID, bucket, prefix string) string {
	return storageObjectConsoleURL(projectID, "", bucket, prefix)
}

func storageObjectConsoleURL(projectID, view, bucket, name string) string {
	parts := make([]string, 0, 3+strings.Count(name, "/"))
	if view != "" {
		parts = append(parts, view)
	}
	parts = append(parts, url.PathEscape(bucket))
	for _, part := range strings.Split(name, "/") {
		parts = append(parts, url.PathEscape(part))
	}
	return fmt.Sprintf("https://console.cloud.google.com/storage/browser/%s?project=%s",
		strings.Join(parts, "/"), url.QueryEscape(projectID))
}

// StorageObjectBrowseState is the query represented by an open Objects
// listing. Prefix is the directory currently being viewed. MatchGlob is empty
// during directory browsing and contains the full-bucket glob during search.
type StorageObjectBrowseState struct {
	Bucket    string
	Prefix    string
	MatchGlob string
}

// StorageObjectState reports whether a bound lister is the object browser and,
// if so, which bucket query it represents.
func StorageObjectState(l Lister) (StorageObjectBrowseState, bool) {
	b, objects, ok := storageObjectLister(l)
	if !ok {
		return StorageObjectBrowseState{}, false
	}
	return StorageObjectBrowseState{
		Bucket:    b.parent.Name,
		Prefix:    objects.prefix,
		MatchGlob: objects.matchGlob,
	}, true
}

// OpenStorageObjectFolder changes an Objects listing to the selected folder.
func OpenStorageObjectFolder(l Lister, r Resource) (Lister, bool) {
	b, objects, ok := storageObjectLister(l)
	if !ok {
		return nil, false
	}
	folder, ok := r.Raw.(*StorageObjectPrefix)
	if !ok || folder.Bucket != b.parent.Name {
		return nil, false
	}
	objects.prefix = folder.Prefix
	objects.matchGlob = ""
	objects.pageToken = ""
	b.child = objects
	return b, true
}

// ParentStorageObjectPath backs out of a search first, then one path segment.
// False means the browser is already at the bucket root and the UI should
// close the bucket drill-down instead.
func ParentStorageObjectPath(l Lister) (Lister, bool) {
	b, objects, ok := storageObjectLister(l)
	if !ok {
		return nil, false
	}
	if objects.matchGlob != "" {
		objects.matchGlob = ""
		objects.pageToken = ""
		b.child = objects
		return b, true
	}
	if objects.prefix == "" {
		return nil, false
	}

	trimmed := strings.TrimSuffix(objects.prefix, "/")
	if slash := strings.LastIndex(trimmed, "/"); slash >= 0 {
		objects.prefix = trimmed[:slash+1]
	} else {
		objects.prefix = ""
	}
	objects.pageToken = ""
	b.child = objects
	return b, true
}

// ChangeStorageObjectPath applies a :cd argument. Paths are relative to the
// current prefix unless they start with / or gs://. An empty path means the
// bucket root, matching a shell's bare cd returning to its starting point.
func ChangeStorageObjectPath(l Lister, input string) (Lister, error) {
	b, objects, ok := storageObjectLister(l)
	if !ok {
		return nil, fmt.Errorf(":cd is only available in Storage Objects")
	}

	input = strings.TrimSpace(input)
	if input == ".." {
		// :cd .. names a path operation, so unlike esc it moves immediately
		// even when a search is active. Clear the glob before asking for the
		// parent prefix.
		if objects.matchGlob != "" {
			objects.matchGlob = ""
			objects.pageToken = ""
			b.child = objects
			l = b
		}
		if parent, moved := ParentStorageObjectPath(l); moved {
			return parent, nil
		}
		return l, nil
	}

	absolute := input == "" || strings.HasPrefix(input, "/")
	if strings.HasPrefix(input, "gs://") {
		parsed, err := url.Parse(input)
		if err != nil {
			return nil, fmt.Errorf("invalid Storage path: %w", err)
		}
		if parsed.Host != b.parent.Name {
			return nil, fmt.Errorf("path names bucket %q, but this browser is %q",
				parsed.Host, b.parent.Name)
		}
		input = parsed.Path
		absolute = true
	}
	input = strings.TrimLeft(input, "/")
	if strings.IndexFunc(input, unicode.IsControl) >= 0 {
		return nil, fmt.Errorf("Storage path contains a control character")
	}

	prefix := input
	if !absolute {
		prefix = objects.prefix + input
	}
	if prefix != "" && !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	if len([]byte(prefix)) > 1024 {
		return nil, fmt.Errorf("Storage path is longer than 1024 bytes")
	}

	objects.prefix = prefix
	objects.matchGlob = ""
	objects.pageToken = ""
	b.child = objects
	return b, nil
}

// FindStorageObjects applies a server-side glob relative to the current path.
// A leading slash searches from the bucket root. An empty glob clears search.
func FindStorageObjects(l Lister, pattern string) (Lister, error) {
	b, objects, ok := storageObjectLister(l)
	if !ok {
		return nil, fmt.Errorf(":find is only available in Storage Objects")
	}

	pattern = strings.TrimSpace(pattern)
	if strings.IndexFunc(pattern, unicode.IsControl) >= 0 {
		return nil, fmt.Errorf("Storage glob contains a control character")
	}
	if strings.HasPrefix(pattern, "/") {
		objects.prefix = ""
		pattern = strings.TrimLeft(pattern, "/")
	}
	matchGlob := ""
	if pattern != "" {
		matchGlob = objects.prefix + pattern
	}
	if len([]byte(matchGlob)) > 1024 {
		return nil, fmt.Errorf("Storage glob is longer than 1024 bytes")
	}

	objects.matchGlob = matchGlob
	objects.pageToken = ""
	b.child = objects
	return b, nil
}

// ContinueStorageObjects creates the one-shot lister for a subsequent API
// page. The UI does not install it as the active browser: refresh must always
// restart the current path at page one rather than refresh page two alone.
func ContinueStorageObjects(l Lister, pageToken string) (Lister, bool) {
	b, objects, ok := storageObjectLister(l)
	if !ok || pageToken == "" {
		return nil, false
	}
	objects.pageToken = pageToken
	b.child = objects
	return b, true
}

func storageObjectLister(l Lister) (boundChild, StorageObjectLister, bool) {
	b, ok := l.(boundChild)
	if !ok {
		return boundChild{}, StorageObjectLister{}, false
	}
	objects, ok := b.child.(StorageObjectLister)
	return b, objects, ok
}
