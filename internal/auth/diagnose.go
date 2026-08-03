package auth

import (
	"strings"
)

// LoginDiagnosis is a recognised login failure and what to do about it.
type LoginDiagnosis struct {
	// Summary is one line naming the cause.
	Summary string
	// Remedy is what to actually do, in the order to try it.
	Remedy []string
}

// LoginAttempt is everything known about a login that did not produce a
// credential.
type LoginAttempt struct {
	// Output is what gcloud wrote before it stopped.
	Output string
	// NoBrowser is whether the --no-browser flow was used.
	NoBrowser bool
	// Err is how the process ended.
	Err error
}

// reachedAuthorization reports whether gcloud got as far as asking for a
// sign-in.
//
// This is the difference between "I changed my mind" and "I was stuck": both
// end in a cancelled process, but only one of them has an authorization URL on
// screen. Everything past this point in the flow is waiting on something that
// happens outside this terminal, which is exactly where the two corporate
// failures live.
func (a LoginAttempt) reachedAuthorization() bool {
	lower := strings.ToLower(a.Output)
	for _, marker := range []string{
		"accounts.google.com/o/oauth2/auth",
		"go to the following link in your browser",
		"your browser has been opened to visit",
		"enter the output of the above command",
		"remote-bootstrap",
		"copy its output back here",
		"enter the authorization code",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

// interrupted reports whether the process was stopped rather than failing.
func (a LoginAttempt) interrupted() bool {
	if a.Err != nil {
		// Not matched on syscall values: bubbletea hands back whatever the
		// exec wrapper produced, and the string form is stable across the
		// shapes it can take (signal: interrupt, exit status 130, 1 after
		// gcloud traps the signal itself).
		errText := strings.ToLower(a.Err.Error())
		for _, marker := range []string{"signal: interrupt", "exit status 130", "killed", "signal: terminated"} {
			if strings.Contains(errText, marker) {
				return true
			}
		}
	}
	lower := strings.ToLower(a.Output)
	for _, marker := range []string{"keyboardinterrupt", "aborted by user", "was cancelled", "interrupted"} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

// DiagnoseLogin turns a failed login into a cause and a remedy.
//
// gcloud reports these accurately, but it reports them as an OAuth error from
// Google's side — "invalid_request", "access_denied" — which reads like a
// problem with the account rather than with how the flow was driven. On a
// corporate machine the cause is almost always local: a proxy in front of
// loopback, an org policy on the OAuth client, or the --no-browser command
// being used as though it were a URL.
//
// The two that matter most do not produce an error at all. Both end with
// gcloud waiting: the browser flow waits for a redirect a proxied browser
// never delivers, and the --no-browser flow waits at a paste prompt for output
// the user cannot produce because they opened the URL instead of running the
// command. Neither times out, so the only way out is ctrl+c — which is why a
// cancelled login that had already shown an authorization URL is treated as a
// distinct diagnosis rather than as "you changed your mind".
//
// Returning false means the failure is not one of the known shapes and should
// be shown as-is rather than guessed at.
func DiagnoseLogin(a LoginAttempt) (LoginDiagnosis, bool) {
	lower := strings.ToLower(a.Output)

	contains := func(needles ...string) bool {
		for _, n := range needles {
			if strings.Contains(lower, n) {
				return true
			}
		}
		return false
	}

	switch {
	// The --no-browser trap, named by Google itself. gcloud prints a *command*
	// to run elsewhere; the URL inside it is not a valid authorization request
	// on its own, because the gcloud that runs the command is what appends a
	// redirect_uri pointing at its own loopback. Opening just the URL gets
	// this, and it looks like g9s produced a broken link.
	case contains("missing required parameter", "invalid_request") && contains("redirect_uri"):
		return LoginDiagnosis{
			Summary: "The authorization URL was opened directly instead of running the command gcloud printed.",
			Remedy:  noBrowserRemedy(),
		}, true

	// Cancelled, but only after an authorization URL was on screen. Nothing
	// here failed — the flow stalled at the step that depends on something
	// outside this terminal, and ctrl+c was the only way out.
	case a.interrupted() && a.reachedAuthorization():
		if a.NoBrowser {
			return LoginDiagnosis{
				Summary: "The login was waiting for the output of the command gcloud printed.",
				Remedy: append([]string{
					"gcloud printed a whole `gcloud auth application-default login --remote-bootstrap=...`",
					"command. If you opened the URL inside it in a browser and got",
					"\"missing required parameter: redirect_uri\", that is this flow being misread:",
					"the URL alone is not a valid authorization request.",
					"",
				}, noBrowserRemedy()...),
			}, true
		}
		return LoginDiagnosis{
			Summary: "You signed in, but the authorization code never came back to this machine.",
			Remedy:  loopbackRemedy(),
		}, true

	// Same family as the first case, but gcloud did not name the parameter.
	case contains("missing required parameter", "invalid_request"):
		remedy := []string{
			"Google rejected the authorization request as incomplete, which usually means the",
			"request was not the one gcloud built.",
		}
		if a.NoBrowser {
			remedy = append(remedy,
				"In the --no-browser flow, run the whole command gcloud printed — do not open",
				"the URL inside it in a browser.")
		} else {
			remedy = append(remedy, "Try pressing L to use the --no-browser flow instead.")
		}
		return LoginDiagnosis{Summary: "Google rejected the authorization request as missing a parameter.", Remedy: remedy}, true

	// Org policy on the OAuth client, common on managed corporate identities.
	case contains("admin_policy_enforced", "org_internal", "disallowed_useragent", "blocked by your administrator", "access_denied"):
		return LoginDiagnosis{
			Summary: "Your organization's policy refused this sign-in.",
			Remedy: []string{
				"Some organizations block the default gcloud OAuth client for application-default",
				"login, or restrict which accounts may use it.",
				"Ask whether ADC login is permitted for this account, and whether your organization",
				"publishes its own client ID for it.",
				"A service account key or workload identity federation avoids the interactive flow",
				"entirely — point the project at it with `credentials_file` in the config.",
			},
		}, true

	// The browser signed in but the code never came back, and this time gcloud
	// said so rather than waiting.
	case contains("connection refused", "failed to connect to localhost", "could not reach", "timed out waiting"):
		return LoginDiagnosis{
			Summary: "The browser could not hand the authorization code back.",
			Remedy:  loopbackRemedy(),
		}, true

	case a.interrupted():
		return LoginDiagnosis{
			Summary: "The login was cancelled before it finished.",
			Remedy:  []string{"Press l to try again, or L to log in without a browser."},
		}, true

	case contains("command not found", "executable file not found", "no such file or directory") && contains("gcloud"):
		return LoginDiagnosis{
			Summary: "gcloud could not be run.",
			Remedy: []string{
				"Check that gcloud is on your PATH, or set defaults.gcloud_path to its full path.",
				"Run `g9s doctor` to check the rest of the setup at the same time.",
			},
		}, true
	}

	return LoginDiagnosis{}, false
}

// loopbackRemedy is what to do when the sign-in succeeded and the redirect did
// not arrive.
//
// Ordered by what actually fixes it rather than by effort. The browser's own
// proxy settings are the cause and the first item is the only fix that keeps
// the one-machine flow working; everything below it is a way around.
func loopbackRemedy() []string {
	remedy := []string{
		"The last step is your browser fetching http://localhost:<port>/ on THIS machine.",
		"Nothing is wrong with the account — the sign-in worked and the reply went elsewhere.",
		"",
		"1. Exempt loopback in the BROWSER's proxy settings (not just the shell):",
		"   localhost, 127.0.0.1, ::1. This is the fix that keeps everything on one machine.",
	}
	if ProxyMayBlockLoopback() {
		remedy = append(remedy,
			"   A proxy is configured in this shell and does not exempt loopback, so the",
			"   browser is likely configured the same way.")
	}
	return append(remedy,
		"2. Add localhost,127.0.0.1,::1 to NO_PROXY for the shell that runs g9s.",
		"3. If the browser's proxy cannot be changed, press L for the --no-browser flow and",
		"   run the command it prints on a machine whose browser can reach its own localhost.",
		"4. Last resort: run `gcloud auth application-default login` on any machine where it",
		"   completes, and point this project at the file it writes with `credentials_file`.",
		"",
		"Run `g9s doctor` to check the proxy and loopback situation directly.")
}

// noBrowserRemedy is what to do with the command gcloud prints in the
// --no-browser flow.
//
// Spelled out because the mistake it prevents is the natural one: the command
// contains a long https:// URL, and a URL in a terminal is something you copy
// into a browser.
func noBrowserRemedy() []string {
	return []string{
		"Copy the ENTIRE line starting with `gcloud auth application-default login`,",
		"including the --remote-bootstrap=\"...\" part. It is a command, not a link.",
		"",
		"1. Run that whole command in a terminal on a machine that has a browser and",
		"   gcloud 372.0.0 or newer. That machine's browser must be able to reach its",
		"   own localhost — the bootstrap does the loopback step there instead.",
		"2. It prints a long token. Paste THAT back into g9s at the prompt.",
		"",
		"If no other machine has gcloud, or its browser is proxied the same way, run a",
		"normal `gcloud auth application-default login` wherever it does complete and",
		"copy the credentials file it writes, then set `credentials_file` for this project.",
	}
}
