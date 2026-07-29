# g9s

A k9s-style terminal UI for Google Cloud. Pick a project, browse what's running in it, act on it — without leaving the terminal or juggling `gcloud config set project`.

Built for the case where you have several projects, each reached through a different account, and those accounts expire daily.

```
 g9s  prod-data · acme-prod-data-1234 · svc-prod-support@example.com
 1 VM Instances (12)   2 Dataproc Clusters (3)   3 Composer Environments (1)

   NAME                ZONE              MACHINE TYPE   INTERNAL IP   EXTERNAL IP   STATUS       AGE
 ▸ etl-worker-01       us-central1-a     n2-standard-8  10.0.0.12     -             RUNNING      14d
   etl-worker-02       us-central1-a     n2-standard-8  10.0.0.13     -             RUNNING      14d
   jump-box            us-central1-b     e2-medium      10.0.1.4      34.72.1.9     TERMINATED   62d

 enter describe · o open · s ssh · y yank · / filter · r refresh · p projects · ? help
```

## Why this exists

Cloud Asset Inventory makes "list everything in a project" a single API call. Without it — and plenty of orgs don't enable it — you fan out across a dozen service APIs, several of which are region-scoped, and you do it again for every project. `g9s` does that fan-out and puts the result in one keyboard-driven table.

## Status

