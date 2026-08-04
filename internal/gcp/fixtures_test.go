package gcp

import (
	"time"

	"cloud.google.com/go/compute/apiv1/computepb"
	"cloud.google.com/go/container/apiv1/containerpb"
	"cloud.google.com/go/dataproc/v2/apiv1/dataprocpb"
	"cloud.google.com/go/orchestration/airflow/service/apiv1/servicepb"
	"cloud.google.com/go/storage"
	artifactregistry "google.golang.org/api/artifactregistry/v1"
	batch "google.golang.org/api/batch/v1"
	bigquery "google.golang.org/api/bigquery/v2"
	bigqueryreservation "google.golang.org/api/bigqueryreservation/v1"
	bigtableadmin "google.golang.org/api/bigtableadmin/v2"
	certificatemanager "google.golang.org/api/certificatemanager/v1"
	cloudbuild "google.golang.org/api/cloudbuild/v1"
	clouderrorreporting "google.golang.org/api/clouderrorreporting/v1beta1"
	cloudfunctions "google.golang.org/api/cloudfunctions/v2"
	cloudkms "google.golang.org/api/cloudkms/v1"
	cloudresourcemanager "google.golang.org/api/cloudresourcemanager/v1"
	cloudscheduler "google.golang.org/api/cloudscheduler/v1"
	cloudtasks "google.golang.org/api/cloudtasks/v2"
	compute "google.golang.org/api/compute/v1"
	dataflow "google.golang.org/api/dataflow/v1b3"
	datafusion "google.golang.org/api/datafusion/v1"
	datastream "google.golang.org/api/datastream/v1"
	dns "google.golang.org/api/dns/v1"
	firestore "google.golang.org/api/firestore/v1"
	iam "google.golang.org/api/iam/v1"
	memcache "google.golang.org/api/memcache/v1"
	monitoring "google.golang.org/api/monitoring/v3"
	pubsub "google.golang.org/api/pubsub/v1"
	redis "google.golang.org/api/redis/v1"
	run "google.golang.org/api/run/v2"
	secretmanager "google.golang.org/api/secretmanager/v1"
	spanner "google.golang.org/api/spanner/v1"
	sqladmin "google.golang.org/api/sqladmin/v1"
	tpu "google.golang.org/api/tpu/v2"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/TTMathCS/g9s/internal/config"
)

func strPtr(s string) *string       { return &s }
func boolPtr(b bool) *bool          { return &b }
func int32Ptr(i int32) *int32       { return &i }
func float32Ptr(f float32) *float32 { return &f }
func int64Ptr(i int64) *int64       { return &i }

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
		// A boot disk that goes with the VM and a data disk that does not,
		// which is the distinction the disks drill-down exists to show.
		Disks: []*computepb.AttachedDisk{
			{
				DeviceName: strPtr("persistent-disk-0"),
				Source:     strPtr("https://www.googleapis.com/compute/v1/projects/sandbox-123/zones/us-central1-a/disks/web-01-boot"),
				DiskSizeGb: int64Ptr(50),
				Boot:       boolPtr(true),
				AutoDelete: boolPtr(true),
				Mode:       strPtr("READ_WRITE"),
				Type:       strPtr("PERSISTENT"),
				Index:      int32Ptr(0),
			},
			{
				DeviceName: strPtr("data"),
				Source:     strPtr("https://www.googleapis.com/compute/v1/projects/sandbox-123/zones/us-central1-a/disks/web-01-data"),
				DiskSizeGb: int64Ptr(500),
				Boot:       boolPtr(false),
				AutoDelete: boolPtr(false),
				Mode:       strPtr("READ_WRITE"),
				Type:       strPtr("PERSISTENT"),
				Index:      int32Ptr(1),
			},
		},
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
		// One rule that costs money and one that loses data, which are the two
		// things this bucket's lifecycle can do to you.
		Lifecycle: storage.Lifecycle{Rules: []storage.LifecycleRule{
			{
				Action:    storage.LifecycleAction{Type: "SetStorageClass", StorageClass: "NEARLINE"},
				Condition: storage.LifecycleCondition{AgeInDays: 30, Liveness: storage.Live},
			},
			{
				Action: storage.LifecycleAction{Type: "Delete"},
				Condition: storage.LifecycleCondition{
					AgeInDays:        365,
					NumNewerVersions: 3,
					MatchesPrefix:    []string{"exports/"},
				},
			},
		}},
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

