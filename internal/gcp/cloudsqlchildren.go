package gcp

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"google.golang.org/api/option"
	sqladmin "google.golang.org/api/sqladmin/v1"

	"github.com/TTMathCS/g9s/internal/config"
)

// SQLDatabaseLister is the databases inside one Cloud SQL instance.
//
// One call, made on demand. Listing every database in the project would be N
// calls on every refresh to answer a question that is always asked about one
// instance — "does this instance have the schema I expect on it" — so this is
// the shape the question actually has.
type SQLDatabaseLister struct{}

func (SQLDatabaseLister) ParentKind() string { return "sql" }

func (SQLDatabaseLister) Kind() Kind {
	return Kind{
		ID:    "sqldbs",
		Title: "Databases",
		Columns: []Column{
			{Title: "NAME", Width: 4},
			{Title: "CHARSET", Width: 2},
			{Title: "COLLATION", Width: 3},
		},
	}
}

func (SQLDatabaseLister) List(ctx context.Context, _ *config.Config, p config.Project, parent Resource, opts []option.ClientOption) (Result, error) {
	inst, ok := parent.Raw.(*sqladmin.DatabaseInstance)
	if !ok {
		return Result{}, fmt.Errorf("no instance data for %s", parent.Name)
	}

	svc, err := sqladmin.NewService(ctx, opts...)
	if err != nil {
		return Result{}, fmt.Errorf("cloud sql client: %w", err)
	}

	// Not paginated: the API returns every database in one response.
	resp, err := svc.Databases.List(p.ProjectID, inst.Name).Context(ctx).Do()
	if err != nil {
		return Result{}, err
	}

	var result Result
	for _, db := range resp.Items {
		if db == nil {
			continue
		}
		result.Resources = append(result.Resources, sqlDatabaseResource(p, inst, db))
	}
	sortResources(result.Resources)
	return result, nil
}

func sqlDatabaseResource(p config.Project, inst *sqladmin.DatabaseInstance, db *sqladmin.Database) Resource {
	return Resource{
		Name:     db.Name,
		Location: inst.Name,
		// A database has no lifecycle state; the instance holds that.
		Status: "ACTIVE",
		Row: []string{
			db.Name,
			dashIfEmpty(db.Charset),
			dashIfEmpty(db.Collation),
		},
		Raw: db,
		ConsoleURL: fmt.Sprintf(
			"https://console.cloud.google.com/sql/instances/%s/databases?project=%s",
			url.PathEscape(inst.Name), url.QueryEscape(p.ProjectID)),
	}
}

// SQLUserLister is the user accounts on one Cloud SQL instance.
//
// The second drill-down on the same parent, and the reason a row is allowed
// more than one: databases and users are both things an instance contains and
// neither is a sub-listing of the other. tab moves between them.
//
// No password material is involved. users.list returns the `password` field
// empty, and the detail pane redacts it regardless — see secretFields.
type SQLUserLister struct{}

func (SQLUserLister) ParentKind() string { return "sql" }

func (SQLUserLister) Kind() Kind {
	return Kind{
		ID:    "sqlusers",
		Title: "Users",
		Columns: []Column{
			{Title: "NAME", Width: 4},
			{Title: "HOST", Width: 2},
			{Title: "TYPE", Width: 3},
			{Title: "ROLES", Width: 3},
			{Title: "STATUS", Width: 2},
		},
	}
}

func (SQLUserLister) List(ctx context.Context, _ *config.Config, p config.Project, parent Resource, opts []option.ClientOption) (Result, error) {
	inst, ok := parent.Raw.(*sqladmin.DatabaseInstance)
	if !ok {
		return Result{}, fmt.Errorf("no instance data for %s", parent.Name)
	}

	svc, err := sqladmin.NewService(ctx, opts...)
	if err != nil {
		return Result{}, fmt.Errorf("cloud sql client: %w", err)
	}

	resp, err := svc.Users.List(p.ProjectID, inst.Name).Context(ctx).Do()
	if err != nil {
		return Result{}, err
	}

	var result Result
	for _, u := range resp.Items {
		if u == nil {
			continue
		}
		result.Resources = append(result.Resources, sqlUserResource(p, inst, u))
	}
	sortResources(result.Resources)
	return result, nil
}

func sqlUserResource(p config.Project, inst *sqladmin.DatabaseInstance, u *sqladmin.User) Resource {
	// MySQL scopes a user to a host; Postgres and SQL Server do not and leave
	// it blank. "%" is the wildcard, which is worth showing as itself rather
	// than as an empty cell that reads like "not set".
	host := u.Host
	if host == "" {
		host = "-"
	}

	return Resource{
		Name:     u.Name,
		Location: inst.Name,
		Status:   sqlUserStatus(u),
		Row: []string{
			u.Name,
			host,
			sqlUserType(u),
			sqlUserRoles(u),
			sqlUserStatus(u),
		},
		Raw: u,
		ConsoleURL: fmt.Sprintf(
			"https://console.cloud.google.com/sql/instances/%s/users?project=%s",
			url.PathEscape(inst.Name), url.QueryEscape(p.ProjectID)),
	}
}

// sqlUserType distinguishes a password held in the database from an identity
// held in IAM — the difference between a credential you rotate and one you
// revoke, and the field most worth a column here.
func sqlUserType(u *sqladmin.User) string {
	switch u.Type {
	case "", "BUILT_IN":
		return "BUILT_IN"
	default:
		return strings.TrimPrefix(u.Type, "CLOUD_IAM_")
	}
}

func sqlUserRoles(u *sqladmin.User) string {
	if len(u.DatabaseRoles) > 0 {
		return strings.Join(u.DatabaseRoles, ",")
	}
	if d := u.SqlserverUserDetails; d != nil && len(d.ServerRoles) > 0 {
		return strings.Join(d.ServerRoles, ",")
	}
	return "-"
}

// sqlUserStatus reports whether the account can actually be used.
//
// A disabled user that still appears in the list is exactly the row worth
// colouring: it looks like access that exists and is not.
func sqlUserStatus(u *sqladmin.User) string {
	if d := u.SqlserverUserDetails; d != nil && d.Disabled {
		return "DISABLED"
	}
	switch u.IamStatus {
	case "DISABLED":
		return "DISABLED"
	case "UNKNOWN":
		// IAM users only: the instance could not confirm the identity, which is
		// not the same as it being fine.
		return "UNKNOWN"
	}
	return "ACTIVE"
}

func dashIfEmpty(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
