# g9s

[![Latest release](https://img.shields.io/github/v/release/TTMathCS/g9s?label=download&color=2ea44f&style=for-the-badge)](https://github.com/TTMathCS/g9s/releases/latest)
[![CI](https://img.shields.io/github/actions/workflow/status/TTMathCS/g9s/ci.yml?branch=main&style=for-the-badge)](https://github.com/TTMathCS/g9s/actions/workflows/ci.yml)
[![All versions](https://img.shields.io/github/downloads/TTMathCS/g9s/total?label=downloads&style=for-the-badge)](https://github.com/TTMathCS/g9s/releases)

A k9s-style terminal console for Google Cloud. Switch between projects and
accounts, inspect resources, and navigate related resources without changing
your global gcloud configuration.

> **Current maturity:** read-mostly. 51 top-level resource kinds and 21
> drill-down listings, all read-only. The only exceptions are SSH to a running
> VM and three VM power actions (`:start`, `:stop`, `:reset`), each of which
> must be typed as a command and confirmed against the instance's own name.
> Nothing else g9s does can change your cloud.

## ⬇️ Download

**Nothing to compile and no Go toolchain on your machine.** Every merge is
tested, vulnerability-scanned, cross-compiled and published automatically, and
these links always point at the newest release:

| Your machine | Direct download |
|---|---|
| **macOS** — Apple Silicon (M-series) | [**g9s_darwin_arm64.tar.gz**](https://github.com/TTMathCS/g9s/releases/latest/download/g9s_darwin_arm64.tar.gz) |
| **macOS** — Intel | [**g9s_darwin_amd64.tar.gz**](https://github.com/TTMathCS/g9s/releases/latest/download/g9s_darwin_amd64.tar.gz) |
| **Linux** — x86-64 | [**g9s_linux_amd64.tar.gz**](https://github.com/TTMathCS/g9s/releases/latest/download/g9s_linux_amd64.tar.gz) |
| **Linux** — arm64 | [**g9s_linux_arm64.tar.gz**](https://github.com/TTMathCS/g9s/releases/latest/download/g9s_linux_arm64.tar.gz) |

**[Version history and release notes →](https://github.com/TTMathCS/g9s/releases)**
— every release keeps its binaries, so any earlier version stays downloadable.

Unpack and run:

```sh
tar -xzf g9s_darwin_arm64.tar.gz
./g9s -version
```

On macOS the first run of an unsigned binary is blocked ("the developer cannot
be verified"). Clear the quarantine flag once:

```sh
xattr -d com.apple.quarantine ./g9s
```

Every release also carries `checksums.txt` and a signed [SLSA build
provenance](https://github.com/TTMathCS/g9s/attestations) attestation proving
each archive was built by CI from this repository:

```sh
gh attestation verify g9s_darwin_arm64.tar.gz --repo TTMathCS/g9s
```

## What it looks like

Every image is generated from the real UI code with invented data — see
[docs/README.md](docs/README.md) for how, and for the full set.

**The project picker**, with the live credential state of all ten configured
projects — who is logged in, as which account, and for how much longer:

![Project picker with per-project credential state](docs/projects.png)

**The per-project dashboard**: every resource category, its count and status
rollup, and partial-failure warnings that cannot be mistaken for a clean
result:

![Per-project dashboard with resource counts and status summaries](docs/dashboard.png)

**A resource table** — filtering, YAML describe, Console links, clipboard and
SSH, with the tab strip showing counts per kind and the footer carrying a
permission warning for an unreachable region:

![VM instance table with a partial-result warning](docs/resources.png)

**Describe (`d`)** decodes any row to YAML, with secret-bearing fields
redacted before they reach the terminal:

![YAML describe pane for a VM instance](docs/detail.png)

**Drill-downs** open a row's children in place — a GKE cluster's node pools
here — and a parent with several listings shows them as sibling tabs, like a
Cloud SQL instance's databases and users:

![A GKE node-pool drill-down opened from its parent cluster](docs/drilldown.png)

![Cloud SQL sibling drill-downs: databases and users side by side](docs/siblings.png)

**Assisted login** keeps the corporate-proxy sign-in alive: when the browser
gets stuck on `localhost refused to connect`, pasting that tab's address back
finishes the login — no waiting on a terminal that will never move:

![Assisted login screen with the paste rescue](docs/login.png)

**`g9s doctor`** checks the whole setup from the command line and says what to
fix — including the proxy configuration that would break the browser login:

![g9s doctor output with findings and remedies](docs/doctor.png)

## At a glance

```text
Projects  →  Project dashboard  →  Resource table  →  Child drill-down
accounts      all kinds             one kind          node pools, tables,
and login     and status            or all kinds      keys, health, etc.
```

- **Projects** shows every configured project and its live credential state.
- **Dashboard** loads all implemented kinds and summarizes counts, states and
  partial failures.
- **Resource table** provides filtering, YAML details, Console links, copy and
  SSH where applicable.
- **Drill-down** opens a resource's children in place. For example, a GKE
  cluster opens its node pools and a Cloud SQL instance opens databases and
  users.

## Complete resource map

This is the source of truth for resource coverage.

- ✅ **Implemented** — available now and complete for its documented scope.
- 🟡 **Implemented, bounded** — available now, with a configurable default row
  or request limit. The TUI warns when the limit is reached.
- ✅ **Implemented, paged** — available now, loading one bounded page at a
  time with an explicit continuation action.
- 🔒 **Implemented, metadata only** — available now, with sensitive values
  deliberately excluded.
- 🔜 **Not implemented, next** — the next planned resource work.
- ⬜ **Not implemented, candidate** — planned for consideration, with no
  delivery commitment.
- 🚫 **Not planned** — intentionally outside the product boundary.

**Surface** shows how the listing is reached: a top-level dashboard kind or a
drill-down opened from a parent row.

| Area | Service and resource hierarchy | Surface | Status | Coverage note |
|---|---|---|---|---|
| Compute and serverless | **Compute Engine** → VM instances | Top-level | ✅ Implemented | One aggregated call covers every zone |
|  | ↳ Attached disks | VM drill-down | ✅ Implemented | Already present on the VM response; highlights auto-delete |
|  | **Compute Engine** → Persistent disks | Top-level | ✅ Implemented | Includes unattached state and age |
|  | **Compute Engine** → Disk snapshots | Top-level | ✅ Implemented | Global plus regional (GCP Preview), in one project-wide listing; snapshots can outlive their source disks |
|  | **Compute Engine** → Managed instance groups | Top-level | ✅ Implemented | Zonal and regional groups in one aggregated listing |
|  | ↳ Managed VM instances | Group drill-down | ✅ Implemented | Instance state, current action, intended template and version |
|  | **Compute Engine** → Instance templates | Top-level | ✅ Implemented | Global and regional templates; includes machine shape and accelerators |
|  | **Compute Engine** → Capacity reservations | Top-level | ✅ Implemented | Includes GPU-backed reservations and unused/partial utilization findings |
|  | **Cloud TPU** → Nodes | Top-level | ✅ Implemented | Swept per configured region across every zone in it. Leads with accelerator type and PREEMPTED, since a reclaimed TPU is not coming back on its own |
|  | **Batch** → Jobs | Top-level | ✅ Implemented | Listed per configured region. Task counts per state, because a job reporting RUNNING with every task failing reads identically to a healthy one |
|  | **Cloud Functions** → Functions | Top-level | ✅ Implemented | Both generations; aggregated across locations |
|  | **Cloud Run** → Services | Top-level | ✅ Implemented | Listed across configured regions |
|  | ↳ Revisions | Service drill-down | ✅ Implemented | Includes traffic split from the parent service |
|  | **Cloud Run** → Jobs | Top-level | ✅ Implemented | Includes the last execution result |
|  | ↳ Executions | Job drill-down | ✅ Implemented | Full execution history for the selected job |
| Containers | **Google Kubernetes Engine** → Clusters | Top-level | ✅ Implemented | Aggregated across zonal and regional clusters |
|  | ↳ Node pools | Cluster drill-down | ✅ Implemented | Already present on the cluster response |
| Storage | **Cloud Storage** → Buckets | Top-level | ✅ Implemented | Bucket inventory; objects are fetched only when one bucket is opened |
|  | ↳ Objects and folders | Bucket drill-down | ✅ Implemented, paged | 500 rows per page by default; path navigation, prefix jumps and server-side glob search |
|  | ↳ Lifecycle rules | Bucket drill-down | ✅ Implemented | Delete and storage-class transition rules |
| Data and analytics | **BigQuery** → Datasets | Top-level | ✅ Implemented | Name, location, type and labels |
|  | ↳ Tables | Dataset drill-down | 🟡 Implemented, bounded | 1,000 rows per dataset by default |
|  | **BigQuery** → Jobs | Top-level | 🟡 Implemented, bounded | 500 rows by default inside the configured time window |
|  | **BigQuery** → Reservations | Top-level | ✅ Implemented | Swept across the configured regions plus `US` and `EU`, where most reservations actually live. Flags baseline slots billed continuously and not lent to other reservations |
|  | **Dataproc** → Clusters | Top-level | ✅ Implemented | Listed per configured region; `global` is always included |
|  | ↳ Jobs on this cluster | Cluster drill-down | 🟡 Implemented, bounded | 200 rows by default |
|  | **Dataproc** → Jobs | Top-level | 🟡 Implemented, bounded | 200 rows per configured region by default |
|  | **Dataflow** → Jobs | Top-level | 🟡 Implemented, bounded | 500 rows by default; API aggregates locations |
|  | **Cloud Composer** → Environments | Top-level | ✅ Implemented | Listed per configured location |
|  | **Bigtable** → Instances | Top-level | ✅ Implemented | One global call. An instance holds no data itself; DEVELOPMENT type is flagged because it means one node, no SLA and no replication |
|  | ↳ Clusters | Instance drill-down | ✅ Implemented | Nodes, zone, storage type and autoscaling — the billed capacity, which the instance row cannot show |
|  | **Spanner** → Instances | Top-level | ✅ Implemented | One global call. Capacity normalised to processing units with the node equivalent, so an instance sized in nodes and one sized in PU are comparable |
|  | ↳ Databases | Instance drill-down | ✅ Implemented | Leads with drop protection, which is off by default and is the only guard against a one-command deletion |
|  | **Memorystore** → Redis instances | Top-level | ✅ Implemented | One call; the API aggregates every region. Flags basic tier (single node, no failover) and AUTH left off |
|  | **Memorystore** → Memcached instances | Top-level | ✅ Implemented | One call; the API aggregates every region. Flags nodes that are not serving while the instance itself still reports READY |
|  | **Firestore** → Databases | Top-level | ✅ Implemented | One global call. Delete protection and point-in-time recovery are both off by default and report nothing when off |
|  | **Datastream** → Streams | Top-level | ✅ Implemented | One call; the API aggregates every region. Errors outrank the state, because a stream can be RUNNING and failing every row it reads |
|  | **Data Fusion** → Instances | Top-level | ✅ Implemented | One call; the API aggregates every region. Edition leads the row — an instance bills per hour for as long as it exists, run or not |
|  | **Artifact Registry** → Repositories | Top-level | ✅ Implemented | Listed per configured region. Size and cleanup policy on the row, because a registry nothing prunes grows until someone reads the bill |
| Databases | **Cloud SQL** → Instances | Top-level | ✅ Implemented | Includes version, tier, HA and unreachable-region warnings |
|  | ↳ Databases | Instance drill-down | ✅ Implemented | One of two sibling listings |
|  | ↳ Users | Instance drill-down | ✅ Implemented | Includes disabled state; `tab` switches siblings |
| Messaging | **Pub/Sub** → Topics | Top-level | ✅ Implemented | Includes ingestion-source state |
|  | ↳ Subscriptions on this topic | Topic drill-down | ✅ Implemented | Warns when a topic has no subscriptions |
|  | **Pub/Sub** → Subscriptions | Top-level | ✅ Implemented | Includes backlog from one Monitoring query |
| Networking | **VPC** → Networks | Top-level | ✅ Implemented | Subnet mode, count and routing mode |
|  | ↳ Subnets | Network drill-down | ✅ Implemented | Aggregated across regions; includes secondary ranges |
|  | **VPC** → Firewall rules | Top-level | ✅ Implemented | Sorted by evaluation priority; disabled rules are flagged |
|  | **VPC** → Routes | Top-level | ✅ Implemented | Network, destination, priority and resolved next-hop type |
|  | **Cloud Router** → Routers | Top-level | ✅ Implemented | Region, network, ASN, peers, interfaces and NAT count |
|  | ↳ Cloud NAT gateways | Router drill-down | ✅ Implemented | Inline router configuration; IP allocation, sources, ports and logging |
|  | **Cloud Load Balancing** → Forwarding rules | Top-level | ✅ Implemented | Covers global and regional forwarding rules |
|  | ↳ Backend health | Rule drill-down | 🟡 Implemented, bounded | 40 backend groups by default; bounds API requests |
|  | **Cloud DNS** → Managed zones | Top-level | ✅ Implemented | Global listing |
|  | ↳ Record sets | Zone drill-down | 🟡 Implemented, bounded | 1,000 rows per zone by default |
|  | **Cloud VPN** → VPN tunnels | Top-level | ✅ Implemented | Aggregated across regions with live tunnel status |
|  | **Cloud Interconnect** → VLAN attachments | Top-level | ✅ Implemented | Aggregated across regions |
|  | **Private Service Connect** → Service attachments | Top-level | ✅ Implemented | Producer side; consumer endpoints are forwarding rules |
|  | **Compute networking** → Reserved static IPs | Top-level | ✅ Implemented | Global and regional IPv4/IPv6 addresses, users and reservation state |
| Security and identity | **Secret Manager** → Secrets | Top-level | 🔒 Metadata only | Replication, rotation and expiry; never secret values |
|  | ↳ Versions | Secret drill-down | 🔒 Metadata only | Names, states and timestamps; never secret values |
|  | **IAM** → Service accounts | Top-level | 🟡 Implemented, bounded | Key age lookup for 200 accounts by default |
|  | ↳ Keys | Account drill-down | 🔒 Metadata only | ID, origin, algorithm, age and expiry; never private key material |
|  | ↳ Direct project roles | Account drill-down | ✅ Implemented | One version-3 project policy read, filtered to the account; conditions are shown and inherited folder/organization roles are explicitly outside the listing |
|  | **IAM** → Project policy bindings | Top-level | ✅ Implemented | One row per member per role, the way the question is usually asked. Flags allUsers, primitive roles and deleted principals; inherited folder/org roles are outside the listing and it says so |
|  | **Cloud KMS** → Keys | Top-level | 🟡 Implemented, bounded | 100 key rings per location by default; never key material |
|  | **Certificate Manager** → Certificates | Top-level | ✅ Implemented | `global` plus the configured regions. Flags expiry inside 30 days, already-expired, and managed certificates stuck provisioning |
|  | **Binary Authorization** → Policies | — | ⬜ Candidate | One policy per project rather than a list; belongs in a detail pane, not a table |
|  | **VPC Service Controls** → Perimeters | — | 🚫 Not planned | Perimeters are defined on an access policy at the organisation, not in a project. g9s is scoped to a project and has no organisation credential to read them with |
|  | **Organization Policy** → Effective constraints | — | ⬜ Candidate | The effective policy needs one call per constraint, and the constraint list is long; a useful table here means choosing which constraints matter rather than listing all of them |
| Operations | **Cloud Scheduler** → Jobs | Top-level | ✅ Implemented | Includes paused state and last-attempt result |
|  | **Cloud Monitoring** → Alert policies | Top-level | ✅ Implemented | One global call. Flags policies that are disabled, have no notification channels, or have no conditions — monitoring that exists and does nothing. Firing state is not exposed by this API and is deliberately not invented |
|  | **Error Reporting** → Error groups | Top-level | ✅ Implemented | Grouped, most frequent first, over a one-day window. FIRING separates a fault still happening from one that merely happened today |
|  | **Cloud Tasks** → Queues | Top-level | ✅ Implemented | Listed per configured region. Leads with PAUSED, the state that silently accumulates work, plus the rate limits that stop a queue keeping up |
|  | **Cloud Build** → Build history | Top-level | 🟡 Implemented, bounded | 200 most recent builds by default, newest first; flags manual builds and running ones by elapsed time |
|  | **Cloud Logging** → Log entries | — | 🚫 Not planned | An unbounded query stream, not a finite resource inventory |
| Cost and capacity | **Cloud Quotas** → Usage and limits | — | ⬜ Candidate | Nested quota metrics need a different presentation, and the Cloud Quotas API is not in the pinned client module |
|  | **Cloud Billing** → Current-month spend | — | ⬜ Candidate | Requires a BigQuery billing export that most projects do not have, and reads cost data that is usually restricted more tightly than anything else here |

### Platform capabilities

| Capability | Status | Current note |
|---|---|---|
| Project picker and live credential state | ✅ Implemented | Checks every configured project at startup |
| Isolated credentials per project | ✅ Implemented | Does not mutate global gcloud state |
| Per-project dashboard and **All Resources** view | ✅ Implemented | Status rollups plus a merged table |
| Filtering, YAML detail, links, clipboard and SSH | ✅ Implemented | SSH is limited to running VMs |
| Parent/child drill-downs with sibling tabs | ✅ Implemented | 21 child listings |
| Partial-result and row-cap warnings | ✅ Implemented | A bounded or incomplete result cannot look complete |
| One shared credential per project | ✅ Implemented | All 43 listers share one token source instead of each reading the credential file and refreshing separately |
| Memoised table assembly | ✅ Implemented | The merged table and its filter are cached against the data, so typing does not rebuild thousands of rows per keystroke |
| Structured completeness, not warning strings | ✅ Implemented | Each gap carries a scope and a reason, so a missing permission is distinguishable from a row cap without parsing prose |
| Expected account versus actual ADC identity | ✅ Implemented | The actual identity is displayed; a live token for a different configured account is refused |
| Assisted login for proxied corporate browsers | ✅ Implemented | Paste the stuck localhost redirect; g9s delivers it past the proxy |
| Consistent HTTP/gRPC permission errors | ✅ Implemented | REST 403/401 map to the same wording as gRPC; unenabled APIs stay quiet |
| Large OSC 52 clipboard payload handling | ✅ Implemented | The escape sequence is measured against a configurable `clipboard_limit`; an oversized copy is refused rather than silently dropped |
| Panic isolation in concurrent listings | ✅ Implemented | A panic in one fan-out scope becomes that scope's warning instead of killing the process and stranding the terminal |
| Prebuilt release pipeline | ✅ Implemented | Every merge to `main` builds, checks and publishes a release |
| Confirmed VM power actions | ✅ Implemented | `:start` / `:stop` / `:reset`, typed as commands and confirmed against the instance name; production demands the typed form for all three |
| Confirmed Dataproc state actions | ⬜ Candidate | The VM action framework generalises, but each new service is its own decision |
| Terraform managed/unmanaged overlay | ✅ Implemented | `:tf` reads the project's state from its GCS backend and marks the open table. Only the resource type and name survive parsing — never a value from `attributes`. A kind g9s has no Terraform type for reads `?`, never `unmanaged` |
| Terraform drift detection | 🚫 Not planned | Drift is what `terraform plan` computes, and computing it means running Terraform against your configuration. g9s reads state; it does not plan |
| Cross-project inventory for one kind | ✅ Implemented | `:fleet <kind>` sweeps every configured project, four at a time. Projects that were denied, failed or never asked are named beneath the table rather than dropped, because a count that quietly omits them reads as a statement about the estate |
| Horizontal dev/uat/prod comparison | ✅ Implemented | `:diff <kind>` lays the same sweep out with the projects as columns and the rows that do not line up first. A project that could not be read shows `?`, never `-`, so a permission error never becomes a missing resource |
| Cloud Asset Inventory fast path | ⬜ Candidate, blocked | One call could replace the whole fan-out, but CAI returns a generic asset shape rather than each service's own response — so the rows it can build are not the rows the normal path builds. A fast path that silently produces different columns is the exact failure the rest of g9s is built to avoid, and closing that gap means reimplementing every lister against a second, sparser source. See [ROADMAP.md](ROADMAP.md) |
| Saved filters and bookmarks | ✅ Implemented | `:bm <name>` saves the project, kind and filter you are looking at; `:bm` opens the list. Stored in `bookmarks.yaml` beside the config rather than inside it, so saving one never rewrites a hand-edited file |
| CSV and JSON export | ✅ Implemented | `:export csv` / `:export json` writes the visible table; JSON records whether the listing was complete |
| `g9s doctor` preflight checks | ✅ Implemented | Config, gcloud, proxy/loopback, credential permissions, live identity |
| Writing infrastructure definitions | 🚫 Not planned | g9s is not a Terraform replacement |
| Storing passwords or minting credentials itself | 🚫 Not planned | gcloud owns interactive authentication |
| Displaying secret values or private key material | 🚫 Not planned | Metadata only |

See [PERMISSIONS.md](PERMISSIONS.md) for the API to enable and the IAM
permissions to grant per resource kind, [ROADMAP.md](ROADMAP.md) for design
rationale and request costs, and [ADVICE.md](ADVICE.md) for the cross-project
architecture review.

## Requirements and installation

The **prebuilt binary from the [⬇️ Download](#g9s) table at the top is the
intended install** — nothing compiles, tests or fetches modules on your
machine. You also need the **gcloud CLI**, which g9s shells out to for login
and SSH (372.0.0+ for the `--no-browser` flow), and read access to the projects
you configure — see [PERMISSIONS.md](PERMISSIONS.md) for the API to enable and
the IAM permissions each resource kind needs:

```sh
brew install --cask gcloud-cli   # macOS with Homebrew; or use Google's installer
```

Building from source is the alternative when you would rather compile than
download, and needs Go 1.25+:

```sh
git clone https://github.com/TTMathCS/g9s.git
cd g9s
go build -o g9s ./cmd/g9s
```

or `go install github.com/TTMathCS/g9s/cmd/g9s@latest` (binaries land in
`~/go/bin`). On a restricted network, point `GOPROXY` at your internal module
registry (for example an Artifactory Go repository); cloning the source does
not remove the need to fetch the modules in `go.mod`.

## Quick start

```sh
g9s -init
$EDITOR ~/.config/g9s/config.yaml
g9s
```

Start with one project. Select it and press `l`; g9s starts
`gcloud auth application-default login` and opens the sign-in link, staying
interactive so a login that gets stuck can be rescued — see
[Login on a corporate laptop](#login-on-a-corporate-laptop). Press `L`, or set
`defaults.login_no_browser: true`, when this machine has no browser at all.
`g9s doctor` checks the whole setup from the command line.

## Configuration

The default file is `~/.config/g9s/config.yaml`. Override it with
`$G9S_CONFIG` or `g9s -config <path>`.

```yaml
defaults:
  # Regional services only scan locations listed here.
  regions:
    - northamerica-northeast1
    - us-central1

  credential_dir: ~/.local/share/g9s/credentials
  gcloud_path: gcloud
  login_no_browser: false
  list_timeout: 90s
  bigquery_job_window: 24h
  storage_objects_page_size: 500
  clipboard_limit: 8192

projects:
  - name: sandbox
    project_id: my-sandbox-project
    description: personal access, read-only

  - name: prod-data
    project_id: my-prod-data-project
    account: svc-prod-support@example.com
    regions:
      - northamerica-northeast1
    composer_locations:
      - us-central1
```

Project settings override defaults. Dataproc and KMS always include `global`.
Unknown YAML keys are errors. An existing ADC file can be selected with
`credentials_file` when no interactive browser flow works. See
[config.example.yaml](config.example.yaml) for the annotated configuration.

### Storage object browsing

Open a bucket and select its **Objects** drill-down. The initial page lists
only the current path's immediate objects and folders; it never recursively
downloads an entire bucket.

| Key or command | Action |
|---|---|
| `enter` on a folder | Open that prefix |
| `q` / `esc` | Clear a search, move to the parent path, then return to buckets |
| `space` | Load the next page when the count has a `+` suffix |
| `:cd logs/2026/` | Jump to a prefix relative to the current path |
| `:cd /logs/` or `:cd gs://bucket/logs/` | Jump from the bucket root |
| `:find **/*.json` | Server-side glob search below the current path |
| `/` | Filter only the rows already loaded in the TUI |

`storage_objects_page_size` defaults to 500 and accepts 1–1,000. It controls
one TUI page, not the total number of rows available: continuation tokens remain
available until the API reports the last page. The client may make another
service request to fill one page when Cloud Storage returns a shorter response.
Current object generations are shown by default; older versions and
soft-deleted objects are not mixed into the ordinary browser.

### Export

`:export csv` or `:export json` writes the table currently on screen to a file
in the working directory and reports the path. What lands in the file is what
is displayed: the visible columns, the rows left after the active filter, in
the order shown.

JSON also records where the rows came from and, crucially, whether the listing
was **complete** — a count taken from a table that was missing two regions is a
lower bound, and a file pasted into a ticket has no footer to say so. The CSV
carries the rows alone so it opens cleanly in a spreadsheet; if the listing was
incomplete the confirmation says so at export time.

Raw API objects are deliberately not exported. Those carry the fields the
detail pane redacts — a VPN tunnel's pre-shared key, a cluster's client key —
and a file on disk is a worse place for them than a terminal. For one
resource's full detail, use `d` then `y`.

### Changing things

g9s can start, stop and reset a VM. That is the entire set — no delete, no bulk
form, and nothing at all for any other service. Everything else it does is a
read.

| Command | What it does |
|---|---|
| `:start` | Boots a stopped instance |
| `:stop` | Shuts the guest down cleanly; anything on it goes offline |
| `:reset` | Cuts power without telling the guest, like pulling the plug |

Four things stand between a keystroke and a stopped production VM:

1. **They are commands, not keys.** Every other binding in g9s is one
   keystroke, because nothing else it does can be regretted. These have to be
   typed in full — `:st` does not resolve to `:stop`, deliberately, while every
   other command does accept prefixes.
2. **The confirmation names the exact target** — instance, zone, project name
   *and* project ID, and the account it will act as.
3. **Destructive actions require the instance's name to be typed**, exactly.
   `:stop` and `:reset` always; `:start` too in production.
4. **Production is louder.** Mark a project `production: true` and every action
   on it gets the typed confirmation and a banner. Left unset, g9s guesses from
   the project name and ID — a guess that can only ever *add* friction, which
   is why it is safe to make.

The permissions for these are **not** granted by `roles/viewer` and have their
own section in [PERMISSIONS.md](PERMISSIONS.md). Granting none of them is a
supported way to run g9s: every table still works and the actions fail with a
permission error.

### Clipboard size

`y` copies over the OSC 52 terminal escape, which needs no clipboard binary and
works over SSH — but the clipboard travels as a single escape sequence, and a
terminal that finds it too long discards the whole thing without reporting
anything. `clipboard_limit` (default 8,192 bytes, the point where a stock xterm
stops) is measured against the *encoded* sequence, which base64 makes about a
third larger than the text. An oversized copy is refused with a message naming
both sizes rather than reported as a copy that did not happen. Raise it once
you know what your terminal accepts; `-1` removes the check. In tmux, OSC 52 is
swallowed entirely unless `set-clipboard on` is set.

### Row and request limits

Omitted or `0` uses the default. A positive number sets a custom cap; `-1`
removes it. The footer warns whenever a listing reaches its cap.

| Key | Default | Bounds |
|---|---:|---|
| `bigquery_jobs` | 500 | Rows inside `bigquery_job_window` |
| `dataflow_jobs` | 500 | Rows |
| `dataproc_jobs_per_region` | 200 | Rows per region |
| `cluster_jobs` | 200 | Rows per cluster |
| `bigquery_tables` | 1,000 | Rows per dataset |
| `dns_record_sets` | 1,000 | Rows per zone |
| `backend_groups` | 40 | Requests: one health call per group |
| `service_account_key_lookups` | 200 | Requests: one key-list call per account |
| `kms_key_rings` | 100 | Requests per location |
| `cloud_builds` | 200 | Rows, newest first |
| `terraform_state_objects` | 25 | Requests: one download per `.tfstate` object |

```yaml
defaults:
  limits:
    bigquery_jobs: 5000
    dns_record_sets: -1
    backend_groups: 10
```

## Cross-project sweep

`:fleet <kind>` reads one kind across every configured project and puts the
results in a single table, with the project as the first column. `:fleet vm`
answers "how many instances are running across all of these, and where", which
otherwise means visiting each project in turn and holding the totals in your
head. `enter` on a row leaves the sweep and opens that kind in the project the
row came from; `r` re-sweeps; `q` or `esc` cancels and goes back.

Four projects are read at a time. A sweep multiplies out as projects × the
regions the kind covers, so ten projects against a six-region kind is sixty
calls if nothing holds it back — enough to hit a per-minute quota on a service
nobody was having trouble with.

The line above the table is the part that matters:

```text
  VM Instances across 5 projects
  2/5 projects read · 1 partial, 1 failed, 1 skipped
```

A cross-project count reads as a statement about the estate, and it is only
ever a statement about the projects that answered. Every project that did not
contribute is named beneath the table with its reason — denied, unreachable,
or never asked because there is no valid credential for it — rather than
silently dropped, which would leave a table that looks complete and is not.

### Comparing environments

`:diff <kind>` lays the same sweep out sideways: one column per project, one
row per resource, rows that do not line up first. `c` switches between the two
shapes without fetching again — one sweep feeds both.

```text
  RESOURCE       PROD       STG        DEV
▸ etl            RUNNING    RUNNING    -
  only           RUNNING    -          ?
  api            RUNNING    RUNNING    RUNNING
```

`-` means that project was read and does not have it. **`?` means that project
could not be read**, so nothing is known either way — a permission error must
never turn into a missing resource, which is the one mistake this table could
make that would send someone to fix something that was never broken.

Rows are grouped by name with the environment words and the project's own name
segments removed, so `api-dev-01` and `api-prod-01` are one row. That is a
heuristic, and `enter` on a row is where you check its work: it lists every
real name that landed on that row, beside the project it came from, and
separates the projects that were read and lack it from the ones nobody could
read. Two resources in one project never merge into one row — the second keeps
its own name instead.

Narrow terminals get fewer columns and a line naming the projects that were
dropped, rather than a comparison quietly missing one.

## Terraform overlay

`:tf` marks the table you are looking at against the project's Terraform
state: which of these resources is in code, and which was made by hand.

```yaml
projects:
  - name: prod
    project_id: acme-prod-1
    terraform:
      state_bucket: acme-tfstate     # no gs://
      state_prefix: prod             # the backend's prefix
```

The same two values as the `backend "gcs"` block your Terraform config already
has. `defaults.terraform` sets a fallback for every project, which suits an
estate keeping all its workspaces in one bucket and is wrong for one giving
each project its own — the per-project setting wins.

```text
  NAME            LOCATION        STATUS     TERRAFORM
▸ api-01          us-central1-a   RUNNING    MANAGED
  scratch-box     us-central1-a   RUNNING    UNMANAGED
```

Three things it deliberately does not do:

- **It never guesses.** A kind g9s has no Terraform type for shows `?`, never
  `UNMANAGED`, and the line above the table says so. An overlay reporting a
  managed resource as untracked because g9s did not recognise the type name is
  how somebody deletes production.
- **It does not detect drift.** Drift is what `terraform plan` computes, and
  computing it means running Terraform against your configuration. g9s reads
  state and reports presence.
- **It does not read your secrets.** A state file carries every value the
  provider round-trips — passwords, keys, certificates. g9s keeps the resource
  type and name and drops the attributes before anything else in the process
  sees them, guarded by a test that fails if a value from `attributes` survives
  parsing. It is still a bucket worth being careful who can read; see
  [PERMISSIONS.md](PERMISSIONS.md).

The line above the table names the state files that were read, because a table
full of `UNMANAGED` usually means the wrong state was consulted rather than an
estate built by hand. State files that could not be read are named beneath it,
for the same reason: one that failed to load makes everything it manages look
untracked.

## Bookmarks

`:bm <name>` saves where you are — the project, the kind and the filter you
typed. `:bm` with no name opens the list; `enter` goes there, `d` removes one.
A sweep is saved as a sweep, so a bookmarked `:diff run` reopens the
comparison rather than one project's table.

Bookmarks live in `bookmarks.yaml` **beside** your config, not inside it:

```yaml
bookmarks:
    - name: prod-api
      project: prod
      kind: vm
      filter: api
    - name: all-errors
      kind: errors
      sweep: fleet
```

A separate file because `config.yaml` is hand-written and full of comments,
and rewriting it from a struct would delete every one of them the first time
you saved a bookmark. It is written by replacing a complete temporary file, so
a crash or a full disk cannot leave a half-written list that parses as a
shorter one.

Opening a bookmark re-resolves everything it names. A project that has left
the config, or a kind that no longer exists, says which — a bookmark that
silently lands somewhere else is worse than one that fails.

## Keys

| Key | Action |
|---|---|
| `↑`/`k`, `↓`/`j`, `g`/`G` | Move; jump to top or bottom |
| `enter` | Open a dashboard kind or the selected row's drill-down; otherwise describe |
| `d` | Always describe the selected resource as YAML |
| Displayed hotkey | Open that resource kind directly |
| `0` / `a` | Open **All Resources** |
| `tab` / `shift+tab`, `]` / `[` | Cycle kinds or sibling drill-downs |
| `:` | Command mode; for example `:vm`, `:all`, `:export csv`, `:stop`, or `:cd` / `:find` in Storage Objects |
| `:fleet <kind>` | One kind across every configured project; `enter` opens the row in the project it came from |
| `:diff <kind>` | The same sweep with the projects as columns and the differences first; `c` switches between the two shapes |
| `:bm <name>` | Save where you are — project, kind and filter; `:bm` alone opens the list, `d` removes one |
| `:tf` | Mark the open table managed/unmanaged from the project's Terraform state |
| `/` | Filter rows; `esc` clears |
| `space` | Load the next Storage Objects page when available |
| `r` | Refresh the current kind, all dashboard kinds, or selected credential |
| `o` | Open Airflow for Composer; Cloud Console otherwise |
| `y` | Copy the resource name; from detail view, copy YAML (see `clipboard_limit`) |
| `s` | SSH to a selected running VM |
| `l` / `L` | Login: assisted browser flow / no-browser flow for machines without one |
| `q` / `esc` | Back one level |
| `p` | Return to projects |
| `?` | Help |
| `ctrl+c` / `:q` | Quit |

## Authentication and safety

g9s never receives a password. Login is always gcloud's own
`auth application-default login` flow: the browser or identity provider
handles password and MFA, and gcloud writes the credential.

Each project uses its own `CLOUDSDK_CONFIG` directory, so switching projects
does not change `~/.config/gcloud`. Credential expiry is checked with a live
token exchange. Resource discovery uses typed Google clients; gcloud is used
only for login and SSH.

### Login on a corporate laptop

The standard gcloud browser login has a step that corporate proxies break:
after you sign in, the browser fetches `http://localhost:<port>/` to hand the
authorization code back to gcloud. A browser that sends localhost through the
proxy never delivers it — the sign-in *succeeds* and gcloud waits forever.

g9s runs the browser login in **assisted mode** to survive exactly this.
Press `l`; g9s starts gcloud, opens the sign-in link, and stays interactive:

1. If your browser can reach its own localhost, the login completes by itself —
   nothing to do.
2. If the browser instead lands on an error like *"localhost refused to
   connect"*, the login is still alive. **Copy the entire address from that
   stuck tab's address bar** (it looks like
   `http://localhost:8085/?state=…&code=…`) and paste it into g9s. g9s hands
   it to gcloud on your machine directly, bypassing the proxy, and the login
   completes. The code in that address is single-use and useless outside the
   running login.

If the flow cannot run at all (no browser on this machine), press `L` for
gcloud's `--no-browser` flow: gcloud prints a **command** — not a link — to
run on a machine that has a browser and gcloud 372.0.0+, and that command's
output is pasted back. Opening the URL inside it in a browser produces
`Error 400: missing required parameter: redirect_uri`; the command is the
thing to copy.

If neither interactive flow can complete, run
`gcloud auth application-default login` wherever it does work and point the
project at the file it writes with `credentials_file:` in the config.

#### The other proxy failure: gcloud crashes *after* you sign in

There is a second, very similar-looking failure, and the remedies are
opposite. The browser says **"You are now authenticated with the gcloud
CLI!"**, and the terminal shows:

```text
ERROR: gcloud crashed (ConnectionError): HTTPSConnectionPool(host='oauth2.googleapis.com',
port=443): Max retries exceeded with url: /token (Caused by NewConnectionError(…
```

This is **not** the loopback problem, and pressing `L` will not help — the
`--no-browser` flow redeems the code with the same request to the same
endpoint. The sign-in worked and the redirect arrived; that is how gcloud got
as far as asking for a token. What failed is gcloud's own outbound HTTPS
connection to Google, because a proxy is required and it was not configured.

Set it for the shell that runs g9s. This is the one fix that covers gcloud
and g9s together — g9s calls the Google APIs itself and reads these variables,
while `gcloud config set proxy/…` applies to gcloud alone and leaves every
table failing after a successful login:

```sh
export HTTPS_PROXY=http://YOUR-PROXY:PORT
export HTTP_PROXY=http://YOUR-PROXY:PORT
export NO_PROXY=localhost,127.0.0.1,::1,metadata.google.internal
```

`NO_PROXY` is not optional. Without it the loopback redirect is proxied too,
and you trade this failure for the one where the sign-in hangs forever.

If instead the error mentions `CERTIFICATE_VERIFY_FAILED` or
`SSLError`, the connection is reaching Google and the certificate is your
organization's — a proxy terminating TLS. That needs a CA bundle rather than a
proxy address:

```sh
gcloud config set core/custom_ca_certs_file /path/to/corp-ca.pem
export SSL_CERT_FILE=/path/to/corp-ca.pem   # g9s does not read gcloud's setting
```

Never disable certificate verification to get past it — that hands every token
and every API response to whatever is terminating the connection.

`g9s doctor` tests the connection to Google directly and tells these two apart,
so it is worth running **before** the first login rather than after it fails.

### `g9s doctor`

`g9s doctor` (or `g9s -doctor`) checks the whole setup outside the TUI and
prints one line per finding, with a remedy for anything broken: the config
file, the gcloud binary and version, whether a proxy would break the browser
login, per-project credential directory permissions, and — unless `-offline`
is set — whether this machine can reach `oauth2.googleapis.com` at all, plus a
live token exchange for every project verifying each credential belongs to the
account the config expects. The reachability check is the one worth running
before your first login: it distinguishes "no route to Google" from "the
certificate is not Google's", which need opposite fixes and look identical
from the terminal. It exits non-zero on failures, so
it also works as a scripted onboarding check:

```sh
g9s doctor            # everything, including live credential checks
g9s doctor -offline   # config, gcloud and proxy checks only; nothing leaves the machine
```

Every GCP call is a list or a get, with one exception: the three VM power
actions described under [Changing things](#changing-things). API-supplied
control characters are
removed before rendering, and sensitive fields returned inside otherwise
ordinary resources are redacted before YAML reaches the terminal or clipboard.
See [SECURITY.md](SECURITY.md) for the full threat model.

## Partial, bounded and paged results

When one region fails, g9s keeps successful rows and reports the failed scope:

```text
⚠ 2 warnings: europe-west1: permission denied; us-east4: permission denied
```

When a configurable cap is reached, it is reported the same way:

```text
⚠ 1 warning: only the 500 most recent jobs are shown
```

Storage Objects is paged rather than capped. A count such as `500+` means a
continuation token is available; press `space` to append the next page. It is
not shown as a warning because no scope failed and the next page remains
directly reachable.

Keep configured region lists accurate. A regional resource in a location that
was never requested cannot be discovered.

## Development

Top-level kinds implement `gcp.Lister` and register in `Listers()`.
Resources belonging to one parent row implement `gcp.ChildLister` and
register in `Children()`. The UI discovers both registries automatically.

```sh
gofmt -w .
go vet ./...
go test -race ./...
go build ./...
```

## License

[MIT](LICENSE)
