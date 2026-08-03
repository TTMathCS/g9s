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