MVP. Three resource kinds (Compute Engine, Dataproc, Cloud Composer), read-only plus SSH. The resource layer is behind a one-method interface, so adding a kind is one new file — see [Adding a resource kind](#adding-a-resource-kind).

## Install

```sh
go install github.com/TTMathCS/g9s/cmd/g9s@latest
```

Requires Go 1.25+ and the `gcloud` CLI on your `PATH`.

## Quick start

```sh
g9s -init          # writes ~/.config/g9s/config.yaml
$EDITOR ~/.config/g9s/config.yaml
g9s
```

On first launch every project shows `○ not logged in`. Select one, press `l`, and gcloud takes over the terminal to run the login. Once it's done you're dropped back into the table.

## How authentication works

This is the part worth understanding, because it's usually mis-modelled.

**g9s never sees your password.** When you press `l`, it suspends itself and runs `gcloud auth application-default login` with the terminal handed over. gcloud opens your browser; your identity provider's login page — including the password from your PAM checkout and the MFA challenge — is handled entirely by the browser. g9s resumes once gcloud has written the credentials to disk.

If your identity is federated (Entra ID, Okta, or similar), that browser redirect is what carries you to your IdP. There is no way to type an SSO password into a terminal and have Google accept it, and you should be suspicious of any tool that offers to.

**Each project gets its own credential directory.** `g9s` sets `CLOUDSDK_CONFIG` to a per-project path under `credential_dir`, so:

- logging into one project never disturbs another,
- ten projects with ten different support accounts coexist without `gcloud config configurations` juggling,
- nothing g9s does mutates your normal `~/.config/gcloud` state.

**Expiry is detected by using the credentials, not by reading a timestamp.** A refresh token your IdP has invalidated looks perfectly healthy on disk. g9s mints a real access token to check, so an expired session shows up as `● expired — press l to re-login` rather than as a confusing API error ten seconds later. With a typical federated session policy you should expect to re-login roughly once a day.

### When the terminal isn't on the machine with the browser

Press `L` instead of `l`. That adds `--no-browser`, which prints a bootstrap command to run on a trusted machine that has both a browser and gcloud; you paste the resulting URL back. It's fiddlier than the local flow, so prefer running g9s on your workstation if you can.

## Configuration

`~/.config/g9s/config.yaml`, or `$G9S_CONFIG`, or `-config <path>`.

```yaml
defaults:
  # Swept for region-scoped resources unless a project overrides them.
  # Keep this tight: every region is another API call on every refresh.
  regions:
    - northamerica-northeast1
    - us-central1

  credential_dir: ~/.local/share/g9s/credentials
  gcloud_path: gcloud
  list_timeout: 90s

projects:
  - name: sandbox                    # label in the picker; names the credential dir
    project_id: my-sandbox-project
    description: personal access, read-only

  - name: prod-data
    project_id: my-prod-data-project
    account: svc-prod-support@example.com   # passed to gcloud --account
    regions:
      - northamerica-northeast1             # overrides defaults.regions
    composer_locations:
      - us-central1                         # overrides both, for Composer only
```

Region resolution runs most-specific-first: `projects[].composer_locations` → `projects[].regions` → `defaults.composer_locations` → `defaults.regions`. Dataproc works the same way via `dataproc_regions`, and always includes the `global` region, which is easy to forget and does hold clusters.

Unknown keys are an error rather than a silent default, so a typo'd `regionz:` tells you instead of quietly scanning nothing.

## Keys

| Key | Action |
|---|---|
| `↑`/`k`, `↓`/`j` | move cursor |
| `g` / `G` | top / bottom |
| `1` `2` `3`, `tab` | switch resource kind |
| `enter` | describe (YAML, as `gcloud describe` would show it) |
| `/` | filter rows; `esc` clears |
| `r` | refresh current kind |
| `o` | open — Airflow UI for Composer, Cloud Console otherwise |
| `c` | open in Cloud Console |
| `y` | copy name to clipboard (OSC 52, works over SSH) |
| `s` | SSH to the selected running VM |
| `l` / `L` | log in / log in without a local browser |
| `p` / `esc` | back to the project list |
| `?` | help |
| `q` | quit |

## Partial results are shown as partial

With a least-privilege account, some regions and some APIs will refuse you. A tool that discards the whole refresh because one of ten regions returned 403 is useless, and one that silently drops it is worse — an empty table reads as "nothing is running here."

So listers return whatever succeeded plus a warning per failed scope, and the footer says so:

```
⚠ 2 scope(s) unavailable: europe-west1: permission denied; us-east4: permission denied
```

Errors that are *expected* rather than informative — the API simply isn't enabled in that region, or the region doesn't exist — are suppressed, or the footer would be permanently full of noise.

## Adding a resource kind

Implement `gcp.Lister` in a new file under `internal/gcp` and add it to `Listers()`:

```go
type Lister interface {
	Kind() Kind
	List(ctx context.Context, cfg *config.Config, p config.Project, opts []option.ClientOption) (Result, error)
}
```

Use `fanOut` for anything region-scoped; it handles the concurrency, the partial-failure collection and the stable ordering. `internal/gcp/dataproc.go` is the shortest example.

Two things the table relies on: your `Resource.Row` must have exactly as many cells as `Kind.Columns`, and the number keys only reach the first five listers. Both are covered by tests.

## Design notes

**Why not shell out to `gcloud ... --format=json`?** It's the fast way to build this and it handles auth for free, but each invocation is a ~1–2s Python cold start. Across a fan-out of a dozen regions the UI would feel dead. g9s uses gcloud only where a human is involved — login and SSH — and talks to the APIs directly everywhere else.

**Why is Dataproc the awkward one?** Its endpoint is regional. A request for `us-central1` sent to the default endpoint returns *nothing* rather than an error, so each region needs its own client pointed at `<region>-dataproc.googleapis.com`. Composer is location-scoped through the request parent instead, so one client covers every location. Compute needs no fan-out at all — `aggregatedList` returns every zone in one call.

**Why a quota project?** Application default credentials minted from a user account have no project of their own, and most APIs reject the call outright without one attached. g9s sets it on every client.

## Roadmap

- More resource kinds: Secret Manager, Cloud DNS, load balancers, GKE, Cloud SQL
- Mutating actions behind a confirmation — VM and Dataproc cluster power state, which Terraform doesn't manage, so they don't cause drift
- Terraform state overlay: read the GCS backend to mark each resource managed / drifted / unmanaged, and jump from a row to the `.tf` that defines it
- Cloud Asset Inventory as an optional fast path when the API is available

## License

MIT
