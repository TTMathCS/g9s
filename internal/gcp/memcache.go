package gcp

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	memcache "google.golang.org/api/memcache/v1"
	"google.golang.org/api/option"

	"github.com/TTMathCS/g9s/internal/config"
)

// MemcacheLister lists Memorystore for Memcached instances.
//
// Regional service, wildcard call: `locations/-` covers every region and the
// response names what it could not reach, the same shape as the Redis kind next
// to it. If the wildcard were ever refused the call errors and the error
// surfaces, which is the honest failure — a listing that is silently short is
// the one worth avoiding.
//
// Memcached has no persistence and no replication, by design: it is a cache,
// every node is independent, and a restart loses whatever was in it. So there
// is no durability finding to report here the way there is for Redis — the
// columns that matter are the size being paid for and whether the nodes are
// actually up, because a partially-degraded instance still reports READY.
type MemcacheLister struct{}

func (MemcacheLister) Kind() Kind {
	return Kind{
		ID:    "memcached",
		Title: "Memorystore Memcached",
		Columns: []Column{
			{Title: "NAME", Width: 4},
			{Title: "REGION", Width: 2},
			{Title: "NODES", Width: 2},
			{Title: "NODE SIZE", Width: 2},
			{Title: "TOTAL", Width: 2},
			{Title: "VERSION", Width: 2},
			{Title: "STATE", Width: 2},
		},
	}
}

func (MemcacheLister) List(ctx context.Context, _ *config.Config, p config.Project, opts []option.ClientOption) (Result, error) {
	svc, err := memcache.NewService(ctx, opts...)
	if err != nil {
		return Result{}, fmt.Errorf("memcached client: %w", err)
	}

	var (
		result      Result
		unreachable = map[string]bool{}
	)
	err = svc.Projects.Locations.Instances.List("projects/"+p.ProjectID+"/locations/-").
		Pages(ctx, func(page *memcache.ListInstancesResponse) error {
			for _, loc := range page.Unreachable {
				unreachable[loc] = true
			}
			for _, inst := range page.Instances {
				if inst != nil {
					result.Resources = append(result.Resources, memcacheResource(p, inst))
				}
			}
			return nil
		})
	if err != nil {
		return result, err
	}

	for _, loc := range sortedKeys(unreachable) {
		if w, ok := describeFailure(lastSegment(loc), fmt.Errorf("location unreachable")); ok {
			result.Warnings = append(result.Warnings, w)
		}
	}

	sortResources(result.Resources)
	return result, nil
}

func memcacheResource(p config.Project, i *memcache.Instance) Resource {
	name := lastSegment(i.Name)
	region := instanceRegion(i.Name)

	return Resource{
		Name:     name,
		Location: region,
		Status:   memcacheStatus(i),
		Row: []string{
			name,
			region,
			memcacheNodes(i),
			memcacheNodeSize(i),
			memcacheTotalMemory(i),
			memcacheVersion(i),
			memcacheState(i),
		},
		Raw: i,
		ConsoleURL: fmt.Sprintf(
			"https://console.cloud.google.com/memorystore/memcached/locations/%s/instances/%s/details?project=%s",
			url.PathEscape(region), url.PathEscape(name), url.QueryEscape(p.ProjectID)),
	}
}

// memcacheStatus reports a partially-degraded instance rather than the READY it
// claims.
//
// The instance state is READY as long as it exists; individual nodes have their
// own state, and one that is not READY is serving nothing while the instance
// row says everything is fine. That is the failure this column is for.
func memcacheStatus(i *memcache.Instance) string {
	state := memcacheState(i)
	if state != "READY" {
		return state
	}
	if down := nodesNotReady(i); down > 0 {
		return "NODES_DOWN"
	}
	return state
}

// nodesNotReady counts the nodes that are not serving.
func nodesNotReady(i *memcache.Instance) int {
	down := 0
	for _, node := range i.MemcacheNodes {
		if node != nil && node.State != "" && node.State != "READY" {
			down++
		}
	}
	return down
}

func memcacheState(i *memcache.Instance) string {
	if i.State == "" || i.State == "STATE_UNSPECIFIED" {
		return "UNKNOWN"
	}
	return i.State
}

// memcacheNodes says how many nodes there are, and how many are not serving —
// the second number is the one the instance state hides.
func memcacheNodes(i *memcache.Instance) string {
	if i.NodeCount <= 0 {
		return "-"
	}
	if down := nodesNotReady(i); down > 0 {
		return fmt.Sprintf("%d (%d down)", i.NodeCount, down)
	}
	return fmt.Sprintf("%d", i.NodeCount)
}

func memcacheNodeSize(i *memcache.Instance) string {
	if i.NodeConfig == nil || i.NodeConfig.MemorySizeMb <= 0 {
		return "-"
	}
	return fmt.Sprintf("%dMB/%dvCPU", i.NodeConfig.MemorySizeMb, i.NodeConfig.CpuCount)
}

// memcacheTotalMemory is nodes x per-node memory, which is what the instance
// actually costs and is on neither field by itself.
func memcacheTotalMemory(i *memcache.Instance) string {
	if i.NodeConfig == nil || i.NodeConfig.MemorySizeMb <= 0 || i.NodeCount <= 0 {
		return "-"
	}
	return humanBytes(i.NodeCount * i.NodeConfig.MemorySizeMb * 1024 * 1024)
}

func memcacheVersion(i *memcache.Instance) string {
	if i.MemcacheVersion == "" {
		return "-"
	}
	return strings.ToLower(strings.TrimPrefix(i.MemcacheVersion, "MEMCACHE_"))
}
