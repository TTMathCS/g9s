package gcp

import (
	"time"

	"cloud.google.com/go/compute/apiv1/computepb"
	"cloud.google.com/go/dataproc/v2/apiv1/dataprocpb"
	"cloud.google.com/go/orchestration/airflow/service/apiv1/servicepb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/TTMathCS/g9s/internal/config"
)

func strPtr(s string) *string { return &s }

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