func testDataprocJob() *dataprocpb.Job {
	submitted := time.Now().Add(-25 * time.Minute)
	return &dataprocpb.Job{
		Reference: &dataprocpb.JobReference{ProjectId: "sandbox-123", JobId: "nightly-etl-0417"},
		Placement: &dataprocpb.JobPlacement{ClusterName: "analytics-cluster"},
		TypeJob:   &dataprocpb.Job_PysparkJob{PysparkJob: &dataprocpb.PySparkJob{MainPythonFileUri: "gs://acme/etl/main.py"}},
		Status: &dataprocpb.JobStatus{
			State:          dataprocpb.JobStatus_RUNNING,
			StateStartTime: timestamppb.New(submitted.Add(time.Minute)),
		},
		StatusHistory: []*dataprocpb.JobStatus{{
			State:          dataprocpb.JobStatus_PENDING,
			StateStartTime: timestamppb.New(submitted),
		}},
	}
}

func testSecret() *secretmanager.Secret {
	return &secretmanager.Secret{
		Name:        "projects/sandbox-123/secrets/prod-db-password",
		CreateTime:  time.Now().Add(-180 * 24 * time.Hour).Format(time.RFC3339),
		Replication: &secretmanager.Replication{Automatic: &secretmanager.Automatic{}},
		// Not a whole number of days from now: a real rotation time is a
		// fixed instant, and pinning the fixture to an exact boundary would
		// make the test flake on the sub-second precision RFC3339 drops.
		Rotation: &secretmanager.Rotation{NextRotationTime: time.Now().Add(12*24*time.Hour + 6*time.Hour).Format(time.RFC3339)},
		Labels:   map[string]string{"env": "prod", "owner": "dataeng"},
	}
}

func testPubSubTopic() *pubsub.Topic {
	return &pubsub.Topic{
		Name:                     "projects/sandbox-123/topics/orders-events",
		MessageRetentionDuration: "604800s",
		Labels:                   map[string]string{"team": "dataeng"},
	}
}

func testPubSubSubscription() *pubsub.Subscription {
	return &pubsub.Subscription{
		Name:                     "projects/sandbox-123/subscriptions/orders-events-etl",
		Topic:                    "projects/sandbox-123/topics/orders-events",
		AckDeadlineSeconds:       60,
		MessageRetentionDuration: "604800s",
	}
}

func testCloudRunService() *run.GoogleCloudRunV2Service {
	return &run.GoogleCloudRunV2Service{
		Name:                "projects/sandbox-123/locations/us-central1/services/api-gateway",
		Uri:                 "https://api-gateway-abc123-uc.a.run.app",
		Ingress:             "INGRESS_TRAFFIC_ALL",
		LatestReadyRevision: "projects/sandbox-123/locations/us-central1/services/api-gateway/revisions/api-gateway-00042-xyz",
		TerminalCondition:   &run.GoogleCloudRunV2Condition{Type: "Ready", State: "CONDITION_SUCCEEDED"},
		CreateTime:          time.Now().Add(-60 * 24 * time.Hour).Format(time.RFC3339),
	}
}

func testCloudRunJob() *run.GoogleCloudRunV2Job {
	return &run.GoogleCloudRunV2Job{
		Name:              "projects/sandbox-123/locations/us-central1/jobs/nightly-report",
		ExecutionCount:    128,
		TerminalCondition: &run.GoogleCloudRunV2Condition{Type: "Ready", State: "CONDITION_SUCCEEDED"},
		LatestCreatedExecution: &run.GoogleCloudRunV2ExecutionReference{
			Name:             "projects/sandbox-123/locations/us-central1/jobs/nightly-report/executions/nightly-report-9x8w7",
			CompletionStatus: "EXECUTION_SUCCEEDED",
			CompletionTime:   time.Now().Add(-3 * time.Hour).Format(time.RFC3339),
		},
		CreateTime: time.Now().Add(-200 * 24 * time.Hour).Format(time.RFC3339),
	}
}

func testDataflowJob() *dataflow.Job {
	return &dataflow.Job{
		Id:               "2026-07-30_18_04_11-1234567890123456789",
		Name:             "orders-enrichment",
		Location:         "us-central1",
		Type:             "JOB_TYPE_STREAMING",
		CurrentState:     "JOB_STATE_RUNNING",
		CreateTime:       time.Now().Add(-11 * 24 * time.Hour).Format(time.RFC3339),
		CurrentStateTime: time.Now().Add(-11 * 24 * time.Hour).Format(time.RFC3339),
	}
}

func testServiceAccount() *iam.ServiceAccount {
	return &iam.ServiceAccount{
		Name:        "projects/sandbox-123/serviceAccounts/etl-runner@sandbox-123.iam.gserviceaccount.com",
		Email:       "etl-runner@sandbox-123.iam.gserviceaccount.com",
		DisplayName: "ETL runner",
		UniqueId:    "104729384756102938475",
		ProjectId:   "sandbox-123",
	}
}

