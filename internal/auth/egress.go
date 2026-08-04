package auth

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net/http"
	"strings"
	"time"
)

// TokenEndpoint is where an authorization code is redeemed for a token.
//
// The one host that has to be reachable for any login to complete, whichever
// flow is used — and the one that fails invisibly. The browser prints "You are
// now authenticated with the gcloud CLI!" before gcloud has called it at all,
// so on a machine that cannot reach it the sign-in looks like a success and
// gcloud crashes afterwards.
const TokenEndpoint = "https://oauth2.googleapis.com/token"

// EgressTimeout bounds the reachability probe.
//
// Short, and short on purpose. This runs in front of an interactive login, so
// it is worth a couple of seconds to save a sign-in and an MFA prompt that were
// always going to end in a crash — and not worth more than that.
const EgressTimeout = 6 * time.Second

// EgressState is what a probe of the token endpoint found.
type EgressState int

const (
	// EgressOK means Google answered.
	EgressOK EgressState = iota
	// EgressUnreachable means the connection could not be made. Almost always
	// a proxy that outbound HTTPS has to go through and that nothing here has
	// been told about.
	EgressUnreachable
	// EgressUntrusted means the connection was made and the certificate was
	// not one this machine trusts — a proxy terminating TLS and re-signing
	// with the organization's own CA. A different problem with an opposite
	// remedy: the route is fine and the trust is missing.
	EgressUntrusted
)

// EgressResult is the probe's finding.
type EgressResult struct {
	State EgressState
	// Err is what went wrong, for the message. Nil when State is EgressOK.
	Err error
}

// OK reports whether a login can be expected to complete.
func (r EgressResult) OK() bool { return r.State == EgressOK }

// CheckEgress reports whether this machine can reach Google's token endpoint.
//
// A real request rather than a DNS lookup or a bare TCP dial, because the three
// fail differently and only the full request tells them apart: a proxy that
// resolves, accepts connections, and then refuses CONNECT to Google looks
// perfectly healthy to anything cheaper.
//
// HEAD, which the endpoint will refuse — that is the point. A refusal is proof
// the request arrived, which is the entire question.
func CheckEgress(ctx context.Context) EgressResult {
	ctx, cancel := context.WithTimeout(ctx, EgressTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodHead, TokenEndpoint, nil)
	if err != nil {
		return EgressResult{State: EgressUnreachable, Err: err}
	}

	resp, err := http.DefaultClient.Do(req)
	if err == nil {
		resp.Body.Close()
		return EgressResult{State: EgressOK}
	}
	if IsTLSTrustFailure(err) {
		return EgressResult{State: EgressUntrusted, Err: err}
	}
	return EgressResult{State: EgressUnreachable, Err: err}
}

// IsTLSTrustFailure reports whether a request failed because the certificate
// was not trusted, rather than because the connection was never made.
//
// The distinction decides the remedy, and the two remedies share nothing: one
// needs a proxy address, the other needs a CA bundle.
func IsTLSTrustFailure(err error) bool {
	var unknownAuthority x509.UnknownAuthorityError
	var hostname x509.HostnameError
	var invalid x509.CertificateInvalidError
	if errors.As(err, &unknownAuthority) || errors.As(err, &hostname) || errors.As(err, &invalid) {
		return true
	}
	var verification *tls.CertificateVerificationError
	return errors.As(err, &verification)
}

// EgressDiagnosis turns a failed probe into the same diagnosis a failed login
// would produce.
//
// Shared deliberately. Someone who hits this before signing in and someone who
// hits it after must read the same words, or the second one spends the
// afternoon deciding they are two different problems.
func EgressDiagnosis(r EgressResult) (LoginDiagnosis, bool) {
	switch r.State {
	case EgressUntrusted:
		return LoginDiagnosis{
			Summary: "This machine reaches Google but refuses its TLS certificate.",
			Remedy:  interceptedTLSRemedy(),
		}, true
	case EgressUnreachable:
		return LoginDiagnosis{
			Summary: "This machine cannot reach oauth2.googleapis.com, where the login is completed.",
			Remedy:  append(egressPreflightPreamble(r), egressRemedy()...),
		}, true
	}
	return LoginDiagnosis{}, false
}

// egressPreflightPreamble says why the login was stopped before it started.
//
// Worth its own paragraph: being stopped up front looks like g9s refusing to
// try, and the reason it is a kindness rather than an obstruction is that the
// alternative ends after a sign-in and an MFA prompt, with a crash.
func egressPreflightPreamble(r EgressResult) []string {
	lines := []string{
		"Checked before starting, because this failure is invisible until it is too late:",
		"the browser prints \"You are now authenticated\" before gcloud ever calls this",
		"endpoint, so the sign-in would look like it worked and gcloud would crash after.",
	}
	if r.Err != nil {
		lines = append(lines, "", "  "+firstLine(r.Err.Error()))
	}
	return append(lines, "")
}

// firstLine keeps a multi-line transport error to one line of a report.
func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return strings.TrimSpace(line)
}
