package auth

import (
	"errors"
	"strings"
	"testing"
)

// The two failures below are the ones reported from a corporate laptop, and
// they are the reason this package exists. Neither produces an error from
// gcloud: both end with gcloud waiting on something that happens outside the
// terminal, so the only way out is ctrl+c. A diagnosis that reads a cancelled
// process as "you changed your mind" gives exactly the wrong advice for both.

// gcloudBrowserOutput is what the ordinary flow leaves on screen when the
// sign-in succeeds and the loopback redirect never arrives.
const gcloudBrowserOutput = `Your browser has been opened to visit:

    https://accounts.google.com/o/oauth2/auth?response_type=code&client_id=764086051850-6qr4p6gpi6hn506pt8ejuq83di341hur.apps.googleusercontent.com&redirect_uri=http%3A%2F%2Flocalhost%3A8085%2F&scope=openid+email&state=abc&access_type=offline&code_challenge=xyz&code_challenge_method=S256

`

// gcloudNoBrowserOutput is the --no-browser flow sitting at its paste prompt.
const gcloudNoBrowserOutput = `You are authorizing client libraries without access to a web browser. Please run the following command on a machine with a web browser and copy its output back here. Make sure the installed gcloud version is 372.0.0 or newer.

gcloud auth application-default login --remote-bootstrap="https://accounts.google.com/o/oauth2/auth?response_type=code&client_id=764086051850-6qr4p6gpi6hn506pt8ejuq83di341hur.apps.googleusercontent.com&scope=openid+email&state=abc&access_type=offline&code_challenge=xyz&code_challenge_method=S256&token_usage=remote"

Enter the output of the above command: `

func TestBrowserLoginThatHungIsNotReportedAsCancelled(t *testing.T) {
	diag, ok := DiagnoseLogin(LoginAttempt{
		Output:    gcloudBrowserOutput,
		NoBrowser: false,
		Err:       errors.New("signal: interrupt"),
	})
	if !ok {
		t.Fatal("a browser login interrupted at the authorization step was not recognised")
	}

	// The distinction that matters: the account is fine and retrying the same
	// way will hang the same way.
	if strings.Contains(strings.ToLower(diag.Summary), "cancelled") {
		t.Errorf("reported as a cancellation, which invites an identical retry:\n%s", diag.Summary)
	}
	remedy := strings.Join(diag.Remedy, "\n")
	if !strings.Contains(remedy, "localhost") {
		t.Errorf("remedy never mentions loopback, which is the actual cause:\n%s", remedy)
	}
	if !strings.Contains(strings.ToLower(remedy), "browser") {
		t.Errorf("remedy does not point at the browser's own proxy settings:\n%s", remedy)
	}
}

func TestNoBrowserLoginThatHungExplainsTheRedirectURIError(t *testing.T) {
	diag, ok := DiagnoseLogin(LoginAttempt{
		Output:    gcloudNoBrowserOutput,
		NoBrowser: true,
		Err:       errors.New("signal: interrupt"),
	})
	if !ok {
		t.Fatal("a --no-browser login interrupted at the paste prompt was not recognised")
	}

	remedy := strings.Join(diag.Remedy, "\n")
	// Someone who got here has already seen Google's 400 page and needs to be
	// told that the error was theirs to avoid, not g9s producing a bad link.
	if !strings.Contains(remedy, "redirect_uri") {
		t.Errorf("remedy does not connect the hang to the 400 the user already saw:\n%s", remedy)
	}
	if !strings.Contains(remedy, "command, not a link") {
		t.Errorf("remedy does not name the actual mistake:\n%s", remedy)
	}
}

func TestRedirectURIErrorIsDiagnosedFromGooglesOwnWording(t *testing.T) {
	// The case where gcloud does surface the error rather than waiting.
	diag, ok := DiagnoseLogin(LoginAttempt{
		Output:    "Error 400: invalid_request\nmissing required parameter: redirect_uri",
		NoBrowser: true,
	})
	if !ok {
		t.Fatal("Google's own wording for the --no-browser trap was not recognised")
	}
	if !strings.Contains(diag.Summary, "instead of running the command") {
		t.Errorf("summary does not name the cause:\n%s", diag.Summary)
	}
}

func TestCancelBeforeSigningInStaysASimpleCancel(t *testing.T) {
	// No authorization URL was ever shown, so nothing stalled — this really is
	// someone changing their mind, and "press l to try again" is right here.
	diag, ok := DiagnoseLogin(LoginAttempt{
		Output: "Credentials saved to file: []\n",
		Err:    errors.New("signal: interrupt"),
	})
	if !ok {
		t.Fatal("a plain cancellation was not recognised")
	}
	if !strings.Contains(diag.Summary, "cancelled") {
		t.Errorf("a cancel with no authorization step should read as a cancel:\n%s", diag.Summary)
	}
}