// testServiceAccountKey is a key minted well inside the rotation window, so a
// test that wants a finding has to age it on purpose.
func testServiceAccountKey() *iam.ServiceAccountKey {
	return &iam.ServiceAccountKey{
		Name:            "projects/sandbox-123/serviceAccounts/etl-runner@sandbox-123.iam.gserviceaccount.com/keys/9f8e7d6c5b4a",
		KeyType:         "USER_MANAGED",
		KeyOrigin:       "KEY_ORIGIN_GOOGLE_PROVIDED",
		KeyAlgorithm:    "KEY_ALG_RSA_2048",
		ValidAfterTime:  time.Now().Add(-30 * 24 * time.Hour).Format(time.RFC3339),
		ValidBeforeTime: time.Now().Add(300 * 24 * time.Hour).Format(time.RFC3339),
	}
}

// testGKEClusterWithNodePools is a Standard cluster, since node pools are the
// thing Autopilot takes away. Two pools, one fixed and one autoscaled, because
// the difference between them is what the drill-down exists to show.
func testGKEClusterWithNodePools() *containerpb.Cluster {
	return &containerpb.Cluster{
		Name:     "batch-cluster",
		Location: "us-central1",
		Status:   containerpb.Cluster_RUNNING,
		NodePools: []*containerpb.NodePool{
			{
				Name:             "default-pool",
				Version:          "1.31.1-gke.1146000",
				InitialNodeCount: 2,
				Locations:        []string{"us-central1-a", "us-central1-b", "us-central1-c"},
				Status:           containerpb.NodePool_RUNNING,
				Config:           &containerpb.NodeConfig{MachineType: "e2-standard-4"},
				Management:       &containerpb.NodeManagement{AutoUpgrade: true, AutoRepair: true},
			},
			{
				Name:             "spot-workers",
				Version:          "1.31.1-gke.1146000",
				InitialNodeCount: 1,
				Locations:        []string{"us-central1-a"},
				Status:           containerpb.NodePool_RUNNING,
				Config:           &containerpb.NodeConfig{MachineType: "n2-highmem-8", Spot: true},
				Autoscaling: &containerpb.NodePoolAutoscaling{
					Enabled: true, MinNodeCount: 0, MaxNodeCount: 20,
				},
				Management: &containerpb.NodeManagement{AutoRepair: true},
			},
		},
	}
}

func testSQLDatabase() *sqladmin.Database {
	return &sqladmin.Database{
		Name:      "orders",
		Instance:  "orders-primary",
		Project:   "sandbox-123",
		Charset:   "UTF8",
		Collation: "en_US.UTF8",
	}
}

func testSQLUser() *sqladmin.User {
	return &sqladmin.User{
		Name:     "app-writer",
		Instance: "orders-primary",
		Project:  "sandbox-123",
		Host:     "%",
		Type:     "BUILT_IN",
	}
}

func testRecordSet() *dns.ResourceRecordSet {
	return &dns.ResourceRecordSet{
		Name:    "api.example.com.",
		Type:    "A",
		Ttl:     300,
		Rrdatas: []string{"34.120.0.10", "34.120.0.11"},
	}
}

func testBackendService() *compute.BackendService {
	return &compute.BackendService{
		Name:     "prod-web-backend",
		Protocol: "HTTPS",
		Backends: []*compute.Backend{
			{Group: "https://www.googleapis.com/compute/v1/projects/sandbox-123/zones/us-central1-a/instanceGroups/web-mig"},
		},
	}
}

func testHealthStatus() *compute.HealthStatus {
	return &compute.HealthStatus{
		HealthState: "HEALTHY",
		Instance:    "https://www.googleapis.com/compute/v1/projects/sandbox-123/zones/us-central1-a/instances/web-01",
		IpAddress:   "10.0.0.12",
		Port:        8080,
	}
}

// testSubnet carries the two things a subnet row exists to show and a network
// row cannot: the range it serves, and the secondary ranges GKE puts pods and
// services in.
func testSubnet() *computepb.Subnetwork {
	return &computepb.Subnetwork{
		Name:                  strPtr("prod-us-central1"),
		Network:               strPtr("https://www.googleapis.com/compute/v1/projects/sandbox-123/global/networks/prod-vpc"),
		IpCidrRange:           strPtr("10.128.0.0/20"),
		GatewayAddress:        strPtr("10.128.0.1"),
		PrivateIpGoogleAccess: boolPtr(true),
		SecondaryIpRanges: []*computepb.SubnetworkSecondaryRange{
			{RangeName: strPtr("pods"), IpCidrRange: strPtr("10.4.0.0/14")},
			{RangeName: strPtr("services"), IpCidrRange: strPtr("10.8.0.0/20")},
		},
		LogConfig: &computepb.SubnetworkLogConfig{
			Enable:       boolPtr(true),
			FlowSampling: float32Ptr(0.5),
		},
	}
}

