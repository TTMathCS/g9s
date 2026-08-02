package gcp

import (
	"strings"
	"testing"

	cloudresourcemanager "google.golang.org/api/cloudresourcemanager/v1"
)

func TestProjectRolesKeepOnlyTheSelectedServiceAccount(t *testing.T) {
	member := "serviceAccount:etl-runner@sandbox-123.iam.gserviceaccount.com"
	policy := &cloudresourcemanager.Policy{Bindings: []*cloudresourcemanager.Binding{
		{Role: "roles/storage.objectViewer", Members: []string{member, "user:someone@example.com"}},
		{Role: "roles/viewer", Members: []string{"serviceAccount:another@sandbox-123.iam.gserviceaccount.com"}},
		{Role: "projects/sandbox-123/roles/pipelineRunner", Members: []string{strings.ToUpper(member)}},
	}}

	result := projectRoleResources(testProject(), member, policy)
	if len(result.Resources) != 2 {
		t.Fatalf("listed %d roles, want the selected account's 2 direct grants", len(result.Resources))
	}
	if result.Resources[0].Name != "projects/sandbox-123/roles/pipelineRunner" ||
		result.Resources[1].Name != "storage.objectViewer" {
		t.Errorf("roles sorted/rendered as %q and %q", result.Resources[0].Name, result.Resources[1].Name)
	}
	for _, r := range result.Resources {
		detail, ok := r.Raw.(*ServiceAccountRoleDetail)
		if !ok {
			t.Fatalf("Raw is %T, want one selected grant", r.Raw)
		}
		if detail.Member != member {
			t.Errorf("detail member = %q, want selected account", detail.Member)
		}
	}
}

func TestConditionalProjectRoleNamesItsCondition(t *testing.T) {
	binding := &cloudresourcemanager.Binding{
		Role: "roles/bigquery.jobUser",
		Condition: &cloudresourcemanager.Expr{
			Title:      "work hours",
			Expression: "request.time.getHours('America/Toronto') < 18",
		},
	}
	r := serviceAccountRoleResource(testProject(), "serviceAccount:etl@example.com", binding)

	if r.Row[1] != "work hours" {
		t.Errorf("condition cell = %q, want its title", r.Row[1])
	}
	if r.Status != "CONDITIONAL" || r.Row[3] != "CONDITIONAL" {
		t.Errorf("conditional grant rendered as status %q/%q", r.Status, r.Row[3])
	}
	if r.Row[2] != "PROJECT" || r.Location != testProject().ProjectID {
		t.Errorf("scope = %q/%q, want the project", r.Row[2], r.Location)
	}
}

func TestUnconditionalProjectRoleReadsAsGranted(t *testing.T) {
	r := serviceAccountRoleResource(testProject(), "serviceAccount:etl@example.com",
		&cloudresourcemanager.Binding{Role: "roles/viewer"})
	if r.Row[0] != "viewer" || r.Row[1] != "-" || r.Status != "GRANTED" {
		t.Errorf("unconditional role row = %#v, status %q", r.Row, r.Status)
	}
}

func TestProjectRoleDrillDownRejectsANonAccountParent(t *testing.T) {
	_, err := (ServiceAccountRoleLister{}).List(t.Context(), nil, testProject(),
		Resource{Name: "not-an-account", Raw: testInstance()}, nil)
	if err == nil {
		t.Error("a non-service-account parent was accepted")
	}
}

func TestBothServiceAccountListingsAreReachable(t *testing.T) {
	children := ChildrenOf("sa")
	if len(children) != 2 {
		t.Fatalf("ChildrenOf(sa) returned %d listings, want keys and project roles", len(children))
	}
	if children[0].Kind().ID != "sakeys" || children[1].Kind().ID != "saroles" {
		t.Errorf("service-account children = %q, %q", children[0].Kind().ID, children[1].Kind().ID)
	}
}
