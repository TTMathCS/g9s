package gcp

import (
	"time"

	"cloud.google.com/go/compute/apiv1/computepb"
	"cloud.google.com/go/container/apiv1/containerpb"
	"cloud.google.com/go/dataproc/v2/apiv1/dataprocpb"
	"cloud.google.com/go/orchestration/airflow/service/apiv1/servicepb"
	"cloud.google.com/go/storage"
	bigquery "google.golang.org/api/bigquery/v2"
	dns "google.golang.org/api/dns/v1"
	sqladmin "google.golang.org/api/sqladmin/v1"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/TTMathCS/g9s/internal/config"
)

func strPtr(s string) *string { return &s }
func boolPtr(b bool) *bool    { return &b }
func int32Ptr(i int32) *int32 { return &i }

func testProject() config.Project {
	return config.Project{
		Name:      "sandbox",
		ProjectID: "sandbox-123",
		Account:   "svc-support@example.com",
	}
}

func testInstance() *computepb.Instance {
	return &computepb.Instance{
		Name:              strPtr("web-01"),
		Zone:              strPtr("https://www.googleapis.com/compute/v1/projects/sandbox-123/zones/us-central1-a"),
		MachineType:       strPtr("https://www.googleapis.com/compute/v1/projects/sandbox-123/zones/us-central1-a/machineTypes/n2-standard-4"),
		Status:            strPtr("RUNNING"),
		CreationTimestamp: strPtr(time.Now().Add(-72 * time.Hour).Format(time.RFC3339)),
		NetworkInterfaces: []*computepb.NetworkInterface{{
			NetworkIP:     strPtr("10.0.0.5"),
			AccessConfigs: []*computepb.AccessConfig{{NatIP: strPtr("34.1.2.3")}},
		}},
	}
}

func testCluster() *dataprocpb.Cluster {
	return &dataprocpb.Cluster{
		ProjectId:   "sandbox-123",
		ClusterName: "analytics-cluster",
		Status: &dataprocpb.ClusterStatus{
			State:          dataprocpb.ClusterStatus_RUNNING,
			StateStartTime: timestamppb.New(time.Now().Add(-6 * time.Hour)),
		},
		Config: &dataprocpb.ClusterConfig{
			SoftwareConfig:        &dataprocpb.SoftwareConfig{ImageVersion: "2.1-debian11"},
			WorkerConfig:          &dataprocpb.InstanceGroupConfig{NumInstances: 8},
			SecondaryWorkerConfig: &dataprocpb.InstanceGroupConfig{NumInstances: 4},
		},
	}
}

func testGKECluster() *containerpb.Cluster {
	return &containerpb.Cluster{
		Name:                 "platform-cluster",
		Location:             "us-central1",
		Status:               containerpb.Cluster_RUNNING,
		CurrentNodeCount:     6,
		CurrentMasterVersion: "1.31.1-gke.1146000",
		CreateTime:           time.Now().Add(-90 * 24 * time.Hour).Format(time.RFC3339),
		Autopilot:            &containerpb.Autopilot{Enabled: true},
	}
}

func testSQLInstance() *sqladmin.DatabaseInstance {
	return &sqladmin.DatabaseInstance{
		Name:            "orders-primary",
		Region:          "us-central1",
		DatabaseVersion: "POSTGRES_15",
		State:           "RUNNABLE",
		CreateTime:      time.Now().Add(-200 * 24 * time.Hour).Format(time.RFC3339),
		Settings: &sqladmin.Settings{
			Tier:             "db-custom-4-15360",
			AvailabilityType: "REGIONAL",
		},
	}
}

func testBucket() *storage.BucketAttrs {
	return &storage.BucketAttrs{
		Name:              "acme-prod-data-exports",
		Location:          "US-CENTRAL1",
		StorageClass:      "STANDARD",
		VersioningEnabled: true,
		Created:           time.Now().Add(-400 * 24 * time.Hour),
	}
}

func testEnvironment() *servicepb.Environment {
	return &servicepb.Environment{
		Name:  "projects/sandbox-123/locations/us-central1/environments/etl-prod",
		State: servicepb.Environment_RUNNING,
		Config: &servicepb.EnvironmentConfig{
			SoftwareConfig: &servicepb.SoftwareConfig{ImageVersion: "composer-2.6.6-airflow-2.7.3"},
			AirflowUri:     "https://abc123-dot-us-central1.composer.googleusercontent.com",
		},
		CreateTime: timestamppb.New(time.Now().Add(-30 * 24 * time.Hour)),
	}
}

// --- networking fixtures ---

func testNetwork() *computepb.Network {
	return &computepb.Network{
		Name:                  strPtr("prod-vpc"),
		AutoCreateSubnetworks: boolPtr(false),
		RoutingConfig:         &computepb.NetworkRoutingConfig{RoutingMode: strPtr("GLOBAL")},
		Mtu:                   int32Ptr(1460),
		Subnetworks: []string{
			"https://www.googleapis.com/compute/v1/projects/sandbox-123/regions/us-central1/subnetworks/prod-us-central1",
			"https://www.googleapis.com/compute/v1/projects/sandbox-123/regions/us-east1/subnetworks/prod-us-east1",
		},
		CreationTimestamp: strPtr(time.Now().Add(-500 * 24 * time.Hour).Format(time.RFC3339)),
	}
}