// testBigQueryTable is partitioned with a required filter, which is the
// configuration that stops a SELECT * scanning the whole history.
func testBigQueryTable() *bigquery.TableListTables {
	return &bigquery.TableListTables{
		Id:             "sandbox-123:analytics.events",
		TableReference: &bigquery.TableReference{ProjectId: "sandbox-123", DatasetId: "analytics", TableId: "events"},
		Type:           "TABLE",
		CreationTime:   time.Now().Add(-140 * 24 * time.Hour).UnixMilli(),
		TimePartitioning: &bigquery.TimePartitioning{
			Type: "DAY", Field: "event_date", RequirePartitionFilter: true,
		},
		Clustering: &bigquery.Clustering{Fields: []string{"customer_id", "event_type"}},
	}
}

// testCloudRunRevision is the revision the test service's traffic points at.
func testCloudRunRevision() *run.GoogleCloudRunV2Revision {
	return &run.GoogleCloudRunV2Revision{
		Name:    "projects/sandbox-123/locations/us-central1/services/api-gateway/revisions/api-gateway-00042-xyz",
		Service: "projects/sandbox-123/locations/us-central1/services/api-gateway",
		Conditions: []*run.GoogleCloudRunV2Condition{
			{Type: "Ready", State: "CONDITION_SUCCEEDED"},
		},
		Containers: []*run.GoogleCloudRunV2Container{
			{Image: "us-docker.pkg.dev/acme-dataeng-prod-4471/services/api-gateway:v2.14.0"},
		},
		Scaling:    &run.GoogleCloudRunV2RevisionScaling{MinInstanceCount: 1, MaxInstanceCount: 40},
		CreateTime: time.Now().Add(-9 * 24 * time.Hour).Format(time.RFC3339),
	}
}

func testSecretVersion() *secretmanager.SecretVersion {
	return &secretmanager.SecretVersion{
		Name:       "projects/sandbox-123/secrets/prod-db-password/versions/7",
		State:      "ENABLED",
		CreateTime: time.Now().Add(-18 * 24 * time.Hour).Format(time.RFC3339),
	}
}

// testCloudRunExecution finished cleanly across every task, so a test that
// wants a failure has to make one.
func testCloudRunExecution() *run.GoogleCloudRunV2Execution {
	start := time.Now().Add(-3 * time.Hour)
	return &run.GoogleCloudRunV2Execution{
		Name:           "projects/sandbox-123/locations/us-central1/jobs/nightly-report/executions/nightly-report-9x8w7",
		Job:            "projects/sandbox-123/locations/us-central1/jobs/nightly-report",
		TaskCount:      12,
		SucceededCount: 12,
		Conditions: []*run.GoogleCloudRunV2Condition{
			{Type: "Completed", State: "CONDITION_SUCCEEDED"},
		},
		CreateTime:     start.Format(time.RFC3339),
		StartTime:      start.Format(time.RFC3339),
		CompletionTime: start.Add(7 * time.Minute).Format(time.RFC3339),
	}
}

// testDisk is unattached and has been for a while, which is the finding the
// kind exists to surface.
func testDisk() *computepb.Disk {
	return &computepb.Disk{
		Name:                strPtr("etl-scratch-old"),
		SizeGb:              int64Ptr(500),
		Type:                strPtr("https://www.googleapis.com/compute/v1/projects/sandbox-123/zones/us-central1-a/diskTypes/pd-ssd"),
		Status:              strPtr("READY"),
		CreationTimestamp:   strPtr(time.Now().Add(-400 * 24 * time.Hour).Format(time.RFC3339)),
		LastDetachTimestamp: strPtr(time.Now().Add(-240 * 24 * time.Hour).Format(time.RFC3339)),
	}
}

func testSnapshot() *compute.Snapshot {
	return &compute.Snapshot{
		Name:              "orders-db-2026-07-31",
		SourceDisk:        "https://www.googleapis.com/compute/v1/projects/sandbox-123/zones/us-central1-a/disks/orders-db",
		DiskSizeGb:        500,
		StorageBytes:      64 * 1024 * 1024 * 1024,
		StorageLocations:  []string{"us-central1"},
		Status:            "READY",
		CreationTimestamp: time.Now().Add(-36 * time.Hour).Format(time.RFC3339),
	}
}

