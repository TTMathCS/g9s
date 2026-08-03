package gcp

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"google.golang.org/api/option"
	redis "google.golang.org/api/redis/v1"

	"github.com/TTMathCS/g9s/internal/config"
)

// RedisLister lists Memorystore for Redis instances.
//
// Regional service, global call: the API documents `locations/-` explicitly —
// "if location_id is specified as `-` (wildcard), then all regions available to
// the project are queried, and the results are aggregated" — and names the ones
// that did not answer in Unreachable. Same shape as Cloud Functions, and the
// opposite of Cloud Run, which is why each lister says which answer it got.
//
// The two columns worth the row are TIER and REPLICAS. A BASIC instance has no
// replica and no failover: it is a cache that loses everything on a restart,
// which is fine when that is what someone chose and is a surprise when the
// thing behind it is treated as a database.
type RedisLister struct{}

func (RedisLister) Kind() Kind {
	return Kind{
		ID:    "redis",
		Title: "Memorystore Redis",
		Columns: []Column{
			{Title: "NAME", Width: 4},
			{Title: "REGION", Width: 2},
			{Title: "TIER", Width: 2},
			{Title: "SIZE", Width: 1},
			{Title: "VERSION", Width: 2},
			{Title: "AUTH", Width: 2},
			{Title: "STATE", Width: 2},
		},
	}
}

func (RedisLister) List(ctx context.Context, _ *config.Config, p config.Project, opts []option.ClientOption) (Result, error) {
	svc, err := redis.NewService(ctx, opts...)
	if err != nil {
		return Result{}, fmt.Errorf("memorystore client: %w", err)
	}

	var (
		result      Result
		unreachable = map[string]bool{}
	)
	err = svc.Projects.Locations.Instances.List("projects/"+p.ProjectID+"/locations/-").
		Pages(ctx, func(page *redis.ListInstancesResponse) error {
			for _, loc := range page.Unreachable {
				unreachable[loc] = true
			}
			for _, inst := range page.Instances {
				if inst != nil {
					result.Resources = append(result.Resources, redisResource(p, inst))
				}
			}
			return nil
		})
	if err != nil {
		return result, err
	}

	for _, loc := range sortedKeys(unreachable) {
		if w, ok := describeFailure(loc, fmt.Errorf("location unreachable")); ok {
			result.Warnings = append(result.Warnings, w)
		}
	}

	sortResources(result.Resources)
	return result, nil
}

func redisResource(p config.Project, i *redis.Instance) Resource {
	name := lastSegment(i.Name)
	region := instanceRegion(i.Name)

	return Resource{
		Name:     name,
		Location: region,
		Status:   redisStatus(i),
		Row: []string{
			name,
			region,
			redisTier(i),
			fmt.Sprintf("%dGB", i.MemorySizeGb),
			redisVersion(i),
			redisAuth(i),
			redisState(i),
		},
		Raw: i,
		ConsoleURL: fmt.Sprintf(
			"https://console.cloud.google.com/memorystore/redis/locations/%s/instances/%s/details?project=%s",
			url.PathEscape(region), url.PathEscape(name), url.QueryEscape(p.ProjectID)),
	}
}

// instanceRegion pulls the region out of a `projects/*/locations/*/…` name,
// which is where it lives when the listing was made with a wildcard.
func instanceRegion(name string) string {
	parts := strings.Split(name, "/")
	for i, part := range parts {
		if part == "locations" && i+1 < len(parts) {
			return parts[i+1]
		}
	}
	return "-"
}

func redisState(i *redis.Instance) string {
	if i.State == "" || i.State == "STATE_UNSPECIFIED" {
		return "UNKNOWN"
	}
	return i.State
}

// redisStatus leads with the durability finding rather than the lifecycle one.
//
// A READY basic-tier instance is working exactly as designed and has no replica
// and no failover, so an outage of its single node loses everything in it. That
// is a property of the tier, not a fault, and it is invisible on a row that
// reports READY like every other healthy instance.
func redisStatus(i *redis.Instance) string {
	state := redisState(i)
	if state != "READY" {
		return state
	}
	if strings.EqualFold(i.Tier, "BASIC") {
		return "NO_REPLICA"
	}
	if !i.AuthEnabled {
		return "NO_AUTH"
	}
	return state
}

func redisTier(i *redis.Instance) string {
	switch strings.ToUpper(i.Tier) {
	case "":
		return "-"
	case "BASIC":
		return "basic"
	case "STANDARD_HA":
		return "standard HA"
	default:
		return strings.ToLower(i.Tier)
	}
}

func redisVersion(i *redis.Instance) string {
	if i.RedisVersion == "" {
		return "-"
	}
	// The API returns REDIS_7_0; the underscores are noise in a narrow column.
	return strings.ToLower(strings.TrimPrefix(i.RedisVersion, "REDIS_"))
}

// redisAuth reports whether AUTH is required.
//
// Memorystore is reachable only from inside the VPC, which is the reason people
// leave AUTH off — and the reason an instance with it off trusts every workload
// on that network equally.
func redisAuth(i *redis.Instance) string {
	if i.AuthEnabled {
		return "on"
	}
	return "off"
}
