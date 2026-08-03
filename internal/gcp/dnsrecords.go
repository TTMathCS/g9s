package gcp

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	dns "google.golang.org/api/dns/v1"
	"google.golang.org/api/option"

	"github.com/TTMathCS/g9s/internal/config"
)

// maxRecordSets caps one zone's listing. A zone serving a large estate can hold
// tens of thousands of records, which is a scroll buffer rather than a table.

// DNSRecordLister is the record sets inside one managed zone.
//
// The first drill-down that actually fetches: a zone listing carries the zone's
// name servers and visibility but nothing about its contents, so this is one
// paginated call made when you open it. That is the argument for drilling
// rather than listing — records are per zone, nobody wants every record in the
// project flattened into one table, and paying for them on every refresh of the
// zones table would be paying for something almost never looked at.
type DNSRecordLister struct{}

func (DNSRecordLister) ParentKind() string { return "dns" }

func (DNSRecordLister) Kind() Kind {
	return Kind{
		ID:    "records",
		Title: "Record Sets",
		Columns: []Column{
			{Title: "NAME", Width: 5},
			{Title: "TYPE", Width: 1},
			{Title: "TTL", Width: 1},
			{Title: "ROUTING", Width: 2},
			{Title: "DATA", Width: 6},
		},
	}
}

func (DNSRecordLister) List(ctx context.Context, cfg *config.Config, p config.Project, parent Resource, opts []option.ClientOption) (Result, error) {
	maxRecordSets := cfg.LimitDNSRecordSets()
	zone, ok := parent.Raw.(*dns.ManagedZone)
	if !ok {
		return Result{}, fmt.Errorf("no zone data for %s", parent.Name)
	}

	svc, err := dns.NewService(ctx, opts...)
	if err != nil {
		return Result{}, fmt.Errorf("dns client: %w", err)
	}

	var (
		result Result
		capped bool
	)
	err = svc.ResourceRecordSets.List(p.ProjectID, zone.Name).
		Pages(ctx, func(page *dns.ResourceRecordSetsListResponse) error {
			for _, rr := range page.Rrsets {
				if len(result.Resources) >= maxRecordSets {
					capped = true
					return errStopPaging
				}
				result.Resources = append(result.Resources, recordSetResource(p, zone, rr))
			}
			return nil
		})
	if err != nil && !errors.Is(err, errStopPaging) {
		return result, err
	}

	if capped {
		result.Warnings = append(result.Warnings, cappedWarning(
			"first %d record sets only — the zone holds more than this table shows",
			maxRecordSets))
	}

	// Not sortResources: every record in a zone shares one location, so that
	// would sort on name alone and scatter a name's A, AAAA and TXT records
	// among its neighbours. Records are read as a group per name.
	sortRecordSets(result.Resources)
	return result, nil
}

func recordSetResource(p config.Project, zone *dns.ManagedZone, rr *dns.ResourceRecordSet) Resource {
	// Every record in a zone ends with the zone's own suffix, which is the same
	// on every row and costs a third of the column to repeat. The apex keeps
	// its full name, since "" would read as a missing value.
	name := rr.Name
	if trimmed := strings.TrimSuffix(name, "."+zone.DnsName); trimmed != name && trimmed != "" {
		name = trimmed
	}

	return Resource{
		Name:     name,
		Location: zone.Name,
		// A record set has no state of its own. The type is what the row is
		// really keyed by, and colouring every row identically is more honest
		// than inventing a health for a static value.
		Status: "ACTIVE",
		Row: []string{
			name,
			rr.Type,
			ttlSummary(rr.Ttl),
			routingSummary(rr.RoutingPolicy),
			recordData(rr),
		},
		Raw: rr,
		ConsoleURL: fmt.Sprintf(
			"https://console.cloud.google.com/net-services/dns/zones/%s?project=%s",
			url.PathEscape(zone.Name), url.QueryEscape(p.ProjectID)),
	}
}

// recordData renders the values, which is what the row exists for.
//
// A record set holds several — an A record with four addresses, an MX with five
// hosts — and they are shown together because a set with one missing value is a
// set that is wrong, and that is invisible if only the first is displayed.
func recordData(rr *dns.ResourceRecordSet) string {
	if len(rr.Rrdatas) > 0 {
		return strings.Join(rr.Rrdatas, " ")
	}
	// A routing policy record carries its values inside the policy instead, and
	// there is no room to unpack a weighted or geo split into one cell.
	if rr.RoutingPolicy != nil {
		return "(routing policy)"
	}
	return "-"
}

// routingSummary names the policy steering the answer, when there is one.
//
// Blank is the common case and reads fine as "-": the record answers the same
// way for everyone. Anything else means two clients resolving the same name can
// get different addresses, which is worth seeing before debugging why.
func routingSummary(rp *dns.RRSetRoutingPolicy) string {
	switch {
	case rp == nil:
		return "-"
	case rp.Geo != nil:
		return "geo"
	case rp.Wrr != nil:
		return "weighted"
	case rp.PrimaryBackup != nil:
		return "failover"
	default:
		return "policy"
	}
}

// ttlSummary renders seconds the way TTLs are discussed — 300 stays 300, but
// 86400 is a day and reading that off five digits is work.
func ttlSummary(seconds int64) string {
	if seconds <= 0 {
		return "-"
	}
	if seconds < 3600 {
		return fmt.Sprintf("%ds", seconds)
	}
	return shortDuration(secondsDuration(seconds))
}

// sortRecordSets groups a name's records together, then orders by type within
// the name, so a name's A and its AAAA sit on adjacent rows.
func sortRecordSets(resources []Resource) {
	stableSortBy(resources, func(a, b Resource) bool {
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		return recordType(a) < recordType(b)
	})
}

func recordType(r Resource) string {
	rr, ok := r.Raw.(*dns.ResourceRecordSet)
	if !ok {
		return ""
	}
	return rr.Type
}