func testInstanceGroupManager() *compute.InstanceGroupManager {
	return &compute.InstanceGroupManager{
		Name:              "api-mig",
		Region:            "https://www.googleapis.com/compute/v1/projects/sandbox-123/regions/us-central1",
		TargetSize:        6,
		InstanceTemplate:  "https://www.googleapis.com/compute/v1/projects/sandbox-123/global/instanceTemplates/api-v12",
		Status:            &compute.InstanceGroupManagerStatus{IsStable: true},
		UpdatePolicy:      &compute.InstanceGroupManagerUpdatePolicy{Type: "PROACTIVE"},
		CreationTimestamp: time.Now().Add(-150 * 24 * time.Hour).Format(time.RFC3339),
	}
}

func testManagedInstance() *compute.ManagedInstance {
	return &compute.ManagedInstance{
		Name:           "api-mig-2f8q",
		Instance:       "https://www.googleapis.com/compute/v1/projects/sandbox-123/zones/us-central1-b/instances/api-mig-2f8q",
		InstanceStatus: "RUNNING",
		CurrentAction:  "NONE",
		Version: &compute.ManagedInstanceVersion{
			Name:             "primary",
			InstanceTemplate: "https://www.googleapis.com/compute/v1/projects/sandbox-123/global/instanceTemplates/api-v12",
		},
	}
}

func testInstanceTemplate() *compute.InstanceTemplate {
	return &compute.InstanceTemplate{
		Name:              "gpu-worker-v4",
		Region:            "https://www.googleapis.com/compute/v1/projects/sandbox-123/regions/us-central1",
		CreationTimestamp: time.Now().Add(-80 * 24 * time.Hour).Format(time.RFC3339),
		Properties: &compute.InstanceProperties{
			MachineType: "g2-standard-8",
			Disks:       []*compute.AttachedDisk{{Boot: true}, {Boot: false}},
			NetworkInterfaces: []*compute.NetworkInterface{
				{Network: "https://www.googleapis.com/compute/v1/projects/sandbox-123/global/networks/prod-vpc"},
			},
			GuestAccelerators: []*compute.AcceleratorConfig{{
				AcceleratorType:  "nvidia-l4",
				AcceleratorCount: 1,
			}},
		},
	}
}

func testReservation() *compute.Reservation {
	return &compute.Reservation{
		Name:                        "gpu-inference-capacity",
		Zone:                        "https://www.googleapis.com/compute/v1/projects/sandbox-123/zones/us-central1-a",
		Status:                      "READY",
		SpecificReservationRequired: true,
		CreationTimestamp:           time.Now().Add(-30 * 24 * time.Hour).Format(time.RFC3339),
		SpecificReservation: &compute.AllocationSpecificSKUReservation{
			Count:      4,
			InUseCount: 2,
			InstanceProperties: &compute.AllocationSpecificSKUAllocationReservedInstanceProperties{
				MachineType: "g2-standard-8",
				GuestAccelerators: []*compute.AcceleratorConfig{{
					AcceleratorType:  "nvidia-l4",
					AcceleratorCount: 1,
				}},
			},
		},
	}
}

func testRoute() *compute.Route {
	return &compute.Route{
		Name:              "egress-via-appliance",
		Network:           "https://www.googleapis.com/compute/v1/projects/sandbox-123/global/networks/prod-vpc",
		DestRange:         "0.0.0.0/0",
		Priority:          900,
		NextHopInstance:   "https://www.googleapis.com/compute/v1/projects/sandbox-123/zones/us-central1-a/instances/egress-appliance",
		RouteStatus:       "ACTIVE",
		RouteType:         "STATIC",
		Tags:              []string{"private-egress"},
		CreationTimestamp: time.Now().Add(-200 * 24 * time.Hour).Format(time.RFC3339),
	}
}

func testRouter() *compute.Router {
	return &compute.Router{
		Name:              "prod-router",
		Region:            "https://www.googleapis.com/compute/v1/projects/sandbox-123/regions/us-central1",
		Network:           "https://www.googleapis.com/compute/v1/projects/sandbox-123/global/networks/prod-vpc",
		Bgp:               &compute.RouterBgp{Asn: 64514},
		BgpPeers:          []*compute.RouterBgpPeer{{Name: "onprem-primary"}},
		Interfaces:        []*compute.RouterInterface{{Name: "onprem-vlan"}},
		CreationTimestamp: time.Now().Add(-320 * 24 * time.Hour).Format(time.RFC3339),
		Nats: []*compute.RouterNat{{
			Name:                          "prod-egress",
			Type:                          "PUBLIC",
			NatIpAllocateOption:           "MANUAL_ONLY",
			NatIps:                        []string{"https://www.googleapis.com/compute/v1/projects/sandbox-123/regions/us-central1/addresses/nat-egress-1"},
			SourceSubnetworkIpRangesToNat: "LIST_OF_SUBNETWORKS",
			Subnetworks: []*compute.RouterNatSubnetworkToNat{{
				Name: "https://www.googleapis.com/compute/v1/projects/sandbox-123/regions/us-central1/subnetworks/prod-us-central1",
			}},
			MinPortsPerVm: 128,
			LogConfig:     &compute.RouterNatLogConfig{Enable: true, Filter: "ERRORS_ONLY"},
		}},
	}
}

