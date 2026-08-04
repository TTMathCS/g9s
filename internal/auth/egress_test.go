package auth

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// The two failures need opposite remedies — one wants a proxy address, the
// other a CA certificate — so telling them apart is the whole value of the
// probe. A certificate signed by an unknown authority is what a TLS-terminating
// corporate proxy produces.
func TestTLSTrustFailuresAreDistinguishedFromUnreachableHosts(t *testing.T) {
	trust := []error{
		x509.UnknownAuthorityError{},
		x509.HostnameError{Host: "oauth2.googleapis.com"},
		x509.CertificateInvalidError{Reason: x509.Expired},
		&tls.CertificateVerificationError{Err: x509.UnknownAuthorityError{}},
		fmt.Errorf("get %q: %w", TokenEndpoint, x509.UnknownAuthorityError{}),
	}
	for _, err := range trust {
		if !IsTLSTrustFailure(err) {
			t.Errorf("%T was not recognised as a certificate problem", err)
		}
	}

	unreachable := []error{
		errors.New("dial tcp 142.250.1.95:443: connect: connection refused"),
		errors.New("proxyconnect tcp: dial tcp: lookup proxy.corp: no such host"),
		context.DeadlineExceeded,
	}
	for _, err := range unreachable {
		if IsTLSTrustFailure(err) {
			t.Errorf("%v was misread as a certificate problem", err)
		}
	}
}

// The probe runs in front of an interactive login, so it is worth a couple of
// seconds to save a sign-in and an MFA prompt that were always going to end in
// a crash — and not worth more than that.
func TestTheEgressProbeIsBounded(t *testing.T) {
	if EgressTimeout <= 0 || EgressTimeout > 10*time.Second {
		t.Errorf("EgressTimeout = %v, want a short positive bound", EgressTimeout)
	}
}

// An already-cancelled context must come back rather than block, since the
// login is waiting on this before it does anything.
func TestTheProbeRespectsACancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	done := make(chan EgressResult, 1)
	go func() { done <- CheckEgress(ctx) }()

	select {
	case result := <-done:
		if result.OK() {
			t.Error("a cancelled probe reported the endpoint reachable")
		}
	case <-time.After(EgressTimeout + 2*time.Second):
		t.Fatal("the probe ignored a cancelled context")
	}
}

// Someone stopped before signing in and someone who crashed after must read the
// same words, or the second one spends the afternoon deciding they are two
// different problems.
func TestThePreflightAndThePostMortemGiveTheSameRemedy(t *testing.T) {
	preflight, ok := EgressDiagnosis(EgressResult{
		State: EgressUnreachable,
		Err:   errors.New("dial tcp 142.250.1.95:443: connect: connection refused"),
	})
	if !ok {
		t.Fatal("an unreachable endpoint produced no diagnosis")
	}

	postMortem, ok := DiagnoseLogin(LoginAttempt{
		Output: gcloudEgressBlockedOutput,
		Err:    errors.New("exit status 1"),
	})
	if !ok {
		t.Fatal("the crash transcript produced no diagnosis")
	}

	pre := strings.Join(preflight.Remedy, "\n")
	post := strings.Join(postMortem.Remedy, "\n")
	if !strings.HasSuffix(pre, post) {
		t.Errorf("the pre-flight remedy is not the post-mortem one with a preamble:\npre:\n%s\n\npost:\n%s", pre, post)
	}

	// And the pre-flight has to say why it stopped before trying, or being
	// stopped reads as g9s refusing rather than as a saved round trip.
	if !strings.Contains(pre, "Checked before starting") {
		t.Errorf("the pre-flight does not explain itself:\n%s", pre)
	}
	if !strings.Contains(pre, "connection refused") {
		t.Errorf("the pre-flight does not quote what actually failed:\n%s", pre)
	}
}

func TestAReachableEndpointProducesNoDiagnosis(t *testing.T) {
	if _, ok := EgressDiagnosis(EgressResult{State: EgressOK}); ok {
		t.Error("a healthy probe produced a diagnosis")
	}
}

// TLS interception routes to the certificate remedy, not the proxy one.
func TestAnUntrustedCertificatePreflightAsksForACABundle(t *testing.T) {
	diag, ok := EgressDiagnosis(EgressResult{
		State: EgressUntrusted,
		Err:   x509.UnknownAuthorityError{},
	})
	if !ok {
		t.Fatal("an untrusted certificate produced no diagnosis")
	}
	remedy := strings.ToLower(strings.Join(diag.Remedy, "\n"))
	if !strings.Contains(remedy, "custom_ca_certs_file") {
		t.Errorf("the remedy does not name the CA setting:\n%s", remedy)
	}
	if strings.Contains(remedy, "export https_proxy") {
		t.Errorf("a trust problem was told to configure a proxy:\n%s", remedy)
	}
}
