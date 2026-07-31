# Roadmap

What g9s covers today, and what it could cover next. Nothing below is a
commitment or a schedule — it is a map of the surface area and of how much work
each piece actually is.

The shape of the work is set by two things. Some GCP APIs are **global** (one
call lists everything), some are **regional** (one client per region, so the
cost of adding them scales with your `regions` list), and some are **zonal but
aggregatable** (one call, server-side fan-out). And every kind is one file
implementing a one-method interface — see
[Adding a resource kind](README.md#adding-a-resource-kind). The interface is
the reason this list is long: most entries are a day's work, not a project.

Since the hotkey alphabet filled up, there is a second shape too: a
**drill-down**, which hangs off one parent row rather than the project, is
reached with `enter`, and costs no key. Most of what is left below wants that
shape rather than another tab — see [The alphabet is full](#the-alphabet-is-full).

## Shipped

| Resource | Scope | Notes |
|---|---|---|
| Compute Engine instances | zonal, aggregated | one `aggregatedList` call covers every zone |
| GKE clusters | zonal + regional, aggregated | `parent: projects/*/locations/-` covers every zone and region in one call, same as Compute; no fan-out despite being both zonal and regional |
| Cloud SQL instances | global | one paginated `Instances.List`; the only lister whose partial failures arrive as `Warnings` in the response body rather than as an error, so unreachable regions are collected from there instead of through `fanOut` |
| Cloud Storage buckets | global | the simplest lister here — one call, no fan-out, no aggregation trick needed |
| BigQuery datasets | global | one paginated call; name, location, type and labels are everything the list response carries, and anything more costs a `Get` per dataset |
| BigQuery jobs | global | jobs are project-global with the location on each row; scoped by `defaults.bigquery_job_window`, which defines the listing rather than truncating it, and capped at 500 rows, which does truncate it and says so |
| Dataproc clusters | **regional** | needs a client per region at `<region>-dataproc.googleapis.com`; `global` always swept |
| Dataproc jobs | **regional** | same per-region clients as the clusters; every state, newest first, capped at 200 per region because the API has no time filter |
| Cloud Composer environments | location-scoped | one client, location in the request parent |
| Dataflow jobs | **regional, aggregated** | the one regional service with a server-side sweep: `projects.jobs.aggregated` covers every regional endpoint in one paginated call, so there is no fan-out and no dependence on the `regions` list — a pipeline someone launched by hand in a region nobody configured still appears. Endpoints that did not answer come back in `FailedLocation`, the same partial-listing story as GKE's missing zones. Every state, newest first, capped at 500 |
| Pub/Sub topics | global | one paginated call; the state field only appears once an ingestion source breaks, so a healthy topic reports nothing and is shown as `ACTIVE` |
| Pub/Sub subscriptions | global | the backlog column is the point, and it is not on the subscription — it comes from one Monitoring `timeSeries.list` covering every subscription at once, so an unavailable metric costs a warning rather than the table |
| Cloud Run services | **regional** | one client per region: the v2 API documents that location cannot be the `-` wildcard, so there is no aggregated call to fall back on |
| Cloud Run jobs | **regional** | same fan-out as the services; the row leads with the last execution's result, because a job whose executions all fail is still a perfectly healthy job resource |
| VPC networks | global | one call |
| Firewall rules | global | ordered by evaluation priority rather than name, because that is the only order a rule set can be reasoned about |
| Load balancers | global + regional | forwarding rules; the one kind needing two calls, as global and regional rules live in separate collections and missing the global one hides every external HTTP(S) load balancer |
| Cloud DNS zones | global | paginated, via the generated REST client |
| VPN tunnels | regional, aggregated | `aggregatedList` sweeps every region server-side |
| Interconnect attachments | regional, aggregated | attachments rather than circuits: a circuit being up says nothing about whether a given VPC can reach it |
| PSC service attachments | regional, aggregated | producer side only; a consumer endpoint *is* a forwarding rule, so listing those here would double-count |
| Secret Manager secrets | global | **metadata only — never values.** One paginated call to `secrets.list`; `AccessSecretVersion` is never called, so no payload enters the process |
| Service accounts | global | one paginated call for the accounts, then `keys.list` per account — bounded to 200 accounts, twelve at a time. N+1 is the only shape on offer, and it is worth paying: key age is the standing audit question, and putting it on the row is the difference between a table you scan and one you have to interrogate account by account. User-managed keys only; Google-managed ones rotate themselves and would put "2 keys" on every row |

Seven listings hang underneath these, reached with `enter` on the row rather
than a hotkey of their own. A row may hold more than one — `tab` moves between
them the way it moves between kinds one level up:

| Drill-down | Parent | Notes |
|---|---|---|
| GKE node pools | a cluster | free: `clusters.list` already returns node pools inline. Node counts are multiplied out across the pool's zones, because a pool of 2 across three zones runs six VMs and reading the 2 as the total is how a cluster ends up mis-sized on paper |
| Cloud SQL databases | an instance | one call. The first half of the pair that made a row allowed more than one listing |
| Cloud SQL users | an instance | one call. `tab` moves between this and the databases; a disabled account still appears in the list, which is exactly the row worth colouring — it looks like access that exists and is not |
| Load balancer backend health | a forwarding rule | the expensive one, and the argument for the whole mechanism: rule → target proxy → URL map → every backend service it routes to → `getHealth` per backend group. Four-plus round trips to answer for one row, which nobody would pay on every refresh of the load balancers table and everybody would pay once, on the rule they are actually looking at |
| Subnets | a VPC network | one `aggregatedList` covers every region server-side, then filtered to this network. Filtered on the last URL segment rather than the whole self-link: the two references come back from different calls and the API is not consistent about the host or api-version prefix it writes, so comparing full strings silently matches nothing and renders as a network with no subnets. Secondary ranges are shown named, because "which one is pods" is the question a GKE range is opened for |
| DNS record sets | a zone | one paginated call, capped at 1000. Grouped by name rather than sorted flat, so a name's A and AAAA sit on adjacent rows — they are read as a group |
| Service account keys | an account | free: the accounts listing fetched them to compute the oldest-key age. Oldest first — that is the row the table was opened to find |

Plus, across all kinds: a per-project dashboard with status rollups, a merged
*All Resources* table, filtering, describe-as-YAML, Console/Airflow deep links,
clipboard yank over OSC 52, and SSH to a running VM.

Twenty-three kinds is well past what the digits cover, so the hotkey sequence
continues into letters — `1`-`9`, then `b c e f h i m n t u v w x z`, skipping
every letter already bound to an action. Each kind's key is printed beside it on
the dashboard and in the tab strip, and `tab`/`shift+tab`, `0`/`a` and `:<kind>`
still reach everything. The strip scrolls around the active tab and marks hidden
tabs with `‹`/`›`.

## The alphabet is full

Twenty-three kinds, twenty-three keys, nothing spare. That is the whole
lowercase alphabet: twelve letters are actions, fourteen are kinds, and the
digits carry the first nine. The twenty-third key came from folding `c` (open in
Cloud Console) into `o` (open), which did the same thing on every kind but
Composer — the last redundancy there was to spend.

So this is where adding tabs stops, and it is not a limit that needs raising.
Twenty-three rows is already about as much as a dashboard is worth scanning, and
the kinds still worth having are mostly not project-wide lists at all. Node
pools belong to a cluster; keys belong to an account; record sets belong to a
zone. "Every node pool in the project", stripped of which cluster each is in, is
not a question anyone asks.

Those are **drill-downs**: `enter` on a row opens its listing in place, with the
child's own columns and a trail naming the parent, and `esc` puts you back. They
cost no hotkey and nothing has to be registered in the UI. A row may hold more
than one where more than one is the honest answer — an instance has databases
*and* users — and `tab` moves between them, the same key that moves between
kinds one level up. `gcp.ChildLister` is the whole interface; see
[Adding a resource kind](README.md#or-a-drill-down-which-needs-no-key).

Cost varies and that is the point. Some are free — the parent listing already
fetched the children. Others are the opposite, and are drill-downs *because*
they are expensive: backend health walks four resources to answer for one
forwarding rule, which is unthinkable per refresh and unremarkable per keypress.
A drill-down is the right home for both.

A twenty-fourth top-level kind would silently lose its hotkey, so a test fails
the build instead — the reminder to reach for a drill-down.

## Next up

The ones that would earn their place first, roughly in order.

Everything here is a drill-down, and that is not a coincidence — see above.

| Resource | Parent | Why it's near the top |
|---|---|---|
| BigQuery tables | a dataset | a dataset with no way into it is a row that only tells you the dataset exists |
| Cloud Run revisions | a service | which revision is actually serving, and what the traffic split is — the question behind most "I deployed but nothing changed" |
| Attached disks | a VM instance | |
| Subscriptions on a topic | a topic | the subscriptions kind lists them project-wide; per topic is the other axis, and the one "who is reading this" is asked on |

### Blocked on the keyspace

Real kinds with nowhere to bind. Each would be a top-level tab and there is no
key left for one, so they wait for either a drill-down shape that fits or a
decision to change the scheme:

| Resource | Scope | Notes |
|---|---|---|
| Cloud Functions | regional | the last serverless kind missing now that Run is in |
| Compute disks and snapshots | zonal, aggregated | unattached disks are the quiet line on every bill, and an unattached disk has no parent row to hang off |
| Batch jobs | regional | |

## Candidates

Plausible, lower priority, grouped by area.

**Compute and serverless** — instance groups and templates, GPU/TPU
reservations. (Cloud Functions, Compute disks and Batch jobs are up under
*Blocked on the keyspace*.)

**Data** — Cloud SQL is shipped, databases and users included. Still open:
Bigtable instances, Spanner instances and databases, Memorystore
(Redis / Memcached), Firestore databases, Datastream streams, Data Fusion
instances, Artifact Registry repositories, BigQuery reservations.

**Networking** — the core is shipped: record sets, backend health and subnets
included. Still open: routes, Cloud NAT and routers, reserved static IPs.

**Security and identity** — service accounts are shipped. Still open: project
IAM policy bindings, KMS keyrings and keys (with rotation age), Certificate
Manager certificates, Binary Authorization policies, VPC Service Controls
perimeters, Org Policy constraints in effect.

**Operations** — recent Cloud Logging entries scoped to a resource, Monitoring
alert policies and which are firing, Error Reporting groups, Cloud Scheduler
jobs, Cloud Tasks queues, Cloud Build history.

**Cost and quota** — quota usage against limits per service and region, and
current-month spend per project if billing export is reachable.

## Features, not resources

These change how the whole tool behaves rather than adding a kind.

- **Mutating actions behind a confirmation.** VM and Dataproc cluster power
  state first, since Terraform does not manage those and toggling them causes
  no drift. Everything stays read-only by default; shipping a drift footgun
  turned on is worse than shipping nothing.
- **Terraform state overlay.** Read the GCS backend and mark each row managed /
  drifted / unmanaged, and jump from a row to the `.tf` that defines it. The
  single most useful thing on this page, and the most work.
- **Cloud Asset Inventory fast path.** Where the API is enabled, one call
  replaces the entire fan-out. Optional, because plenty of orgs do not enable it
  — that is the reason g9s does the fan-out at all.
- **Cross-project view.** One kind across every project at once, rather than
  per project. The dashboard already aggregates across kinds; this is the other
  axis.
- **Saved filters and bookmarks**, for the query you type ten times a day.
- **Export** the current table to CSV or JSON, for when the answer needs to
  leave the terminal.

*(Prebuilt release binaries shipped — see [Install](README.md#install).)*

## Not planned

- **Writing infrastructure.** g9s is not a Terraform replacement. Mutating
  actions, if they arrive, stay narrow and confirmed.
- **Storing credentials.** g9s never sees your password and never writes a
  credential itself — `gcloud` owns that, and g9s only points it at a
  per-project directory. That does not change.
- **Displaying secret values.** The shipped Secret Manager kind lists names,
  replication, rotation and expiry. Values belong in
  `gcloud secrets versions access`, where the read is logged against your
  identity rather than sitting in a TUI's scroll buffer or your clipboard.
  g9s does not call `AccessSecretVersion` at all, and a test fails the build if
  that changes.
- **Minting or downloading service account keys.** The keys drill-down shows a
  key's id, origin, algorithm, age and expiry — every field except the one that
  matters. `keys.list` never returns private key material and `keys.create`, the
  call that does, is not made. Creating a key is a decision with an audit trail
  attached; it does not belong behind a keypress in a read-only viewer.