func testAddress() *compute.Address {
	return &compute.Address{
		Name:              "nat-egress-1",
		Region:            "https://www.googleapis.com/compute/v1/projects/sandbox-123/regions/us-central1",
		Address:           "34.72.1.20",
		AddressType:       "EXTERNAL",
		IpVersion:         "IPV4",
		NetworkTier:       "PREMIUM",
		Status:            "IN_USE",
		Users:             []string{"https://www.googleapis.com/compute/v1/projects/sandbox-123/regions/us-central1/routers/prod-router"},
		CreationTimestamp: time.Now().Add(-320 * 24 * time.Hour).Format(time.RFC3339),
	}
}

func testFunction() *cloudfunctions.Function {
	return &cloudfunctions.Function{
		Name:        "projects/sandbox-123/locations/us-central1/functions/thumbnailer",
		Environment: "GEN_2",
		State:       "ACTIVE",
		BuildConfig: &cloudfunctions.BuildConfig{Runtime: "python312", EntryPoint: "handle"},
		EventTrigger: &cloudfunctions.EventTrigger{
			EventType: "google.cloud.storage.object.v1.finalized",
		},
		UpdateTime: time.Now().Add(-16 * 24 * time.Hour).Format(time.RFC3339),
	}
}

// testCryptoKey rotates on a schedule and is not overdue: the ordinary case, so
// a test asserting a finding has to set one up rather than inherit it.
func testCryptoKey() *cloudkms.CryptoKey {
	return &cloudkms.CryptoKey{
		Name:             "projects/sandbox-123/locations/us-central1/keyRings/app-secrets/cryptoKeys/db-encryption",
		Purpose:          "ENCRYPT_DECRYPT",
		RotationPeriod:   "7776000s",
		NextRotationTime: time.Now().Add(30 * 24 * time.Hour).Format(time.RFC3339),
		CreateTime:       time.Now().Add(-400 * 24 * time.Hour).Format(time.RFC3339),
		Primary: &cloudkms.CryptoKeyVersion{
			Name:            "projects/sandbox-123/locations/us-central1/keyRings/app-secrets/cryptoKeys/db-encryption/cryptoKeyVersions/7",
			State:           "ENABLED",
			ProtectionLevel: "SOFTWARE",
			Algorithm:       "GOOGLE_SYMMETRIC_ENCRYPTION",
		},
		VersionTemplate: &cloudkms.CryptoKeyVersionTemplate{ProtectionLevel: "SOFTWARE"},
	}
}

// testSchedulerJob last ran cleanly. The failing and paused cases are the
// findings, so tests build those explicitly.
func testSchedulerJob() *cloudscheduler.Job {
	return &cloudscheduler.Job{
		Name:            "projects/sandbox-123/locations/us-central1/jobs/nightly-rollup",
		Schedule:        "0 3 * * *",
		TimeZone:        "Etc/UTC",
		State:           "ENABLED",
		LastAttemptTime: time.Now().Add(-9 * time.Hour).Format(time.RFC3339),
		HttpTarget: &cloudscheduler.HttpTarget{
			Uri:        "https://api.internal.example.com/jobs/rollup",
			HttpMethod: "POST",
		},
	}
}

// testRepository has no cleanup policy, which is the finding this kind exists
// to surface: a registry nothing prunes grows until someone reads the bill.
func testRepository() *artifactregistry.Repository {
	return &artifactregistry.Repository{
		Name:       "projects/sandbox-123/locations/us-central1/repositories/service-images",
		Format:     "DOCKER",
		Mode:       "STANDARD_REPOSITORY",
		SizeBytes:  412 * 1024 * 1024 * 1024,
		CreateTime: time.Now().Add(-500 * 24 * time.Hour).Format(time.RFC3339),
		UpdateTime: time.Now().Add(-30 * time.Hour).Format(time.RFC3339),
	}
}

// testRedisInstance is standard-tier with AUTH on — the healthy case, so a test
// asserting a finding has to build one rather than inherit it.
func testRedisInstance() *redis.Instance {
	return &redis.Instance{
		Name:         "projects/sandbox-123/locations/us-central1/instances/session-cache",
		Tier:         "STANDARD_HA",
		MemorySizeGb: 16,
		RedisVersion: "REDIS_7_0",
		AuthEnabled:  true,
		State:        "READY",
		Host:         "10.0.0.3",
	}
}