func testFirewall() *computepb.Firewall {
	return &computepb.Firewall{
		Name:              strPtr("allow-internal-ssh"),
		Network:           strPtr("https://www.googleapis.com/compute/v1/projects/sandbox-123/global/networks/prod-vpc"),
		Direction:         strPtr("INGRESS"),
		Priority:          int32Ptr(1000),
		Disabled:          boolPtr(false),
		SourceRanges:      []string{"10.0.0.0/8"},
		Allowed:           []*computepb.Allowed{{IPProtocol: strPtr("tcp"), Ports: []string{"22"}}},
		CreationTimestamp: strPtr(time.Now().Add(-120 * 24 * time.Hour).Format(time.RFC3339)),
	}
}

func testForwardingRule() *computepb.ForwardingRule {
	return &computepb.ForwardingRule{
		Name:                strPtr("prod-https-lb"),
		IPAddress:           strPtr("34.120.0.10"),
		IPProtocol:          strPtr("TCP"),
		PortRange:           strPtr("443-443"),
		LoadBalancingScheme: strPtr("EXTERNAL_MANAGED"),
		CreationTimestamp:   strPtr(time.Now().Add(-45 * 24 * time.Hour).Format(time.RFC3339)),
	}
}

func testDNSZone() *dns.ManagedZone {
	return &dns.ManagedZone{
		Name:         "example-com",
		DnsName:      "example.com.",
		Visibility:   "public",
		NameServers:  []string{"ns-cloud-a1.googledomains.com.", "ns-cloud-a2.googledomains.com."},
		CreationTime: time.Now().Add(-800 * 24 * time.Hour).Format(time.RFC3339),
	}
}

func testVPNTunnel() *computepb.VpnTunnel {
	return &computepb.VpnTunnel{
		Name:              strPtr("onprem-tunnel-1"),
		Region:            strPtr("https://www.googleapis.com/compute/v1/projects/sandbox-123/regions/us-central1"),
		PeerIp:            strPtr("203.0.113.10"),
		VpnGateway:        strPtr("https://www.googleapis.com/compute/v1/projects/sandbox-123/regions/us-central1/vpnGateways/ha-vpn-1"),
		Status:            strPtr("ESTABLISHED"),
		CreationTimestamp: strPtr(time.Now().Add(-260 * 24 * time.Hour).Format(time.RFC3339)),
	}
}

func testInterconnectAttachment() *computepb.InterconnectAttachment {
	return &computepb.InterconnectAttachment{
		Name:              strPtr("dc1-attachment"),
		Type:              strPtr("DEDICATED"),
		Bandwidth:         strPtr("BPS_10G"),
		VlanTag8021Q:      int32Ptr(1010),
		State:             strPtr("ACTIVE"),
		AdminEnabled:      boolPtr(true),
		CreationTimestamp: strPtr(time.Now().Add(-370 * 24 * time.Hour).Format(time.RFC3339)),
	}
}

func testServiceAttachment() *computepb.ServiceAttachment {
	return &computepb.ServiceAttachment{
		Name:                 strPtr("analytics-psc"),
		TargetService:        strPtr("https://www.googleapis.com/compute/v1/projects/sandbox-123/regions/us-central1/forwardingRules/analytics-ilb"),
		ConnectionPreference: strPtr("ACCEPT_MANUAL"),
		ConnectedEndpoints: []*computepb.ServiceAttachmentConnectedEndpoint{
			{Endpoint: strPtr("consumer-1")},
			{Endpoint: strPtr("consumer-2")},
		},
		CreationTimestamp: strPtr(time.Now().Add(-90 * 24 * time.Hour).Format(time.RFC3339)),
	}
}

func testBigQueryDataset() *bigquery.DatasetListDatasets {
	return &bigquery.DatasetListDatasets{
		Id:               "sandbox-123:analytics",
		DatasetReference: &bigquery.DatasetReference{ProjectId: "sandbox-123", DatasetId: "analytics"},
		Location:         "northamerica-northeast1",
		Type:             "DEFAULT",
		Labels:           map[string]string{"team": "dataeng", "env": "prod"},
	}
}

func testBigQueryJob() *bigquery.JobListJobs {
	start := time.Now().Add(-4 * time.Minute)
	return &bigquery.JobListJobs{
		Id:           "sandbox-123:northamerica-northeast1.bquxjob_1a2b3c",
		JobReference: &bigquery.JobReference{ProjectId: "sandbox-123", JobId: "bquxjob_1a2b3c", Location: "northamerica-northeast1"},
		Configuration: &bigquery.JobConfiguration{
			JobType: "QUERY",
			Query:   &bigquery.JobConfigurationQuery{Query: "SELECT count(*) FROM `analytics.events`"},
		},
		Status:    &bigquery.JobStatus{State: "RUNNING"},
		UserEmail: "svc-dataeng-prod@example.com",
		Statistics: &bigquery.JobStatistics{
			CreationTime: start.Add(-time.Second).UnixMilli(),
			StartTime:    start.UnixMilli(),
			Query:        &bigquery.JobStatistics2{TotalBytesProcessed: 3 * 1024 * 1024 * 1024},
		},
	}
}
