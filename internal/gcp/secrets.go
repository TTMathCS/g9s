package gcp

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"google.golang.org/api/option"
	secretmanager "google.golang.org/api/secretmanager/v1"

	"github.com/TTMathCS/g9s/internal/config"
)

// SecretLister lists Secret Manager secrets — names and metadata, never values.
//
// This kind exists because "which secrets are here, and when were they last
// rotated" is an audit question people answer by clicking through the Console.
// The value behind a secret is a different question with a different answer:
// `gcloud secrets versions access`, where the read is logged against your
// identity. g9s does not call AccessSecretVersion at all, so a secret's payload
// never enters this process — not the table, not the detail pane, not the
// clipboard, and not whatever is recording the terminal.
//
// That is enforced by the API surface used, not by filtering after the fact:
// projects.secrets.list returns metadata only. The one thing it does not return
// is a version count, which would cost a versions.list call per secret — a
// round trip per row, for a number the Console shows on the page that `o`
// already opens.
type SecretLister struct{}

func (SecretLister) Kind() Kind {
	return Kind{
		ID:    "secrets",
		Title: "Secret Manager Secrets",
		Columns: []Column{
			{Title: "NAME", Width: 6},
			{Title: "REPLICATION", Width: 3},
			{Title: "ROTATES IN", Width: 2},
			{Title: "STATE", Width: 2},
			{Title: "LABELS", Width: 4},
			{Title: "AGE", Width: 2},
		},
	}
}

func (SecretLister) List(ctx context.Context, _ *config.Config, p config.Project, opts []option.ClientOption) (Result, error) {
	svc, err := secretmanager.NewService(ctx, opts...)
	if err != nil {
		return Result{}, fmt.Errorf("secret manager client: %w", err)
	}

	var result Result
	parent := "projects/" + p.ProjectID
	err = svc.Projects.Secrets.List(parent).Pages(ctx, func(page *secretmanager.ListSecretsResponse) error {
		for _, s := range page.Secrets {
			result.Resources = append(result.Resources, secretResource(p, s))
		}
		return nil
	})
	if err != nil {
		return result, err
	}

	sortResources(result.Resources)
	return result, nil
}

func secretResource(p config.Project, s *secretmanager.Secret) Resource {
	// The API returns "projects/<project>/secrets/<name>".
	name := lastSegment(s.Name)

	return Resource{
		Name: name,
		// Secrets are global; a user-managed replication policy pins the
		// material to chosen regions, which the REPLICATION column reports.
		Location: "global",
		Status:   secretState(s),
		Row: []string{
			name,
			secretReplication(s),
			secretRotatesIn(s),
			secretState(s),
			formatLabels(s.Labels),
			age(s.CreateTime),
		},
		// Metadata only. See the type comment: the payload is never fetched, so
		// there is none here to leak into the detail pane or the clipboard.
		Raw: s,
		ConsoleURL: fmt.Sprintf(
			"https://console.cloud.google.com/security/secret-manager/secret/%s/versions?project=%s",
			url.PathEscape(name), url.QueryEscape(p.ProjectID)),
	}
}

// secretState reports whether the secret is on its way out.
//
// Most secrets have no expiry and are simply ACTIVE. One that does is worth
// drawing the eye to, because an expired secret is deleted along with every
// version of it, and finding that out from a failing workload is expensive.
func secretState(s *secretmanager.Secret) string {
	if s.ExpireTime == "" {
		return "ACTIVE"
	}
	expiry, err := time.Parse(time.RFC3339, s.ExpireTime)
	if err != nil {
		return "ACTIVE"
	}
	if time.Now().After(expiry) {
		return "EXPIRED"
	}
	return "EXPIRING"
}

// secretReplication summarises where the material lives. "automatic" means
// Google chooses; user-managed means someone picked regions, usually for a data
// residency rule, and which regions is the whole point of having picked.
func secretReplication(s *secretmanager.Secret) string {
	if s.Replication == nil {
		return "-"
	}
	if s.Replication.Automatic != nil {
		return "automatic"
	}
	if um := s.Replication.UserManaged; um != nil {
		locations := make([]string, 0, len(um.Replicas))
		for _, r := range um.Replicas {
			if r.Location != "" {
				locations = append(locations, r.Location)
			}
		}
		if len(locations) == 0 {
			return "user-managed"
		}
		return strings.Join(locations, ",")
	}
	return "-"
}

// secretRotatesIn renders the time until the next scheduled rotation, which is
// the standing audit question about a secret. A rotation whose time has passed
// is called out rather than shown as a negative age: it means the schedule is
// set but the notification is not being acted on.
func secretRotatesIn(s *secretmanager.Secret) string {
	if s.Rotation == nil || s.Rotation.NextRotationTime == "" {
		return "-"
	}
	next, err := time.Parse(time.RFC3339, s.Rotation.NextRotationTime)
	if err != nil {
		return "-"
	}
	if until := time.Until(next); until > 0 {
		return shortDuration(until)
	}
	return "overdue"
}