// testSpannerInstance is sized in processing units rather than nodes, which is
// the half of the capacity problem that reads as a tiny instance.
func testSpannerInstance() *spanner.Instance {
	return &spanner.Instance{
		Name:            "projects/sandbox-123/instances/orders-prod",
		Config:          "projects/sandbox-123/instanceConfigs/regional-us-central1",
		ProcessingUnits: 3000,
		Edition:         "ENTERPRISE",
		State:           "READY",
	}
}

// testSpannerDatabase has drop protection off, which is the default and the
// finding.
func testSpannerDatabase() *spanner.Database {
	return &spanner.Database{
		Name:                   "projects/sandbox-123/instances/orders-prod/databases/orders",
		State:                  "READY",
		DatabaseDialect:        "GOOGLE_STANDARD_SQL",
		VersionRetentionPeriod: "1h",
		EnableDropProtection:   false,
	}
}

// testBigtableInstance is a production instance — the ordinary case, so a test
// asserting the development finding has to build one.
func testBigtableInstance() *bigtableadmin.Instance {
	return &bigtableadmin.Instance{
		Name:        "projects/sandbox-123/instances/events-store",
		DisplayName: "Events Store",
		Type:        "PRODUCTION",
		Edition:     "STANDARD",
		State:       "READY",
	}
}

func testBigtableCluster() *bigtableadmin.Cluster {
	return &bigtableadmin.Cluster{
		Name:               "projects/sandbox-123/instances/events-store/clusters/events-c1",
		Location:           "projects/sandbox-123/locations/us-central1-b",
		ServeNodes:         6,
		DefaultStorageType: "SSD",
		State:              "READY",
	}
}

// testFirestoreDatabase has both recovery guards off, which is the default and
// the finding.
func testFirestoreDatabase() *firestore.GoogleFirestoreAdminV1Database {
	return &firestore.GoogleFirestoreAdminV1Database{
		Name:                          "projects/sandbox-123/databases/(default)",
		LocationId:                    "nam5",
		Type:                          "FIRESTORE_NATIVE",
		ConcurrencyMode:               "PESSIMISTIC",
		PointInTimeRecoveryEnablement: "POINT_IN_TIME_RECOVERY_DISABLED",
		DeleteProtectionState:         "DELETE_PROTECTION_DISABLED",
	}
}

// testMemcacheInstance has every node serving; the degraded case is built by
// the test that needs it.
func testMemcacheInstance() *memcache.Instance {
	return &memcache.Instance{
		Name:            "projects/sandbox-123/locations/us-central1/instances/page-cache",
		NodeCount:       3,
		MemcacheVersion: "MEMCACHE_1_5",
		State:           "READY",
		NodeConfig:      &memcache.NodeConfig{CpuCount: 2, MemorySizeMb: 4096},
		MemcacheNodes: []*memcache.Node{
			{NodeId: "node-1", State: "READY"},
			{NodeId: "node-2", State: "READY"},
			{NodeId: "node-3", State: "READY"},
		},
	}
}

// testStream is running cleanly with a full backfill; the paused and erroring
// cases are the findings, so the tests that need them build them.
func testStream() *datastream.Stream {
	return &datastream.Stream{
		Name:        "projects/sandbox-123/locations/us-central1/streams/orders-cdc",
		DisplayName: "Orders CDC",
		State:       "RUNNING",
		BackfillAll: &datastream.BackfillAllStrategy{},
		SourceConfig: &datastream.SourceConfig{
			SourceConnectionProfile: "projects/sandbox-123/locations/us-central1/connectionProfiles/orders-mysql",
			MysqlSourceConfig:       &datastream.MysqlSourceConfig{},
		},
		DestinationConfig: &datastream.DestinationConfig{
			DestinationConnectionProfile: "projects/sandbox-123/locations/us-central1/connectionProfiles/warehouse-bq",
			BigqueryDestinationConfig:    &datastream.BigQueryDestinationConfig{},
		},
		UpdateTime: time.Now().Add(-50 * time.Hour).Format(time.RFC3339),
	}
}

// testDataFusionInstance is Enterprise — the expensive, ordinary case.
func testDataFusionInstance() *datafusion.Instance {
	return &datafusion.Instance{
		Name:            "projects/sandbox-123/locations/us-central1/instances/etl-fusion",
		Type:            "ENTERPRISE",
		Version:         "6.10.1",
		State:           "RUNNING",
		PrivateInstance: true,
		CreateTime:      time.Now().Add(-200 * 24 * time.Hour).Format(time.RFC3339),
	}
}

