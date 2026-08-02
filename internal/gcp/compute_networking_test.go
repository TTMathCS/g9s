package gcp

import (
	"testing"

	compute "google.golang.org/api/compute/v1"
)

func TestRouteResourceShape(t *testing.T) {
	r := routeResource(testProject(), testRoute())

	if r.Row[1] != "prod-vpc" || r.Row[2] != "0.0.0.0/0" || r.Row[3] != "900" {
		t.Errorf("network/destination/priority = %q/%q/%q", r.Row[1], r.Row[2], r.Row[3])
	}
	if r.Row[4] != "instance:egress-appliance" || r.Row[5] != "static" {
		t.Errorf("next hop/type = %q/%q", r.Row[4], r.Row[5])
	}
	if r.Status != "ACTIVE" {
		t.Errorf("status = %q", r.Status)
	}
}

func TestRouteUsesAPIStatus(t *testing.T) {
	route := testRoute()
	route.RouteStatus = "DROPPED"
	if got := routeStatus(route); got != "DROPPED" {
		t.Errorf("status = %q", got)
	}
}

func TestRoutesSortPriorityNumerically(t *testing.T) {
	resources := []Resource{
		routeResource(testProject(), &compute.Route{Name: "low", Network: "net", Priority: 1000}),
		routeResource(testProject(), &compute.Route{Name: "high", Network: "net", Priority: 90}),
	}
	sortRouteResources(resources)
	if resources[0].Name != "high" || resources[1].Name != "low" {
		t.Errorf("route order = %s, %s", resources[0].Name, resources[1].Name)
	}
}

func TestRouteWarningMarksItDegraded(t *testing.T) {
	route := testRoute()
	route.Warnings = []*compute.RouteWarnings{{Code: "NEXT_HOP_NOT_RUNNING", Message: "stopped"}}
	if got := routeStatus(route); got != "DEGRADED" {
		t.Errorf("status = %q", got)
	}
}

func TestRouterResourceShape(t *testing.T) {
	r := routerResource(testProject(), "us-central1", testRouter())
	if r.Row[2] != "prod-vpc" || r.Row[3] != "64514" {
		t.Errorf("network/asn = %q/%q", r.Row[2], r.Row[3])
	}
	if r.Row[4] != "1" || r.Row[5] != "1" || r.Row[6] != "1" {
		t.Errorf("peer/interface/NAT counts = %q/%q/%q", r.Row[4], r.Row[5], r.Row[6])
	}
}

func TestRouterNATResourceShape(t *testing.T) {
	router := testRouter()
	r := routerNATResource(testProject(), router, router.Nats[0])

	if r.Row[1] != "public" || r.Row[2] != "manual_only" {
		t.Errorf("type/allocation = %q/%q", r.Row[1], r.Row[2])
	}
	if r.Row[3] != "nat-egress-1" || r.Row[4] != "prod-us-central1" {
		t.Errorf("IP/sources = %q/%q", r.Row[3], r.Row[4])
	}
	if r.Row[5] != "128" || r.Row[6] != "errors_only" {
		t.Errorf("ports/logging = %q/%q", r.Row[5], r.Row[6])
	}
}

func TestAutoNATDoesNotPretendToHaveNamedAddresses(t *testing.T) {
	if got := routerNATIPs(&compute.RouterNat{NatIpAllocateOption: "AUTO_ONLY"}); got != "automatic" {
		t.Errorf("NAT IPs = %q", got)
	}
}

func TestAddressResourceShape(t *testing.T) {
	r := addressResource(testProject(), "us-central1", testAddress())
	if r.Row[2] != "34.72.1.20" || r.Row[3] != "external/ipv4" {
		t.Errorf("address/type = %q/%q", r.Row[2], r.Row[3])
	}
	if r.Row[5] != "premium" || r.Row[6] != "1" || r.Status != "IN_USE" {
		t.Errorf("tier/users/status = %q/%q/%q", r.Row[5], r.Row[6], r.Status)
	}
}
