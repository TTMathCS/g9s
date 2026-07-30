# Security

## What g9s does with your credentials

**It never handles your password, and it never writes a credential.** Pressing
`l` suspends the TUI and hands the terminal to `gcloud auth application-default
login`. Your identity provider's login page, the password from your PAM
checkout and the MFA challenge all happen in the browser and in gcloud's own
process. g9s resumes afterwards and reads the file gcloud wrote.

**Each project is isolated.** g9s sets `CLOUDSDK_CONFIG` to a per-project
directory under `credential_dir`, created `0700`. Logging into one project
cannot disturb another, and nothing g9s does mutates your global
`~/.config/gcloud`.

**Credentials are used, not stored.** g9s reads the application default
credentials file to mint an access token — that live token exchange is also how
expiry is detected. Tokens stay in memory for the life of the process.

## What it can reach and run

| | |
|---|---|
| **Network** | GCP APIs only, over the Google client libraries. No telemetry, no analytics, no update check, no other host. |
| **Executes** | `gcloud` (path from `defaults.gcloud_path`) for login and SSH; the platform opener (`open` / `xdg-open` / `rundll32`) for `o` and `c`. Nothing else. |
| **Writes** | The per-project credential directories (via gcloud), and `config.yaml` on `-init`. |
| **API calls** | Read-only. Every call is a `List` or `Get`; g9s issues no mutating request of any kind. |

The clipboard (`y`) uses the OSC 52 terminal escape rather than a platform
clipboard binary. The payload is base64-encoded, so contents cannot break out
of the escape sequence.

## Review findings

A review of the whole codebase in July 2026. Two issues were found and fixed;
the rest is recorded so the reasoning is visible rather than implied.

### Fixed

**Untrusted URI reaching the platform opener.** Console links are built by g9s
from a fixed `https://` prefix with escaped components, but a Composer
environment's Airflow URI comes straight out of the API response. On macOS
`open` launches whichever application claims a scheme, so a surprising value in
an API response could have turned `o` into "launch an arbitrary handler".
`openURL` now refuses anything that is not `http`/`https` with a host, and
shows the URL instead of opening it. Covered by tests, including one asserting
that g9s's own Console URLs still pass the guard.

**Credential-directory collisions were possible.** Sanitizing project names
for the filesystem can collapse two distinct names into one directory —
`prod/data` and `prod-data` both become `prod-data` — and sharing a credential
directory means logging into one project silently re-identifies the other.
Startup now refuses a config whose project names collide after sanitizing,
naming both projects. Found in a follow-up review, July 2026.

**World-writable config was accepted.** `defaults.gcloud_path` names the binary
g9s executes, so write access to the config file is code execution as you.
`-init` now writes `0600` instead of `0644`, and `config.Load` refuses a file
that is group- or world-**writable**, naming the `chmod` that fixes it.
Readable-by-others is still allowed — that is the default umask in plenty of
places and the contents are not secret.

### Examined, not a problem

- **Command injection.** Every `exec.Command` passes arguments as separate
  argv entries; no shell is involved anywhere, so metacharacters in a resource
  name cannot become commands.