func TestUnrecognisedFailureIsNotGuessedAt(t *testing.T) {
	if _, ok := DiagnoseLogin(LoginAttempt{
		Output: "ERROR: (gcloud.auth.application-default.login) Something entirely new went wrong.",
		Err:    errors.New("exit status 1"),
	}); ok {
		t.Error("an unknown failure was matched to a diagnosis; it should be shown verbatim instead")
	}
}

func TestOrgPolicyRefusalIsDistinctFromAMisdrivenFlow(t *testing.T) {
	diag, ok := DiagnoseLogin(LoginAttempt{
		Output: "Error 403: admin_policy_enforced",
	})
	if !ok {
		t.Fatal("an org policy refusal was not recognised")
	}
	// Retrying differently cannot fix this one, so the remedy has to leave the
	// tool rather than suggest another flow.
	if !strings.Contains(strings.Join(diag.Remedy, "\n"), "credentials_file") {
		t.Errorf("remedy offers no path that avoids the interactive flow:\n%v", diag.Remedy)
	}
}

func TestInterruptIsRecognisedAcrossExitShapes(t *testing.T) {
	// bubbletea hands back whatever the exec wrapper produced, and gcloud may
	// trap the signal itself, so the exit shape varies by platform and version.
	for _, err := range []error{
		errors.New("signal: interrupt"),
		errors.New("exit status 130"),
		errors.New("signal: terminated"),
	} {
		attempt := LoginAttempt{Output: gcloudBrowserOutput, Err: err}
		if !attempt.interrupted() {
			t.Errorf("%v was not recognised as an interrupt", err)
		}
	}

	// gcloud's own report of it, when the process exits 1 instead.
	attempt := LoginAttempt{
		Output: gcloudBrowserOutput + "\nKeyboardInterrupt\n",
		Err:    errors.New("exit status 1"),
	}
	if !attempt.interrupted() {
		t.Error("a KeyboardInterrupt in the output was not recognised as an interrupt")
	}
}

// gcloudEgressBlockedOutput is the failure reported from the corporate laptop
// after the loopback problem was fixed, transcribed from the terminal.
//
// It is the most misleading shape in this file. The browser shows "You are now
// authenticated with the gcloud CLI!", the sign-in genuinely worked, and the
// redirect genuinely arrived — gcloud got far enough to ask for a token. What
// failed is the connection to Google, and every remedy for the loopback problem
// makes it worse.
const gcloudEgressBlockedOutput = `Your browser has been opened to visit:

    https://accounts.google.com/o/oauth2/auth?response_type=code&client_id=764086051850-6qr4p6gpi6hn506pt8ejuq83di341hur.apps.googleusercontent.com&redirect_uri=http%3A%2F%2Flocalhost%3A8085%2F&scope=openid+email&state=abc

ERROR: gcloud crashed (ConnectionError): HTTPSConnectionPool(host='oauth2.googleapis.com', port=443): Max retries exceeded with url: /token (Caused by NewConnectionError('<urllib3.connection.HTTPSConnection object at 0x10a1b2c40>: Failed to establish a new connection: [Errno 61] Connection refused'))

If you would like to report this issue, please run the following command:
  gcloud feedback
`

// The whole point of the new case. This transcript used to fall through to
// "unrecognised", and the screen it produced sent the reader to press L — which
// performs the same token exchange and fails identically.
func TestBlockedEgressIsNotMistakenForTheLoopbackProblem(t *testing.T) {
	diag, ok := DiagnoseLogin(LoginAttempt{
		Output: gcloudEgressBlockedOutput,
		Err:    errors.New("exit status 1"),
	})
	if !ok {
		t.Fatal("a gcloud that crashed reaching oauth2.googleapis.com is still unrecognised")
	}

	remedy := strings.ToLower(strings.Join(diag.Remedy, "\n"))
	if !strings.Contains(remedy, "https_proxy") {
		t.Errorf("the remedy never mentions the proxy:\n%s", remedy)
	}
	if !strings.Contains(remedy, "no_proxy") {
		t.Error("the remedy sets a proxy without exempting loopback, which trades one failure for the other")
	}

	// It must say outright that L is the wrong move, because the screen looks
	// exactly like the failure where L is the right move.
	if !strings.Contains(remedy, "not help") && !strings.Contains(remedy, "will not") {
		t.Errorf("the remedy does not rule out the --no-browser flow:\n%s", remedy)
	}

	// And it must not repeat the loopback advice, which is what the reader
	// would otherwise act on first.
	if strings.Contains(remedy, "exempt loopback in the browser") {
		t.Errorf("blocked egress was given the loopback remedy:\n%s", remedy)
	}
}