// testBQReservation holds a baseline and shares its idle slots, which is the
// sensible default. The finding is the reservation that does not.
func testBQReservation() *bigqueryreservation.Reservation {
	return &bigqueryreservation.Reservation{
		Name:            "projects/sandbox-123/locations/US/reservations/analytics",
		SlotCapacity:    500,
		Edition:         "ENTERPRISE",
		IgnoreIdleSlots: false,
		Autoscale:       &bigqueryreservation.Autoscale{CurrentSlots: 100, MaxSlots: 1000},
	}
}

func testCloudBuild() *cloudbuild.Build {
	return &cloudbuild.Build{
		Id:             "9f2c1a4e-7b33-4d51-9c88-0a1b2c3d4e5f",
		Status:         "FAILURE",
		BuildTriggerId: "3c7a91be-1122-4433-8899-aabbccddeeff",
		StartTime:      time.Now().Add(-40 * time.Minute).Format(time.RFC3339),
		FinishTime:     time.Now().Add(-36 * time.Minute).Format(time.RFC3339),
		Substitutions: map[string]string{
			"TRIGGER_NAME": "deploy-api-on-main",
			"REPO_NAME":    "acme/api",
			"BRANCH_NAME":  "main",
		},
	}
}

func testTaskQueue() *cloudtasks.Queue {
	return &cloudtasks.Queue{
		Name:  "projects/sandbox-123/locations/us-central1/queues/order-webhooks",
		State: "PAUSED",
		RateLimits: &cloudtasks.RateLimits{
			MaxDispatchesPerSecond:  5,
			MaxConcurrentDispatches: 20,
		},
		RetryConfig: &cloudtasks.RetryConfig{MaxAttempts: -1},
	}
}

func testAlertPolicy() *monitoring.AlertPolicy {
	return &monitoring.AlertPolicy{
		Name:        "projects/sandbox-123/alertPolicies/1122334455",
		DisplayName: "ETL lag over 30 minutes",
		Enabled:     true,
		Combiner:    "OR",
		Conditions:  []*monitoring.Condition{{DisplayName: "lag > 1800s"}},
		// No notification channels: enabled, evaluating, and telling nobody.
		NotificationChannels: nil,
	}
}

func testBatchJob() *batch.Job {
	return &batch.Job{
		Name:       "projects/sandbox-123/locations/us-central1/jobs/nightly-render",
		CreateTime: time.Now().Add(-5 * time.Hour).Format(time.RFC3339),
		Status: &batch.JobStatus{
			State: "RUNNING",
			// Every task failing while the job still reports RUNNING is the
			// case the counts column exists to make visible.
			TaskGroups: map[string]batch.TaskGroupStatus{
				"group0": {Counts: map[string]string{"FAILED": "12", "RUNNING": "3", "SUCCEEDED": "0"}},
			},
		},
	}
}

func testCertificate() *certificatemanager.Certificate {
	return &certificatemanager.Certificate{
		Name:        "projects/sandbox-123/locations/global/certificates/api-edge",
		SanDnsnames: []string{"api.example.com", "www.example.com"},
		ExpireTime:  time.Now().Add(11 * 24 * time.Hour).Format(time.RFC3339),
	}
}

func testIAMBinding() *cloudresourcemanager.Binding {
	return &cloudresourcemanager.Binding{
		Role:    "roles/editor",
		Members: []string{"user:dana@example.com"},
	}
}

func testErrorGroup() *clouderrorreporting.ErrorGroupStats {
	return &clouderrorreporting.ErrorGroupStats{
		Group:              &clouderrorreporting.ErrorGroup{GroupId: "CNbn4pDDlqoCEAE"},
		Count:              11402,
		AffectedUsersCount: 87,
		LastSeenTime:       time.Now().Add(-4 * time.Minute).Format(time.RFC3339),
		Representative: &clouderrorreporting.ErrorEvent{
			Message: "java.lang.NullPointerException: orders.customer is null\n\tat com.acme.OrderService.price(OrderService.java:214)",
		},
		AffectedServices: []*clouderrorreporting.ServiceContext{
			{Service: "order-api", Version: "00042"},
		},
	}
}

func testTPUNode() *tpu.Node {
	return &tpu.Node{
		Name:             "projects/sandbox-123/locations/us-central1-a/nodes/train-v5-slice",
		AcceleratorType:  "v5litepod-16",
		RuntimeVersion:   "tpu-vm-tf-2.16.1",
		State:            "READY",
		SchedulingConfig: &tpu.SchedulingConfig{Preemptible: true},
	}
}
