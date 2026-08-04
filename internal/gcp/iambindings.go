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

// IAMBindingLister lists the project's IAM policy as one row per member.
//
// "Who can do what here" is a top-three support question and the hardest one
// to answer from the console, where the policy is presented role-first: fifty
// roles, each with a list of members, and the question is almost always the
// other way round. One row per member per role is more rows, and they are the
// rows people actually scan.
//
// Three findings drive the status column, and each is a thing that is fine
// individually and alarming in a list:
//
//   - **allUsers / allAuthenticatedUsers.** A binding to either makes that role
//     public. It is occasionally deliberate and usually not.
//   - **A primitive role.** owner, editor and viewer predate IAM's granular
//     roles and grant vastly more than anyone means to. editor in particular
//     can modify almost everything in the project.
//   - **A conditional binding.** A real grant, but only while its expression
//     holds — which means the row is telling less than the whole truth unless
//     the condition is visible.
//
// This is the project policy only. Roles inherited from a folder or the
// organization are not here and cannot be, since reading them needs access to
// resources above the project — so the listing says so rather than implying
// this is everything.
type IAMBindingLister struct{}

func (IAMBindingLister) Kind() Kind {
	return Kind{
		ID:    "iam",
		Title: "IAM Bindings",
		Columns: []Column{
			{Title: "MEMBER", Width: 6},
			{Title: "TYPE", Width: 2},
			{Title: "ROLE", Width: 5},
			{Title: "CONDITION", Width: 3},
			{Title: "SCOPE", Width: 2},
		},
	}
}

func (IAMBindingLister) List(ctx context.Context, cfg *config.Config, p config.Project, opts []option.ClientOption) (Result, error) {
	svc, err := cloudresourcemanager.NewService(ctx, opts...)
	if err != nil {
		return Result{}, fmt.Errorf("resource manager client: %w", err)
	}

	// Version 3 so conditional bindings come back at all. A version-1 read of a
	// policy that has conditions silently omits them, which would make this
	// table claim unconditional access that is not.
	policy, err := svc.Projects.GetIamPolicy(p.ProjectID,
		&cloudresourcemanager.GetIamPolicyRequest{
			Options: &cloudresourcemanager.GetPolicyOptions{RequestedPolicyVersion: 3},
		}).Context(ctx).Do()
	if err != nil {
		return Result{}, err
	}

	var result Result
	for _, binding := range policy.Bindings {
		if binding == nil {
			continue
		}
		for _, member := range binding.Members {
			result.Resources = append(result.Resources, iamBindingResource(p, member, binding))
		}
	}

	// Inheritance is invisible from here, and a table that looks complete while
	// missing the folder-level owner of the project is worse than one that says
	// what it does not know.
	result.Warnings = append(result.Warnings, narrowedWarning(
		"project-level bindings only — roles inherited from a folder or the organisation are not shown"))

	sort.SliceStable(result.Resources, func(i, j int) bool {
		return result.Resources[i].Name < result.Resources[j].Name
	})
	return result, nil
}

func iamBindingResource(p config.Project, member string, binding *cloudresourcemanager.Binding) Resource {
	kind, name := splitIAMMember(member)

	condition := "-"
	if binding.Condition != nil {
		condition = binding.Condition.Title
		if condition == "" {
			condition = clip(binding.Condition.Expression, 60)
		}
	}

	return Resource{
		Name:     name,
		Location: "project",
		Status:   iamBindingStatus(member, binding),
		Row: []string{
			name,
			kind,
			shortRole(binding.Role),
			condition,
			"project",
		},
		Raw: binding,
		ConsoleURL: fmt.Sprintf("https://console.cloud.google.com/iam-admin/iam?project=%s",
			url.QueryEscape(p.ProjectID)),
	}
}

// splitIAMMember separates the member type from the identity.
//
// The prefix is what distinguishes a person from a service account from the
// entire internet, and it is the first thing anyone scanning this table needs.
func splitIAMMember(member string) (kind, name string) {
	prefix, rest, found := strings.Cut(member, ":")
	if !found {
		// allUsers and allAuthenticatedUsers carry no colon, and they are the
		// two members most worth not mangling.
		return "public", member
	}
	switch prefix {
	case "user":
		return "user", rest
	case "serviceAccount":
		return "svc acct", rest
	case "group":
		return "group", rest
	case "domain":
		return "domain", rest
	case "deleted":
		// A binding left behind by a deleted principal. It grants nothing now
		// and will grant again if the name is ever reused.
		return "deleted", clip(rest, 60)
	default:
		return prefix, rest
	}
}

// primitiveRoles are the pre-IAM roles that grant far more than their names
// suggest. editor can modify nearly everything in the project.
var primitiveRoles = map[string]bool{
	"roles/owner":  true,
	"roles/editor": true,
	"roles/viewer": true,
}

// iamBindingStatus ranks the ways a binding is worth a second look.
//
// Public first: a role granted to allUsers is reachable by anyone on the
// internet, which outranks anything else on the row. Then primitive roles,
// then conditions — a conditional binding is not a problem, it is a row whose
// plain reading is incomplete.
func iamBindingStatus(member string, binding *cloudresourcemanager.Binding) string {
	switch member {
	case "allUsers", "allAuthenticatedUsers":
		return "PUBLIC"
	}
	if strings.HasPrefix(member, "deleted:") {
		return "DELETED_PRINCIPAL"
	}
	if primitiveRoles[binding.Role] {
		return "PRIMITIVE_ROLE"
	}
	if binding.Condition != nil {
		return "CONDITIONAL"
	}
	return "ACTIVE"
}

// shortRole drops the predefined-role prefix, which is the same on almost
// every row and costs width that the role name itself needs.
func shortRole(role string) string {
	if trimmed := strings.TrimPrefix(role, "roles/"); trimmed != role {
		return trimmed
	}
	// A custom role keeps its full path, because "which project defined this"
	// is part of what the row is saying.
	return clip(role, 60)
}
