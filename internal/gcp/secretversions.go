package gcp

import (
	"context"
	"fmt"
	"net/url"
	"time"

	"google.golang.org/api/option"
	secretmanager "google.golang.org/api/secretmanager/v1"

	"github.com/TTMathCS/g9s/internal/config"
)

// SecretVersionLister is the versions of one secret.
//
// **Metadata only, exactly like the parent.** projects.secrets.versions.list
// returns names, states and timestamps — there is no payload in the response to
// leak, and AccessSecretVersion is not called here any more than it is in
// secrets.go. TestNoSecretFileFetchesAValue scans every file in this package,
// so this file is covered by the same structural guarantee.
//
// What it adds over the parent row: the secrets table shows the rotation
// *policy*, this shows whether rotation actually happened. A secret configured
// to rotate every 30 days whose newest enabled version is eight months old
// looks perfectly healthy one level up.
type SecretVersionLister struct{}

func (SecretVersionLister) ParentKind() string { return "secrets" }

func (SecretVersionLister) Kind() Kind {
	return Kind{
		ID:    "secretversions",
		Title: "Versions",
		Columns: []Column{
			{Title: "VERSION", Width: 1},
			{Title: "STATE", Width: 2},
			{Title: "CREATED", Width: 2},
			{Title: "DESTROYED", Width: 2},
			{Title: "ENCRYPTION", Width: 3},
		},
	}
}

func (SecretVersionLister) List(ctx context.Context, _ *config.Config, p config.Project, parent Resource, opts []option.ClientOption) (Result, error) {
	secret, ok := parent.Raw.(*secretmanager.Secret)
	if !ok {
		return Result{}, fmt.Errorf("no secret data for %s", parent.Name)
	}

	svc, err := secretmanager.NewService(ctx, opts...)
	if err != nil {
		return Result{}, fmt.Errorf("secret manager client: %w", err)
	}

	var result Result
	err = svc.Projects.Secrets.Versions.List(secret.Name).
		Pages(ctx, func(page *secretmanager.ListSecretVersionsResponse) error {
			for _, v := range page.Versions {
				if v != nil {
					result.Resources = append(result.Resources, secretVersionResource(p, parent, v))
				}
			}
			return nil
		})
	if err != nil {
		return result, err
	}

	sortSecretVersions(result.Resources)
	return result, nil
}

func secretVersionResource(p config.Project, parent Resource, v *secretmanager.SecretVersion) Resource {
	// Versions are numbered, and the number is how they are referred to
	// everywhere — in a workload's config, in gcloud, in the Console.
	version := lastSegment(v.Name)

	state := v.State
	if state == "" {
		state = "UNKNOWN"
	}

	// A version scheduled for destruction is still readable until the date
	// arrives, which is the window where a workload still pinned to it works
	// and is about to stop.
	destroyed := "-"
	switch {
	case v.DestroyTime != "":
		destroyed = age(v.DestroyTime) + " ago"
	case v.ScheduledDestroyTime != "":
		destroyed = "in " + untilRFC3339(v.ScheduledDestroyTime)
	}

	return Resource{
		Name:     version,
		Location: parent.Name,
		Status:   state,
		Row: []string{
			version,
			state,
			age(v.CreateTime),
			destroyed,
			secretVersionEncryption(v),
		},
		// The metadata object, which has no payload field on it at all — see
		// the package-wide test.
		Raw: v,
		ConsoleURL: fmt.Sprintf(
			"https://console.cloud.google.com/security/secret-manager/secret/%s/versions?project=%s",
			url.PathEscape(parent.Name), url.QueryEscape(p.ProjectID)),
	}
}

// secretVersionEncryption names the customer-managed key, if there is one.
//
// Per version rather than per secret because a rotation can change it: a secret
// whose newest version is on a new key while older enabled versions are still
// on the retired one is exactly the state a key deletion breaks.
func secretVersionEncryption(v *secretmanager.SecretVersion) string {
	if cme := v.CustomerManagedEncryption; cme != nil && cme.KmsKeyVersionName != "" {
		return lastSegment(cme.KmsKeyVersionName)
	}
	if rs := v.ReplicationStatus; rs != nil {
		if a := rs.Automatic; a != nil && a.CustomerManagedEncryption != nil {
			if name := a.CustomerManagedEncryption.KmsKeyVersionName; name != "" {
				return lastSegment(name)
			}
		}
	}
	return "google-managed"
}

// untilRFC3339 renders how long until a timestamp, or "now" once it has passed.
func untilRFC3339(timestamp string) string {
	t, err := time.Parse(time.RFC3339, timestamp)
	if err != nil {
		return timestamp
	}
	if d := time.Until(t); d > 0 {
		return shortDuration(d)
	}
	return "now"
}

// sortSecretVersions puts the newest version first.
//
// Numbers, not strings: version 10 sorts before version 9 alphabetically, which
// puts the version a workload is most likely pinned to in the wrong place.
func sortSecretVersions(resources []Resource) {
	stableSortBy(resources, func(a, b Resource) bool {
		na, oka := atoiSafe(a.Name)
		nb, okb := atoiSafe(b.Name)
		if oka && okb && na != nb {
			return na > nb
		}
		if oka != okb {
			// A non-numeric name is not something the API produces; if one ever
			// appears it goes last rather than scrambling the numbered ones.
			return oka
		}
		return a.Name < b.Name
	})
}

// atoiSafe parses a positive integer, reporting whether the whole string was
// one.
func atoiSafe(s string) (int, bool) {
	if s == "" {
		return 0, false
	}
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, false
		}
		n = n*10 + int(r-'0')
	}
	return n, true
}
