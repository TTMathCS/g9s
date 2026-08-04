package gcp

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"google.golang.org/api/option"
	"google.golang.org/api/storage/v1"

	"github.com/TTMathCS/g9s/internal/config"
	"github.com/TTMathCS/g9s/internal/tfstate"
)

// terraformTypes maps a g9s kind to the Terraform resource types that manage
// it.
//
// Incomplete on purpose, and the incompleteness is the safety property. A kind
// that is not in here reports "not checked" rather than "unmanaged" — an
// overlay that told somebody their entire Cloud SQL estate was untracked
// because g9s did not know the type name would be worse than no overlay at
// all, and the person acting on it would be deleting things.
//
// Only kinds whose Terraform name attribute is the same string g9s puts in the
// NAME column are listed. Where the provider stores a path, an id or a
// generated name, the match would be silently wrong, so the kind stays out.
var terraformTypes = map[string][]string{
	"vm":             {"google_compute_instance", "google_compute_instance_from_template"},
	"disks":          {"google_compute_disk", "google_compute_region_disk"},
	"snapshots":      {"google_compute_snapshot"},
	"migs":           {"google_compute_instance_group_manager", "google_compute_region_instance_group_manager"},
	"templates":      {"google_compute_instance_template", "google_compute_region_instance_template"},
	"reservations":   {"google_compute_reservation"},
	"tpus":           {"google_tpu_v2_vm", "google_tpu_node"},
	"functions":      {"google_cloudfunctions_function", "google_cloudfunctions2_function"},
	"run":            {"google_cloud_run_service", "google_cloud_run_v2_service"},
	"runjobs":        {"google_cloud_run_v2_job"},
	"gke":            {"google_container_cluster"},
	"nodepools":      {"google_container_node_pool"},
	"gcs":            {"google_storage_bucket"},
	"bq":             {"google_bigquery_dataset"},
	"bqreservations": {"google_bigquery_reservation"},
	"dataproc":       {"google_dataproc_cluster"},
	"composer":       {"google_composer_environment"},
	"bigtable":       {"google_bigtable_instance"},
	"spanner":        {"google_spanner_instance"},
	"redis":          {"google_redis_instance"},
	"memcached":      {"google_memcache_instance"},
	"datastream":     {"google_datastream_stream"},
	"datafusion":     {"google_data_fusion_instance"},
	"artifacts":      {"google_artifact_registry_repository"},
	"sql":            {"google_sql_database_instance"},
	"sqldbs":         {"google_sql_database"},
	"sqlusers":       {"google_sql_user"},
	"topics":         {"google_pubsub_topic"},
	"subs":           {"google_pubsub_subscription"},
	"vpc":            {"google_compute_network"},
	"subnets":        {"google_compute_subnetwork"},
	"fw":             {"google_compute_firewall"},
	"routes":         {"google_compute_route"},
	"routers":        {"google_compute_router"},
	"routernats":     {"google_compute_router_nat"},
	"lb":             {"google_compute_forwarding_rule", "google_compute_global_forwarding_rule"},
	"dns":            {"google_dns_managed_zone"},
	"vpn":            {"google_compute_vpn_tunnel"},
	"interconnect":   {"google_compute_interconnect_attachment"},
	"psc":            {"google_compute_service_attachment"},
	"addresses":      {"google_compute_address", "google_compute_global_address"},
	"secrets":        {"google_secret_manager_secret"},
	"sa":             {"google_service_account"},
	"kms":            {"google_kms_crypto_key"},
	"certs":          {"google_certificate_manager_certificate"},
	"scheduler":      {"google_cloud_scheduler_job"},
	"tasks":          {"google_cloud_tasks_queue"},
	"alerts":         {"google_monitoring_alert_policy"},
	"firestore":      {"google_firestore_database"},
}

// TerraformTypesFor returns the Terraform types that back a kind, and whether
// the kind is mapped at all.
func TerraformTypesFor(kindID string) ([]string, bool) {
	types, ok := terraformTypes[kindID]
	return types, ok
}

