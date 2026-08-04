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

// tokenExchangeUnreachable reports whether gcloud failed because it could not
// open a connection to Google, rather than because of anything about the
// account or the redirect.
//
// gcloud is Python, so this arrives as a urllib3 traceback: a ConnectionError
// wrapping NewConnectionError, or "Max retries exceeded with url: /token".
// Matching on the transport failure rather than on the hostname, because the
// same wall stops the metadata, revocation and token endpoints alike and the
// remedy is identical for all of them.
func (a LoginAttempt) tokenExchangeUnreachable() bool {
	lower := strings.ToLower(a.Output)

	transportFailure := false
	for _, marker := range []string{
		"max retries exceeded",
		"newconnectionerror",
		"connectionerror",
		"proxyerror",
		"failed to establish a new connection",
		"name or service not known",
		"temporary failure in name resolution",
		"network is unreachable",
	} {
		if strings.Contains(lower, marker) {
			transportFailure = true
			break
		}
	}
	if !transportFailure {
		return false
	}

	// Only when it was Google that could not be reached. A connection failure
	// against localhost is the loopback problem and has its own diagnosis, and
	// answering it with proxy advice would send someone to route their loopback
	// through a proxy — the precise opposite of the fix.
	for _, host := range []string{
		"oauth2.googleapis.com",
		"accounts.google.com",
		"googleapis.com",
		"sts.googleapis.com",
		"/token",
	} {
		if strings.Contains(lower, host) {
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
	// gcloud reached the sign-in and then could not reach Google itself.
	//
	// First, ahead of every other case including the interrupt ones, because
	// this failure wears their clothes: the browser says "You are now
	// authenticated", the terminal sits there, and everything about it looks
	// like the loopback problem. It is the opposite. The redirect arrived —
	// that is how gcloud got as far as the token request — and the request it
	// then made to oauth2.googleapis.com never left the machine.
	//
	// Getting this one wrong is expensive: the loopback advice sends someone to
	// press L, and the --no-browser flow performs exactly the same token
	// exchange, so it fails the same way and appears to confirm the account is
	// at fault.
	case contains("certificate verify failed", "sslerror", "ssl: certificate", "self signed certificate",
		"unable to get local issuer"):
		return LoginDiagnosis{
			Summary: "gcloud reached Google but refused its TLS certificate.",
			Remedy:  interceptedTLSRemedy(),
		}, true

	case a.tokenExchangeUnreachable():
		return LoginDiagnosis{
			Summary: "You signed in and the code came back, but gcloud could not reach Google to redeem it.",
			Remedy:  egressRemedy(),
		}, true

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

// egressRemedy is what to do when nothing on this machine can reach Google
// directly.
//
// It leads by ruling out the flow, because the screen this appears on looks
// exactly like the loopback failure and the instinct is to press L. That would
// waste the afternoon: the --no-browser flow redeems the code with the same
// request to the same endpoint, and fails identically.
//
// The environment variables come before gcloud's own proxy settings even
// though gcloud's are more discoverable, because `gcloud config set proxy/…`
// fixes gcloud alone. g9s talks to the Google APIs itself, through Go's HTTP
// client, which reads HTTPS_PROXY and NO_PROXY and knows nothing about gcloud's
// configuration — so the gcloud-only fix produces a login that succeeds
// followed by every table failing, which is a worse place to be than here.
func egressRemedy() []string {
	remedy := []string{
		"This is not the loopback problem, and pressing L will not help: the --no-browser",
		"flow redeems the code with the same request to the same endpoint.",
		"The sign-in worked and the code arrived. What failed is the connection gcloud",
		"then opened to oauth2.googleapis.com — it never left this machine.",
		"",
		"Almost always a corporate proxy that all outbound HTTPS has to go through.",
		"",
		"1. Set the proxy for the shell that runs g9s, and exempt loopback in the same",
		"   breath — g9s reads both of these for its own API calls, so this is the one",
		"   fix that covers gcloud and g9s together:",
		"",
		"     export HTTPS_PROXY=http://YOUR-PROXY:PORT",
		"     export HTTP_PROXY=http://YOUR-PROXY:PORT",
		"     export NO_PROXY=localhost,127.0.0.1,::1,metadata.google.internal",
		"",
		"   NO_PROXY is not optional. Without it the loopback redirect goes to the proxy",
		"   too, and you trade this failure for the one where the sign-in hangs forever.",
	}

	if addr := ProxyAddress(); addr != "" {
		remedy = append(remedy,
			"",
			"   A proxy is already set in this shell ("+addr+"), so either it is not the",
			"   right one for Google, or it requires credentials gcloud was not given.")
	} else {
		remedy = append(remedy,
			"",
			"   No proxy is set in this shell at the moment, which fits: gcloud tried to",
			"   connect directly and nothing answered.")
	}

	return append(remedy,
		"",
		"2. Find the address in your browser's proxy settings, or in the PAC file your",
		"   organization publishes — the browser reached Google, so it knows the way.",
		"3. If the proxy needs authentication, put it in the URL:",
		"     export HTTPS_PROXY=http://USER:PASSWORD@YOUR-PROXY:PORT",
		"4. If none of that is available, run `gcloud auth application-default login` on",
		"   a machine where it completes and point this project at the file it writes",
		"   with `credentials_file` — though note g9s will still need the proxy to call",
		"   the APIs afterwards.",
		"",
		"Run `g9s doctor` to test the connection to Google directly.")
}

// interceptedTLSRemedy is what to do when the connection reaches Google and the
// certificate is not Google's.
//
// A TLS-terminating proxy, which is normal on a corporate network and means
// every HTTPS response arrives signed by the organization's own CA. Distinct
// from egressRemedy because the connection is fine: adding a proxy address
// changes nothing, and the missing piece is trust.
func interceptedTLSRemedy() []string {
	return []string{
		"The connection reached Google and the certificate was signed by something this",
		"machine does not trust — the signature of a proxy that terminates TLS and",
		"re-signs it with your organization's own CA.",
		"",
		"Nothing is wrong with the account, and pressing L will not help: the",
		"--no-browser flow makes the same HTTPS request through the same proxy.",
		"",
		"1. Get your organization's root CA certificate in PEM form. It is usually",
		"   already in the OS trust store — the browser accepted it — and IT can name",
		"   the file.",
		"2. Point gcloud at it:",
		"",
		"     gcloud config set core/custom_ca_certs_file /path/to/corp-ca.pem",
		"",
		"3. g9s calls the Google APIs itself and does not read gcloud's setting, so give",
		"   it the same bundle:",
		"",
		"     export SSL_CERT_FILE=/path/to/corp-ca.pem",
		"",
		"   Without this the login succeeds and every table fails instead.",
		"",
		"Never disable certificate verification to get past this. It would hand every",
		"token and every API response to whatever is on the other end.",
	}
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
