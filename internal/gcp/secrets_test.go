package gcp

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
	"time"

	secretmanager "google.golang.org/api/secretmanager/v1"
)

func TestSecretResourceShape(t *testing.T) {
	r := secretResource(testProject(), testSecret())

	if r.Name != "prod-db-password" {
		t.Errorf("Name = %q, want the last path segment", r.Name)
	}
	if r.Location != "global" {
		t.Errorf("Location = %q, want global", r.Location)
	}
	if r.Status != "ACTIVE" {
		t.Errorf("Status = %q, want ACTIVE", r.Status)
	}
	if r.Row[1] != "automatic" {
		t.Errorf("replication cell = %q", r.Row[1])
	}
	if r.Row[2] != "12d" {
		t.Errorf("rotates-in cell = %q, want 12d", r.Row[2])
	}
}

// TestSecretListerNeverFetchesAValue is the point of the whole kind.
//
// The guarantee is structural — projects.secrets.list returns metadata and
// there is no call to AccessSecretVersion anywhere — so this asserts on the
// source rather than on behaviour: a future edit that reaches for a payload
// fails here, before it ever renders one.
func TestSecretListerNeverFetchesAValue(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "secrets.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}

	forbidden := []string{"AccessSecretVersion", "Access(", "SecretPayload", "Payload"}
	ast.Inspect(file, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		for _, bad := range forbidden {
			if strings.Contains(sel.Sel.Name, strings.TrimSuffix(bad, "(")) {
				t.Errorf("secrets.go reaches for a secret value via %q at %s",
					sel.Sel.Name, fset.Position(sel.Pos()))
			}
		}
		return true
	})
}

func TestSecretMetadataCarriesNoPayloadField(t *testing.T) {
	// The other half of the guarantee: what the list call hands back has no
	// value in it to leak, whatever the detail pane does with it.
	r := secretResource(testProject(), testSecret())
	s, ok := r.Raw.(*secretmanager.Secret)
	if !ok {
		t.Fatalf("Raw is %T, want the metadata object", r.Raw)
	}
	// Compile-time proof by construction: secretmanager.Secret has no payload
	// field at all, so naming one here would not build. This asserts the type
	// that is stored, which is what stops a future refactor swapping in a
	// SecretVersion or an AccessSecretVersionResponse.
	if s.Name == "" {
		t.Error("metadata object is empty")
	}
}

func TestSecretStateFlagsExpiry(t *testing.T) {
	tests := []struct {
		name   string
		expire string
		want   string
	}{
		{"no expiry is the ordinary case", "", "ACTIVE"},
		{"expiry ahead", time.Now().Add(48 * time.Hour).Format(time.RFC3339), "EXPIRING"},
		// An expired secret takes every version with it, which is worth seeing
		// before a workload finds out.
		{"expiry passed", time.Now().Add(-time.Hour).Format(time.RFC3339), "EXPIRED"},
		{"unparseable", "not-a-timestamp", "ACTIVE"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := secretState(&secretmanager.Secret{ExpireTime: tt.expire})
			if got != tt.want {
				t.Errorf("secretState = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSecretReplicationSummary(t *testing.T) {
	tests := []struct {
		name string
		repl *secretmanager.Replication
		want string
	}{
		{"none reported", nil, "-"},
		{"automatic", &secretmanager.Replication{Automatic: &secretmanager.Automatic{}}, "automatic"},
		{
			// Which regions is the whole point of having chosen them — that is
			// usually a data residency rule.
			"user managed names its regions",
			&secretmanager.Replication{UserManaged: &secretmanager.UserManaged{
				Replicas: []*secretmanager.Replica{
					{Location: "northamerica-northeast1"},
					{Location: "us-central1"},
				},
			}},
			"northamerica-northeast1,us-central1",
		},
		{
			"user managed with no replicas",
			&secretmanager.Replication{UserManaged: &secretmanager.UserManaged{}},
			"user-managed",
		},
		{"empty replication", &secretmanager.Replication{}, "-"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := secretReplication(&secretmanager.Secret{Replication: tt.repl}); got != tt.want {
				t.Errorf("secretReplication = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSecretRotatesIn(t *testing.T) {
	tests := []struct {
		name string
		rot  *secretmanager.Rotation
		want string
	}{
		{"no rotation configured", nil, "-"},
		{"no next time", &secretmanager.Rotation{RotationPeriod: "7776000s"}, "-"},
		{"scheduled", &secretmanager.Rotation{
			NextRotationTime: time.Now().Add(30*24*time.Hour + 6*time.Hour).Format(time.RFC3339),
		}, "30d"},
		// A schedule whose time has passed means the notification is not being
		// acted on, which is a different thing from having no schedule.
		{"overdue", &secretmanager.Rotation{
			NextRotationTime: time.Now().Add(-24 * time.Hour).Format(time.RFC3339),
		}, "overdue"},
		{"unparseable", &secretmanager.Rotation{NextRotationTime: "soon"}, "-"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := secretRotatesIn(&secretmanager.Secret{Rotation: tt.rot}); got != tt.want {
				t.Errorf("secretRotatesIn = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSecretIsNotAnSSHOrAirflowTarget(t *testing.T) {
	r := secretResource(testProject(), testSecret())
	if _, _, ok := SSHTarget(r); ok {
		t.Error("a secret is an ssh target")
	}
	if _, ok := AirflowURI(r); ok {
		t.Error("a secret has an Airflow URI")
	}
}