// TerraformKinds lists the kind ids the overlay understands, sorted. Used by
// the docs test and to tell the user which tables the overlay works on.
func TerraformKinds() []string {
	out := make([]string, 0, len(terraformTypes))
	for id := range terraformTypes {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// ManagedState is what the overlay says about one row.
type ManagedState int

const (
	// ManagedUnknown is a kind the overlay has no Terraform type for. Never
	// rendered as unmanaged: an overlay that reports "not in Terraform"
	// because g9s did not recognise the type sends someone to delete
	// something that is managed.
	ManagedUnknown ManagedState = iota
	// ManagedNo is a kind the overlay understands, in which this name does not
	// appear in the state.
	ManagedNo
	// ManagedYes is a name the state manages.
	ManagedYes
)

func (s ManagedState) String() string {
	switch s {
	case ManagedYes:
		return "MANAGED"
	case ManagedNo:
		return "UNMANAGED"
	default:
		return "?"
	}
}

// ManagedBy reports what the state says about one resource of one kind.
func ManagedBy(idx *tfstate.Index, kindID, name string) ManagedState {
	types, mapped := TerraformTypesFor(kindID)
	if !mapped {
		return ManagedUnknown
	}
	if idx.HasAny(types, name) {
		return ManagedYes
	}
	return ManagedNo
}

// stateObjectSuffix is what a state object is called. Terraform's GCS backend
// writes `<prefix>/default.tfstate` and `<prefix>/<workspace>.tfstate`.
const stateObjectSuffix = ".tfstate"

// TerraformState reads the Terraform state for a project from its GCS backend.
//
// Returns the index, the object names that were read, and a warning for
// anything that was skipped. A state file that could not be read is a warning
// rather than an error for the same reason a denied region is: the answer from
// the files that did load is still worth having, as long as nobody is told it
// is the whole picture.
func TerraformState(ctx context.Context, cfg *config.Config, p config.Project, opts []option.ClientOption) (*tfstate.Index, []string, []Warning, error) {
	bucket, prefix := cfg.TerraformBackend(p)
	if bucket == "" {
		return nil, nil, nil, fmt.Errorf("no terraform state bucket configured for %s — set terraform.state_bucket", p.Name)
	}

	svc, err := storage.NewService(ctx, opts...)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("storage client: %w", err)
	}

	call := svc.Objects.List(bucket).Context(ctx)
	if prefix != "" {
		call = call.Prefix(prefix)
	}

	// A prefix somebody points at the root of a bucket can match hundreds of
	// state files, and each one is a download. Bounded, and the bound is a
	// setting so an estate that genuinely has more can raise it.
	maxObjects := cfg.LimitTerraformStateObjects()

	var (
		names    []string
		warnings []Warning
		capped   bool
	)
	err = call.Pages(ctx, func(page *storage.Objects) error {
		for _, obj := range page.Items {
			if obj == nil || !strings.HasSuffix(obj.Name, stateObjectSuffix) {
				continue
			}
			if len(names) >= maxObjects {
				capped = true
				return nil
			}
			names = append(names, obj.Name)
		}
		return nil
	})
	if err != nil {
		return nil, nil, nil, fmt.Errorf("listing gs://%s: %w", bucket, err)
	}
	if capped {
		warnings = append(warnings, cappedWarning(
			"stopped after %d state objects under gs://%s — narrow terraform.state_prefix or raise limits.terraform_state_objects",
			maxObjects, bucket))
	}
	if len(names) == 0 {
		return nil, nil, warnings, fmt.Errorf("no %s objects under gs://%s/%s", stateObjectSuffix, bucket, prefix)
	}

	index := tfstate.New()
	var read []string
	for _, name := range names {
		resp, err := svc.Objects.Get(bucket, name).Context(ctx).Download()
		if err != nil {
			if w, ok := describeFailure(fmt.Sprintf("gs://%s/%s", bucket, name), err); ok {
				warnings = append(warnings, w)
			}
			continue
		}
		parsed, err := tfstate.Parse(resp.Body)
		resp.Body.Close()
		if err != nil {
			warnings = append(warnings, Warning{
				Scope:  fmt.Sprintf("gs://%s/%s", bucket, name),
				Reason: ReasonPartial,
				Detail: err.Error(),
			})
			continue
		}
		index.Merge(parsed)
		read = append(read, name)
	}

	if len(read) == 0 {
		return nil, nil, warnings, fmt.Errorf("no readable state under gs://%s/%s", bucket, prefix)
	}
	return index, read, warnings, nil
}
