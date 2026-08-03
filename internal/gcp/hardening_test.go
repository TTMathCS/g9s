package gcp

import (
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	"cloud.google.com/go/compute/apiv1/computepb"
	"google.golang.org/api/sqladmin/v1"
	"google.golang.org/protobuf/proto"
)

func TestClipCutsOnRuneBoundaries(t *testing.T) {
	// Slicing a byte offset lands in the middle of a multi-byte character, and
	// the replacement characters that produces read as a corrupt response.
	for _, text := range []string{
		strings.Repeat("é", 300),
		strings.Repeat("日", 300),
		strings.Repeat("x", 300),
		strings.Repeat("🔥", 300),
	} {
		got := clip(text, 100)
		if !utf8.ValidString(got) {
			t.Errorf("clip produced invalid UTF-8: %q", got)
		}
		if n := len([]rune(got)); n > 100 {
			t.Errorf("clip returned %d runes, want at most 100", n)
		}
	}
}

func TestClipLeavesShortTextAlone(t *testing.T) {
	if got := clip("us-central1: permission denied", 120); got != "us-central1: permission denied" {
		t.Errorf("clip rewrote a short string: %q", got)
	}
	if got := clip("anything", 0); got != "" {
		t.Errorf("clip(_, 0) = %q, want empty", got)
	}
}

func TestWarningsStayValidUTF8(t *testing.T) {
	// The end-to-end version: both places that shorten an API message have to
	// survive a non-ASCII one, because both feed straight into the status bar.
	long := errors.New(strings.Repeat("é", 500))
	if got, _ := describeFailure("us-central1", long); !utf8.ValidString(got.String()) {
		t.Errorf("describeFailure produced invalid UTF-8: %q", got)
	}

	w := &sqladmin.ApiWarning{Region: "us-east4", Message: strings.Repeat("é", 500)}
	if got, _ := sqlWarning(w); !utf8.ValidString(got.String()) {
		t.Errorf("sqlWarning produced invalid UTF-8: %q", got)
	}
}

func TestSSHTargetAcceptsARealInstance(t *testing.T) {
	name, zone, ok := SSHTarget(Resource{Raw: &computepb.Instance{
		Name:   proto.String("web-01"),
		Zone:   proto.String("https://www.googleapis.com/compute/v1/projects/p/zones/us-central1-a"),
		Status: proto.String("RUNNING"),
	}})

	if !ok {
		t.Fatal("a running instance is not an ssh target")
	}
	if name != "web-01" || zone != "us-central1-a" {
		t.Errorf("SSHTarget = (%q, %q), want (web-01, us-central1-a)", name, zone)
	}
}

func TestSSHTargetRefusesNamesThatCouldPassForArguments(t *testing.T) {
	// Both values become argv for `gcloud compute ssh`. GCP will not mint a
	// name like these; this is the guard for the day something upstream returns
	// one anyway, before it reaches a command line.
	for _, tt := range []struct{ name, zone string }{
		{"--command=rm -rf /", "us-central1-a"},
		{"-web-01", "us-central1-a"},
		{"web 01", "us-central1-a"},
		{"web;reboot", "us-central1-a"},
		{"web$(id)", "us-central1-a"},
		{"web\nnext", "us-central1-a"},
		{"", "us-central1-a"},
		{"web-01", "--zone=elsewhere"},
		{"web-01", "us central1"},
		{"web-01", ""},
		{"web-01", strings.Repeat("z", 200)},
	} {
		_, _, ok := SSHTarget(Resource{Raw: &computepb.Instance{
			Name:   proto.String(tt.name),
			Zone:   proto.String("https://example/zones/" + tt.zone),
			Status: proto.String("RUNNING"),
		}})
		if ok {
			t.Errorf("SSHTarget accepted name=%q zone=%q", tt.name, tt.zone)
		}
	}
}

func TestSSHTargetStillRefusesStoppedInstances(t *testing.T) {
	_, _, ok := SSHTarget(Resource{Raw: &computepb.Instance{
		Name:   proto.String("web-01"),
		Zone:   proto.String("https://example/zones/us-central1-a"),
		Status: proto.String("TERMINATED"),
	}})
	if ok {
		t.Error("a terminated instance is an ssh target")
	}
}
