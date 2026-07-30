package ui

import (
	"strings"
	"testing"

	"cloud.google.com/go/compute/apiv1/computepb"
	"cloud.google.com/go/container/apiv1/containerpb"
	"google.golang.org/protobuf/proto"

	"github.com/TTMathCS/g9s/internal/gcp"
)

func TestDetailRedactsVPNSharedSecret(t *testing.T) {
	// A tunnel's pre-shared key comes back in the object the API returns, so
	// describing a tunnel used to print the IPsec PSK into the terminal, the
	// scrollback and anything recording the session — and `y` copied it.
	const psk = "s3cret-preshared-key"

	out := renderDetail(gcp.Resource{Raw: &computepb.VpnTunnel{
		Name:             proto.String("to-onprem"),
		SharedSecret:     proto.String(psk),
		SharedSecretHash: proto.String("AVW1kZ..."),
		PeerIp:           proto.String("203.0.113.10"),
	}})

	if strings.Contains(out, psk) {
		t.Errorf("the shared secret survived into the detail pane:\n%s", out)
	}
	if strings.Contains(out, "AVW1kZ") {
		t.Errorf("the shared secret hash survived into the detail pane:\n%s", out)
	}
	// Redacted, not hidden: that a secret is configured is worth seeing, and so
	// is the rest of the tunnel.
	if !strings.Contains(out, redactedMarker) {
		t.Errorf("no redaction marker where the secret was:\n%s", out)
	}
	if !strings.Contains(out, "203.0.113.10") {
		t.Errorf("redaction ate the rest of the object:\n%s", out)
	}
}

func TestDetailRedactsGKEMasterAuth(t *testing.T) {
	// A cluster's client private key and basic-auth password are both returned
	// inside MasterAuth.
	out := renderDetail(gcp.Resource{Raw: &containerpb.Cluster{
		Name: "prod",
		MasterAuth: &containerpb.MasterAuth{
			Username:             "admin",
			Password:             "hunter2-but-real",
			ClientKey:            "PRIVATE-KEY-MATERIAL",
			ClientCertificate:    "CERT",
			ClusterCaCertificate: "CA-CERT",
		},
	}})

	for _, secret := range []string{"hunter2-but-real", "PRIVATE-KEY-MATERIAL"} {
		if strings.Contains(out, secret) {
			t.Errorf("%q survived into the detail pane:\n%s", secret, out)
		}
	}
	// Certificates are public halves and stay: they are how you recognise which
	// cluster you are looking at.
	if !strings.Contains(out, "CA-CERT") {
		t.Errorf("the CA certificate should not be redacted:\n%s", out)
	}
	if !strings.Contains(out, "admin") {
		t.Errorf("the username should not be redacted:\n%s", out)
	}
}

func TestRedactSecretsMatchesWholeFieldNamesOnly(t *testing.T) {
	// Substring matching would blank out configuration that merely mentions a
	// secret — Cloud SQL's passwordValidationPolicy is real settings worth
	// reading, and holds nothing secret.
	doc := map[string]any{
		"password":                 "gone",
		"shared_secret":            "gone",
		"SharedSecret":             "gone",
		"passwordValidationPolicy": map[string]any{"minLength": float64(12)},
		"secretManagerVersion":     "projects/p/secrets/s/versions/1",
		"tokenBucket":              "keep",
	}

	redactSecrets(doc)

	for _, key := range []string{"password", "shared_secret", "SharedSecret"} {
		if doc[key] != redactedMarker {
			t.Errorf("%s = %v, want it redacted", key, doc[key])
		}
	}
	if policy, ok := doc["passwordValidationPolicy"].(map[string]any); !ok || policy["minLength"] != float64(12) {
		t.Errorf("passwordValidationPolicy was redacted: %v", doc["passwordValidationPolicy"])
	}
	if doc["secretManagerVersion"] != "projects/p/secrets/s/versions/1" {
		t.Errorf("secretManagerVersion was redacted: %v", doc["secretManagerVersion"])
	}
	if doc["tokenBucket"] != "keep" {
		t.Errorf("tokenBucket was redacted: %v", doc["tokenBucket"])
	}
}

func TestRedactSecretsReachesNestedValues(t *testing.T) {
	doc := map[string]any{
		"config": map[string]any{
			"tunnels": []any{
				map[string]any{"name": "a", "sharedSecret": "one"},
				map[string]any{"name": "b", "sharedSecret": "two"},
			},
		},
	}

	redactSecrets(doc)

	tunnels := doc["config"].(map[string]any)["tunnels"].([]any)
	for _, tunnel := range tunnels {
		if got := tunnel.(map[string]any)["sharedSecret"]; got != redactedMarker {
			t.Errorf("nested sharedSecret = %v, want it redacted", got)
		}
	}
}

func TestRedactSecretsLeavesAbsentFieldsAlone(t *testing.T) {
	// Marking an unset field as redacted would misreport the resource: a tunnel
	// with no shared secret is a different thing from one whose secret is
	// hidden.
	doc := map[string]any{"sharedSecret": "", "password": nil}
	redactSecrets(doc)

	if doc["sharedSecret"] != "" {
		t.Errorf("empty sharedSecret = %v, want it left empty", doc["sharedSecret"])
	}
	if doc["password"] != nil {
		t.Errorf("nil password = %v, want it left nil", doc["password"])
	}
}

func TestDetailRendersNonProtoResources(t *testing.T) {
	// Cloud SQL, DNS and Storage hand back plain Go structs rather than
	// protobuf messages, and they go down the same path.
	type bucket struct {
		Name       string `json:"name"`
		Location   string `json:"location"`
		SecretKey  string `json:"secretKey"`
		Versioning bool   `json:"versioning"`
	}

	out := renderDetail(gcp.Resource{Raw: &bucket{
		Name: "data-lake", Location: "US", SecretKey: "leaked", Versioning: true,
	}})

	if !strings.Contains(out, "data-lake") {
		t.Errorf("non-proto resource did not render:\n%s", out)
	}
	if strings.Contains(out, "leaked") {
		t.Errorf("secretKey survived on a non-proto resource:\n%s", out)
	}
}

func TestDetailSurvivesUnrenderableResources(t *testing.T) {
	// A resource that cannot be marshalled must produce a message, not a panic
	// or an empty pane.
	out := renderDetail(gcp.Resource{Raw: func() {}})
	if out == "" {
		t.Error("an unrenderable resource rendered nothing at all")
	}
}

func TestDetailPaneCarriesNoRawEscapes(t *testing.T) {
	// The viewport writes this straight to the terminal, and every value in it
	// came from an API response. The YAML emitter quotes and escapes control
	// characters on its own, so this is belt and braces — and the belt is what
	// keeps holding if the pane ever renders something other than YAML.
	out := renderDetail(gcp.Resource{Raw: &computepb.VpnTunnel{
		Name: proto.String("evil\x1b]0;pwned\x07\x1b[2Jtunnel"),
	}})

	if strings.ContainsAny(out, "\x1b\x07") {
		t.Errorf("raw escape sequences reached the detail pane: %q", out)
	}
	if !strings.Contains(out, "tunnel") {
		t.Errorf("the resource did not render at all: %q", out)
	}
}
