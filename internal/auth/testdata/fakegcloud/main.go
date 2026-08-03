// Command fakegcloud imitates `gcloud auth application-default login
// --no-launch-browser` closely enough to exercise the assisted login end to
// end: it prints the authorization banner with a real loopback redirect_uri,
// serves that loopback listener, and exits by what arrives on it.
//
// It is a test fixture, compiled by the auth package's tests. The environment
// variable FAKEGCLOUD_MODE selects the failure to imitate:
//
//	""            print banner, listen, exit 0 when a code arrives
//	"no-url"      exit 1 without printing a URL
//	"hang-banner" print prose but never the URL, then sleep
package main

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"time"
)

func main() {
	switch os.Getenv("FAKEGCLOUD_MODE") {
	case "no-url":
		fmt.Fprintln(os.Stderr, "ERROR: (gcloud.auth.application-default.login) something broke early")
		os.Exit(1)
	case "hang-banner":
		fmt.Println("You are authorizing client libraries without access to a web browser.")
		time.Sleep(time.Minute)
		os.Exit(1)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	port := ln.Addr().(*net.TCPAddr).Port

	// The real banner, shape and all: URL on its own indented line, the
	// redirect_uri percent-encoded inside the query string.
	fmt.Printf("Go to the following link in your browser, and complete the sign-in prompts:\n\n")
	fmt.Printf("    https://accounts.google.com/o/oauth2/auth?response_type=code&client_id=fake.apps.googleusercontent.com&redirect_uri=http%%3A%%2F%%2Flocalhost%%3A%d%%2F&scope=openid&state=fake-state&code_challenge=fake&code_challenge_method=S256\n\n", port)

	got := make(chan string, 1)
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "You may close this window.")
		got <- r.URL.Query().Get("code")
	})}
	go srv.Serve(ln)

	select {
	case code := <-got:
		if code == "" || code == "bad" {
			fmt.Fprintln(os.Stderr, "ERROR: invalid authorization code")
			os.Exit(1)
		}
		// Where the real gcloud would exchange the code and write ADC.
		fmt.Println("\nCredentials saved to file: [" + os.Getenv("CLOUDSDK_CONFIG") + "/application_default_credentials.json]")
		os.Exit(0)
	case <-time.After(time.Minute):
		os.Exit(1)
	}
}
