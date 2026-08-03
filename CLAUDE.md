# g9s — working agreements

## Absolute rule: nothing is ever installed, built or run on the user's machine

**Never** instruct the user to install Go, download a module, run `go build`,
`go install`, `go test`, `go run`, or fetch any dependency on their laptop.
Never suggest a workflow whose first step is a toolchain. This is a standing
security requirement, not a preference, and it does not expire.

The only thing that ever reaches the user's machine is a **prebuilt binary from
a GitHub release**, which CI builds, tests, vulnerability-scans and attests.
That release pipeline exists specifically to honour this rule — see
`.github/workflows/ci.yml`. Every merge to `main` publishes one automatically.

What the user's machine is allowed to have:

- the prebuilt `g9s` binary from https://github.com/TTMathCS/g9s/releases
- the **gcloud CLI**, which is a documented prerequisite and which g9s shells
  out to for login and SSH. g9s runs the gcloud already installed there; it
  never installs, updates or downloads it.

Building, testing and dependency resolution happen **only** in the remote
Claude Code container, which is isolated from the user's machine and discarded
after the session. Anything downloaded there never touches their laptop — but
say so plainly when it could look otherwise, rather than leaving it ambiguous.

When a feature would need something new on the user's side, the answer is to
ship it inside the binary, not to add a prerequisite.

## Context

- The user runs g9s from a **corporate laptop behind a proxy that intercepts
  localhost**, which is why the assisted login flow exists (`internal/auth/
  assisted.go`). Their browser login hangs and the `--no-browser` flow's URL
  produces `missing required parameter: redirect_uri` when opened directly.
- g9s is **read-only**. Every GCP call is a list or get; no mutation is
  implemented, and adding one is a deliberate product decision, not a
  refactor.
- Secrets are never displayed. `PERMISSIONS.md` must never ask for
  `secretmanager.versions.access` or `roles/secretmanager.secretAccessor`;
  a test enforces this.

## Repository conventions

- Documentation is tied to code by tests: kind counts in `README.md`, every
  kind in `PERMISSIONS.md`, and every bound key in the help panel. Adding a
  lister or a keybinding without documenting it fails the build.
- Warnings are typed (`gcp.Warning`), never bare strings. Displayed text is
  derived from the type so the sentence and the classification cannot drift.
- Every goroutine g9s starts itself is wrapped in `safely()`. A panic that
  escapes one kills the process and strands the terminal in raw mode.
