package gcp

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strings"

	cloudresourcemanager "google.golang.org/api/cloudresourcemanager/v1"
	"google.golang.org/api/option"

	"github.com/TTMathCS/g9s/internal/config"
)

// ServiceAccountRoleLister lists the project-level IAM roles granted directly
// to one service account.
//
// IAM policy is project-shaped rather than account-shaped: there is no API
// call that asks which roles one principal has. Opening this drill-down reads
// the project's policy once and filters its bindings locally. Folder and
// organisation inheritance are deliberately not guessed at here; the table's
// scope column says PROJECT so a direct grant cannot be mistaken for the
// account's complete effective access.
type ServiceAccountRoleLister struct{}

func (ServiceAccountRoleLister) ParentKind() string { return "sa" }

func (ServiceAccountRoleLister) Kind() Kind {
	return Kind{
		ID:    "saroles",
		Title: "Project Roles",
		Columns: []Column{
			{Title: "ROLE", Width: 5},
			{Title: "CONDITION", Width: 5},
			{Title: "SCOPE", Width: 2},
			{Title: "STATUS", Width: 2},
		},
	}
}

func (ServiceAccountRoleLister) List(ctx context.Context, _ *config.Config, p config.Project, parent Resource, opts []option.ClientOption) (Result, error) {
	detail, ok := parent.Raw.(*ServiceAccountDetail)
	if !ok || detail.Account == nil || detail.Account.Email == "" {
		return Result{}, fmt.Errorf("no service account data for %s", parent.Name)
	}

	svc, err := cloudresourcemanager.NewService(ctx, opts...)
	if err != nil {
		return Result{}, fmt.Errorf("resource manager client: %w", err)
	}
	policy, err := svc.Projects.GetIamPolicy(p.ProjectID, &cloudresourcemanager.GetIamPolicyRequest{
		Options: &cloudresourcemanager.GetPolicyOptions{RequestedPolicyVersion: 3},
	}).Context(ctx).Do()
	if err != nil {
		return Result{}, err
	}

	member := "serviceAccount:" + detail.Account.Email
	result := projectRoleResources(p, member, policy)
	if len(result.Resources) == 0 {
		result.Warnings = []string{
			"no direct project roles — access inherited from folders or the organisation is outside this listing",
		}
	}
	return result, nil
}

// ServiceAccountRoleDetail is the exact grant rendered in the describe pane.
// Keeping only the selected member avoids exposing every unrelated principal
// from the project's policy when one service account row is described.
type ServiceAccountRoleDetail struct {
	Role      string                     `json:"role"`
	Member    string                     `json:"member"`
	Condition *cloudresourcemanager.Expr `json:"condition,omitempty"`
}

func projectRoleResources(p config.Project, member string, policy *cloudresourcemanager.Policy) Result {
	var result Result
	if policy == nil {
		return result
	}

	for _, binding := range policy.Bindings {
		if binding == nil || !containsIAMMember(binding.Members, member) {
			continue
		}
		result.Resources = append(result.Resources, serviceAccountRoleResource(p, member, binding))
	}

	sort.SliceStable(result.Resources, func(i, j int) bool {
		if result.Resources[i].Name != result.Resources[j].Name {
			return result.Resources[i].Name < result.Resources[j].Name
		}
		return result.Resources[i].Row[1] < result.Resources[j].Row[1]
	})
	return result
}

func containsIAMMember(members []string, want string) bool {
	for _, member := range members {
		// Email addresses are case-insensitive. The principal prefix is fixed by
		// IAM, but folding the whole value also tolerates hand-authored policies.
		if strings.EqualFold(member, want) {
			return true
		}
	}
	return false
}

func serviceAccountRoleResource(p config.Project, member string, binding *cloudresourcemanager.Binding) Resource {
	condition := "-"
	status := "GRANTED"
	if binding.Condition != nil {
		status = "CONDITIONAL"
		condition = binding.Condition.Title
		if condition == "" {
			condition = clip(binding.Condition.Expression, 80)
		}
		if condition == "" {
			condition = "configured"
		}
	}

	role := roleName(binding.Role)
	return Resource{
		Name:     role,
		Location: p.ProjectID,
		Status:   status,
		Row: []string{
			role,
			condition,
			"PROJECT",
			status,
		},
		Raw: &ServiceAccountRoleDetail{
			Role:      binding.Role,
			Member:    member,
			Condition: binding.Condition,
		},
		ConsoleURL: "https://console.cloud.google.com/iam-admin/iam?project=" + url.QueryEscape(p.ProjectID),
	}
}

func roleName(role string) string {
	if strings.HasPrefix(role, "roles/") {
		return strings.TrimPrefix(role, "roles/")
	}
	return role
}