// Same transcript, but the user hit ctrl+c first. The interrupt cases sit
// lower in the switch and would otherwise claim the code never came back.
func TestBlockedEgressWinsOverTheInterruptDiagnosis(t *testing.T) {
	diag, ok := DiagnoseLogin(LoginAttempt{
		Output: gcloudEgressBlockedOutput,
		Err:    errors.New("signal: interrupt"),
	})
	if !ok {
		t.Fatal("not diagnosed")
	}
	if strings.Contains(diag.Summary, "never came back") {
		t.Errorf("summary = %q — the code did come back; redeeming it is what failed", diag.Summary)
	}
}

// A connection failure against localhost is the loopback problem. Answering it
// with proxy advice would tell someone to route their own loopback through a
// proxy, which is the precise opposite of the fix.
func TestALoopbackConnectionFailureIsStillTheLoopbackProblem(t *testing.T) {
	out := gcloudBrowserOutput + `
ERROR: Failed to connect to localhost port 8085: Connection refused
`
	diag, ok := DiagnoseLogin(LoginAttempt{Output: out, Err: errors.New("exit status 1")})
	if !ok {
		t.Fatal("not diagnosed")
	}
	remedy := strings.ToLower(strings.Join(diag.Remedy, "\n"))
	if strings.Contains(remedy, "export https_proxy") {
		t.Errorf("a loopback failure was told to configure a proxy:\n%s", remedy)
	}
	if !strings.Contains(remedy, "loopback") {
		t.Errorf("remedy is not the loopback one:\n%s", remedy)
	}
}

// A TLS-terminating proxy is a different problem from an unreachable one: the
// connection is fine and the missing piece is trust, so a proxy address fixes
// nothing.
func TestInterceptedTLSAsksForACertificateNotAProxy(t *testing.T) {
	out := gcloudBrowserOutput + `
ERROR: gcloud crashed (SSLError): HTTPSConnectionPool(host='oauth2.googleapis.com', port=443): Max retries exceeded with url: /token (Caused by SSLError(SSLCertVerificationError(1, '[SSL: CERTIFICATE_VERIFY_FAILED] certificate verify failed: unable to get local issuer certificate (_ssl.c:1006)')))
`
	diag, ok := DiagnoseLogin(LoginAttempt{Output: out, Err: errors.New("exit status 1")})
	if !ok {
		t.Fatal("a TLS verification failure is unrecognised")
	}

	remedy := strings.ToLower(strings.Join(diag.Remedy, "\n"))
	if !strings.Contains(remedy, "custom_ca_certs_file") {
		t.Errorf("the remedy never names gcloud's CA setting:\n%s", remedy)
	}
	if !strings.Contains(remedy, "ssl_cert_file") {
		t.Error("the remedy fixes gcloud and leaves g9s failing every table afterwards")
	}
	// g9s must never suggest turning verification off to get past a proxy —
	// that would hand every token and every API response to whatever is
	// terminating the connection. Naming it in order to forbid it is the point,
	// so the check is for a recommendation rather than for the words.
	for _, forbidden := range []string{
		"--no-verify",
		"insecureskipverify",
		"gcloud config set auth/disable_ssl_validation",
		"curl -k",
		"pythonhttpsverify=0",
	} {
		if strings.Contains(remedy, forbidden) {
			t.Errorf("the remedy suggests weakening TLS: %q", forbidden)
		}
	}
	if !strings.Contains(remedy, "never disable certificate verification") {
		t.Errorf("the remedy does not warn against turning verification off, which is what someone will reach for next:\n%s", remedy)
	}
}

// The remedy prints the configured proxy so "none set" and "that one did not
// work" are distinguishable. A password in it must not reach the screen.
func TestTheProxyAddressIsShownWithoutItsPassword(t *testing.T) {
	cases := map[string]string{
		"http://proxy.corp:3128":                "http://proxy.corp:3128",
		"http://alice:s3cr3t@proxy.corp:3128":   "s3cr3t",
		"https://dom%5Cuser:pw@proxy.corp:8080": "pw",
		"proxy.corp:3128":                       "proxy.corp:3128",
	}
	for raw, expectation := range cases {
		got := proxyAddress(func(key string) string {
			if key == "HTTPS_PROXY" {
				return raw
			}
			return ""
		})
		if got == "" {
			t.Errorf("proxyAddress(%q) returned nothing", raw)
			continue
		}
		if strings.Contains(raw, "@") {
			// expectation is the secret that must be gone.
			if strings.Contains(got, expectation) {
				t.Errorf("proxyAddress(%q) = %q, which still carries the password", raw, got)
			}
			continue
		}
		if !strings.Contains(got, expectation) {
			t.Errorf("proxyAddress(%q) = %q, want it to name the proxy", raw, got)
		}
	}

	if got := proxyAddress(func(string) string { return "" }); got != "" {
		t.Errorf("proxyAddress with nothing set = %q", got)
	}
}