- **Argument injection into gcloud.** Instance and zone names flow into
  `gcloud compute ssh` from the API. GCP's own naming rules (`[a-z]([-a-z0-9]*
  [a-z0-9])?`) forbid a leading `-`, so a name cannot pose as a flag.
- **Path traversal into the credential directory.** Project names come from a
  hand-edited file and become directory components. `sanitize` splits on
  anything outside `[A-Za-z0-9._-]` and drops dot-only segments, so `..` and
  path separators cannot survive in any form. Tested directly.
- **Config parsing.** The decoder runs with `KnownFields(true)`, so a typo is
  an error rather than a silent default. The input is your own local file, not
  attacker-controlled.
- **Clipboard escape injection.** OSC 52 payloads are base64, so no content can
  terminate the sequence early.
- **Credential file handling.** g9s only reads the ADC file; gcloud creates it,
  inside a directory g9s creates `0700`.

### Residual, by design

- **The detail pane renders the full API object.** That is the point — it is
  what `gcloud describe` shows you — but it means whatever GCP returns about a
  resource is on screen, and `y` copies it to the clipboard. Worth knowing
  before you screen-share.
- **OSC 52 writes to stderr.** If you redirect stderr to a file, copied
  content lands there base64-encoded.
- **A malicious `gcloud` on `PATH`** would be trusted, since g9s shells out to
  it by name unless `gcloud_path` is absolute. This is the same trust you
  already extend to gcloud.

## Dependencies

16 direct dependencies; about 108 module roots and 508 packages linked into the
binary as of the networking listers. Cloud SQL, Cloud DNS and all seven
networking kinds added no new modules at all — they live in
`google.golang.org/api` and `cloud.google.com/go/compute`, both already
present. All from Google
(`cloud.google.com/go/*`, `google.golang.org/*`), the CNCF (gRPC's xDS
machinery, OpenTelemetry), the Go team (`golang.org/x/*`), charmbracelet (the
TUI stack) or go-yaml. No unmaintained or single-author-obscure packages in the
build.

**Why the jump from ~40 to ~106 module roots.** `cloud.google.com/go/storage`
supports both a JSON/HTTP transport and an optional gRPC transport
("Directpath"), and g9s uses only the former — `storage.NewClient`, not
`NewGRPCClient`. But the package statically imports both code paths, so both
compile into the binary regardless of which one ever runs. That pulls in
gRPC's xDS load-balancing stack (`google.golang.org/grpc/xds`, `orca`,
`channelz`, `cel.dev/expr`, `envoyproxy/go-control-plane`, `cncf/xds`), SPIFFE
mTLS support (`go-spiffe`), and OpenTelemetry's SDK and exporters
(`cloud.google.com/go/monitoring`, `opentelemetry-operations-go`) — none of it
reachable from code g9s calls, all of it compiled in anyway. This is a known
property of the official client library, not a supply-chain concern: every
package is Google- or CNCF-maintained and none carries an open advisory (see
below). It is, however, a materially larger trusted-code footprint than the
three-lister version of this tool had, which is worth knowing rather than
glossing over.

Status of the versions in use against published advisories, checked July 2026:

| Module | Version | Status |
|---|---|---|
| `golang.org/x/net` | v0.56.0 | Past v0.55.0, which fixed CVE-2026-39821 (idna), CVE-2026-25680 and CVE-2026-42506 (html). `x/net/html` is not linked at all; only `idna` and the HTTP/2 plumbing are. |
| `google.golang.org/grpc` | v1.82.1 | Upgraded from v1.82.0 to clear **GO-2026-6061** (xDS RBAC engine and HTTP/2 server transport), which CI caught. Also past v1.79.3, which fixed CVE-2026-33186. |
| `golang.org/x/text` | v0.40.0 | Upgraded from v0.38.0 to clear **GO-2026-5970** (infinite loop on invalid input in `norm`), reachable through the OAuth2 token exchange. Fixed in v0.39.0. |
| `gopkg.in/yaml.v3` | v3.0.1 | Fixed for CVE-2022-28948. CVE-2022-3064 (deeply nested input) is not in the threat model: the only YAML g9s parses is your own config file. |
| `golang.org/x/crypto` | v0.53.0 | Only TLS primitives linked (chacha20poly1305, hkdf, cryptobyte). The `ssh` package, where the notable advisories live, is not present. |
| `go-jose/go-jose/v4` | v4.1.4 | Exactly the version that fixed CVE-2026-34986 (JWE decrypt panic); past v4.0.5, which fixed CVE-2025-27144 (unbounded memory on a crafted token). Pulled in by the storage client's transport plumbing, not called directly. |
| `envoyproxy/go-control-plane`, `spiffe/go-spiffe/v2` | v1.37.0, v2.6.0 | No advisories found against either at these versions. Note these are Go SDK/config-generation libraries, not the Envoy proxy binary itself — the C++ Envoy CVEs that turn up in a search do not apply to this module. |

That table is a point-in-time check against published advisories, not a
substitute for a scanner — and it has already been proven so. The grpc and
x/text rows above were both green when first written, and both went stale
within days as new advisories landed. `govulncheck` in CI is what caught them,
which is the whole argument for having it: a hand-checked table tells you about
the advisories that existed when someone last looked.

**CI runs `govulncheck ./...` on every push**, which reports only
vulnerabilities reachable from this code rather than every advisory touching the
module graph. A red `vulncheck` job is expected to mean a real upgrade is
needed, not noise to be waved through. To run it yourself:

```sh
go install golang.org/x/vuln/cmd/govulncheck@latest
govulncheck ./...
```

## Release binaries

Releases are built by GitHub Actions, never on a maintainer's machine, and only
after `gofmt`, `go vet`, `go test -race` and `govulncheck` have all passed —
the build job declares `needs: [version, test, vulncheck]`, so a red check
produces no artifacts and therefore no release. Binaries are built once and
promoted: the archive attached to a release is byte-identical to the one the
build job produced, because nothing is recompiled at publish time.

Publishing is automatic on every merge to `main` (patch version bumped from the
previous release), which means **the security posture of a release is exactly
the posture of the checks above** — there is no manual step at which someone
could wave a failing scan through. The version is decided before compilation so
the binary is stamped with the version it is published as, rather than reporting
something different from the release it is attached to.

**Verify provenance, not just checksums.** `checksums.txt` only proves your
download was not corrupted in transit. It proves nothing about origin — anyone
can publish a file and a matching hash. Each archive additionally carries a
signed [SLSA build provenance](https://slsa.dev/) attestation binding it to this
repository, the exact commit and the workflow run:

```sh
gh attestation verify g9s_v0.1.0_darwin_arm64.tar.gz --repo TTMathCS/g9s
```

That fails if the archive was built anywhere other than this repo's CI, which is
the property actually worth checking.

Two limits worth stating rather than leaving implied:

- **The binaries are not Apple-notarised.** Notarisation needs a paid Developer
  ID, so macOS Gatekeeper will warn on first run. That is a code-signing gap,
  not a provenance one — the attestation above still proves origin.
- **Downloading a binary is a different trust model from building source you
  have read.** Provenance proves *where* it was built, not that the source is
  benign. If your threat model does not extend that trust to this repository's
  CI, build from source in a container you control; the workflow in
  `.github/workflows/ci.yml` shows exactly what the release build does.

The workflow keeps `permissions: contents: read` at the top level and grants
write only inside the release job, so nothing in the test, scan or build path
can write to the repository. Publishing uses the `gh` CLI already on the runner
rather than a third-party action, keeping the release path's supply-chain
surface to first-party GitHub actions only.

## Reporting

Open an issue for anything non-sensitive. For something you would rather not
file in public, use GitHub's private vulnerability reporting on this
repository.
