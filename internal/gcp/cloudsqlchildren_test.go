package gcp

import (
	"testing"

	sqladmin "google.golang.org/api/sqladmin/v1"
)

func TestSQLDatabaseResourceShape(t *testing.T) {
	r := sqlDatabaseResource(testProject(), testSQLInstance(), testSQLDatabase())

	if r.Name != "orders" {
		t.Errorf("Name = %q", r.Name)
	}
	// The instance is the location here: it is what the listing is scoped to,
	// and it is what the drill trail names.
	if r.Location != "orders-primary" {
		t.Errorf("Location = %q, want the instance", r.Location)
	}
	if r.Row[1] != "UTF8" || r.Row[2] != "en_US.UTF8" {
		t.Errorf("charset/collation = %q/%q", r.Row[1], r.Row[2])
	}
}

func TestSQLDatabaseWithoutCharsetReported(t *testing.T) {
	r := sqlDatabaseResource(testProject(), testSQLInstance(), &sqladmin.Database{Name: "bare"})
	if r.Row[1] != "-" || r.Row[2] != "-" {
		t.Errorf("empty fields rendered as %q/%q, want dashes", r.Row[1], r.Row[2])
	}
}

func TestSQLUserResourceShape(t *testing.T) {
	r := sqlUserResource(testProject(), testSQLInstance(), testSQLUser())

	if r.Name != "app-writer" {
		t.Errorf("Name = %q", r.Name)
	}
	if r.Row[1] != "%" {
		t.Errorf("host cell = %q, want the wildcard shown as itself", r.Row[1])
	}
	if r.Row[2] != "BUILT_IN" {
		t.Errorf("type cell = %q", r.Row[2])
	}
	if r.Status != "ACTIVE" {
		t.Errorf("Status = %q", r.Status)
	}
}

func TestSQLUserTypeDistinguishesIAMFromPassword(t *testing.T) {
	// The difference between a credential you rotate and an identity you
	// revoke, which is the field most worth a column on this table.
	tests := []struct {
		in   string
		want string
	}{
		{"", "BUILT_IN"},
		{"BUILT_IN", "BUILT_IN"},
		{"CLOUD_IAM_USER", "USER"},
		{"CLOUD_IAM_SERVICE_ACCOUNT", "SERVICE_ACCOUNT"},
		{"CLOUD_IAM_GROUP", "GROUP"},
	}
	for _, tt := range tests {
		if got := sqlUserType(&sqladmin.User{Type: tt.in}); got != tt.want {
			t.Errorf("sqlUserType(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestSQLUserWithoutAHost(t *testing.T) {
	// Postgres and SQL Server do not scope users to a host and leave it blank.
	r := sqlUserResource(testProject(), testSQLInstance(), &sqladmin.User{Name: "postgres"})
	if r.Row[1] != "-" {
		t.Errorf("host cell = %q, want a dash", r.Row[1])
	}
}

func TestDisabledSQLUserIsFlagged(t *testing.T) {
	// A disabled user still appears in the list, which is exactly the row worth
	// colouring: it looks like access that exists and is not.
	iam := sqlUserResource(testProject(), testSQLInstance(),
		&sqladmin.User{Name: "leaver", Type: "CLOUD_IAM_USER", IamStatus: "DISABLED"})
	if iam.Status != "DISABLED" {
		t.Errorf("Status = %q, want DISABLED", iam.Status)
	}

	mssql := sqlUserResource(testProject(), testSQLInstance(), &sqladmin.User{
		Name:                 "svc",
		SqlserverUserDetails: &sqladmin.SqlServerUserDetails{Disabled: true},
	})
	if mssql.Status != "DISABLED" {
		t.Errorf("SQL Server Status = %q, want DISABLED", mssql.Status)
	}
}

func TestUnknownIAMStatusIsNotTreatedAsFine(t *testing.T) {
	r := sqlUserResource(testProject(), testSQLInstance(),
		&sqladmin.User{Name: "who", Type: "CLOUD_IAM_USER", IamStatus: "UNKNOWN"})
	if r.Status != "UNKNOWN" {
		t.Errorf("Status = %q, want UNKNOWN rather than ACTIVE", r.Status)
	}
}

func TestSQLUserRolesFallBackToServerRoles(t *testing.T) {
	pg := sqlUserResource(testProject(), testSQLInstance(),
		&sqladmin.User{Name: "app", DatabaseRoles: []string{"cloudsqlsuperuser"}})
	if pg.Row[3] != "cloudsqlsuperuser" {
		t.Errorf("roles cell = %q", pg.Row[3])
	}

	mssql := sqlUserResource(testProject(), testSQLInstance(), &sqladmin.User{
		Name:                 "sa",
		SqlserverUserDetails: &sqladmin.SqlServerUserDetails{ServerRoles: []string{"sysadmin", "public"}},
	})
	if mssql.Row[3] != "sysadmin,public" {
		t.Errorf("SQL Server roles cell = %q", mssql.Row[3])
	}

	none := sqlUserResource(testProject(), testSQLInstance(), &sqladmin.User{Name: "plain"})
	if none.Row[3] != "-" {
		t.Errorf("no roles rendered as %q, want a dash", none.Row[3])
	}
}

func TestSQLDrillDownsRejectAParentThatIsNotAnInstance(t *testing.T) {
	bad := Resource{Name: "not-an-instance", Raw: testInstance()}
	if _, err := (SQLDatabaseLister{}).List(t.Context(), nil, testProject(), bad, nil); err == nil {
		t.Error("the databases drill-down accepted a non-instance parent")
	}
	if _, err := (SQLUserLister{}).List(t.Context(), nil, testProject(), bad, nil); err == nil {
		t.Error("the users drill-down accepted a non-instance parent")
	}
}

func TestCloudSQLInstanceOffersBothListings(t *testing.T) {
	// The reason a parent is allowed more than one child: databases and users
	// are both things an instance contains, and neither is under the other.
	children := ChildrenOf("sql")
	if len(children) != 2 {
		t.Fatalf("ChildrenOf(sql) returned %d listings, want 2", len(children))
	}
	if children[0].Kind().Title != "Databases" || children[1].Kind().Title != "Users" {
		t.Errorf("got %q and %q", children[0].Kind().Title, children[1].Kind().Title)
	}
	// ChildOf takes the first, which is what enter opens.
	first, ok := ChildOf("sql")
	if !ok || first.Kind().ID != "sqldbs" {
		t.Errorf("ChildOf(sql) = %v, want the databases listing", first)
	}
}

func TestSQLUserPasswordNeverReachesARow(t *testing.T) {
	// users.list returns the field empty, but Raw is rendered in full by the
	// detail pane, so nothing may copy it into a cell either.
	u := testSQLUser()
	u.Password = "hunter2"

	r := sqlUserResource(testProject(), testSQLInstance(), u)
	for i, cell := range r.Row {
		if cell == "hunter2" {
			t.Errorf("cell %d carries the password", i)
		}
	}
}
