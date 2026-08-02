# Roadmap

What g9s covers today, and what it could cover next. Nothing below is a
commitment or a schedule — it is a map of the surface area and of how much work
each piece actually is.

The shape of the work is set by two things, and the tables below give each its
own column rather than running them together.

**Scope** is where the resource lives, as GCP defines it: global, regional,
zonal, or some combination. **Requests per refresh** is what listing it
actually costs, which does not follow from the scope. A regional service
usually means one client per region, so the cost scales with your `regions`
list — but Dataflow, Cloud Functions and the Compute kinds are regional or
zonal *and* answer in a single call, because the API sweeps server-side. Those
two facts pull in opposite directions and used to share a cell.

And every kind is one file implementing a one-method interface — see
[Adding a resource kind](README.md#adding-a-resource-kind). The interface is
the reason this list is long: most entries are a day's work, not a project.

There is a second shape too: a **drill-down**, which hangs off one parent row
rather than the project, is reached with `enter`, and costs no key. Most of what
is left below wants that shape rather than another tab — see
[Tabs and drill-downs](#tabs-and-drill-downs).

## Shipped

| Resource | Scope | Requests per refresh | Notes |
|---|---|---|---|
| Compute Engine instances | zonal | 1 (aggregated) | one `aggregatedList` call covers every zone |
| Compute persistent disks | zonal + regional | 1 (aggregated) | one `aggregatedList`, the same trick the instances use. Top-level rather than a drill-down from the VM, and deliberately: the disks worth finding are the ones no VM uses, and those have no parent row to hang off — a per-instance listing is exactly the view that cannot show them. So the status column says `UNATTACHED` where the API says `READY`, and the row carries how long it has been true. "Unattached" invites the reply that it is about to be used; "unattached for 240 days" does not |
| Compute disk snapshots | global + regional | 1 | one project-wide aggregated list; regional snapshot scope is still a GCP Preview. Top-level rather than a disk drill-down because snapshots are independently billed, can outlive a deleted source disk and can restore several new disks. A parent-only view would hide the orphaned snapshots the inventory exists to find |
| Managed instance groups | zonal + regional | 1 (aggregated) | one sweep covers both scopes. The row carries target size, template, rollout mode and a derived `STABLE`/`CHANGING` state |
| Instance templates | global + regional | 1 (aggregated) | machine type, disks, NICs and accelerators without a per-template `Get`; top-level because several groups and reservations can consume one, while an unused template has no parent |
| Compute capacity reservations | zonal | 1 (aggregated) | specific reservations show machine shape, accelerators and used/total capacity. `UNUSED` and `PARTIAL` deliberately replace a generic `READY` so stranded reserved capacity is visible. This includes GPU-backed Compute reservations, not Cloud TPU's separate API |
| GKE clusters | zonal + regional | 1 (aggregated) | `parent: projects/*/locations/-` covers every zone and region in one call, same as Compute; no fan-out despite being both zonal and regional |
| Cloud SQL instances | global | 1 | one paginated `Instances.List`; the only lister whose partial failures arrive as `Warnings` in the response body rather than as an error, so unreachable regions are collected from there instead of through `fanOut` |
| Cloud Storage buckets | global | 1 | the project inventory stays cheap: one call lists buckets, while their potentially enormous object namespaces are fetched only after a bucket is opened |
| BigQuery datasets | global | 1 | one paginated call; name, location, type and labels are everything the list response carries, and anything more costs a `Get` per dataset |
| BigQuery jobs | global | 1 | jobs are project-global with the location on each row; scoped by `defaults.bigquery_job_window`, which defines the listing rather than truncating it, and then `limits.bigquery_jobs` (default 500), which does truncate it and says so |
| Dataproc clusters | regional | 1 per region | needs a client per region at `<region>-dataproc.googleapis.com`; `global` always swept |
| Dataproc jobs | regional | 1 per region | same per-region clients as the clusters; every state, newest first, `limits.dataproc_jobs_per_region` (default 200) because the API has no time filter |
| Cloud Composer environments | location | 1 per location | one client, location in the request parent |
| Dataflow jobs | regional | 1 (aggregated) | the one regional service with a server-side sweep: `projects.jobs.aggregated` covers every regional endpoint in one paginated call, so there is no fan-out and no dependence on the `regions` list — a pipeline someone launched by hand in a region nobody configured still appears. Endpoints that did not answer come back in `FailedLocation`, the same partial-listing story as GKE's missing zones. Every state, newest first, `limits.dataflow_jobs` (default 500) |
| Pub/Sub topics | global | 1 | one paginated call; the state field only appears once an ingestion source breaks, so a healthy topic reports nothing and is shown as `ACTIVE` |
| Pub/Sub subscriptions | global | 2 | the backlog column is the point, and it is not on the subscription — it comes from one Monitoring `timeSeries.list` covering every subscription at once, so an unavailable metric costs a warning rather than the table |
| Cloud Run services | regional | 1 per region | one client per region: the v2 API documents that location cannot be the `-` wildcard, so there is no aggregated call to fall back on |
| Cloud Run jobs | regional | 1 per region | same fan-out as the services; the row leads with the last execution's result, because a job whose executions all fail is still a perfectly healthy job resource |
| Cloud Scheduler jobs | regional | 1 per region | one client per region; the parent is a concrete location. The row leads with what happened last rather than what is configured, because a cron entry is not interesting for existing — it is interesting when it stopped working, and there are two ways for that which a config-only table cannot tell apart. PAUSED is the quiet one: nothing errors, nothing alerts, the work simply stops, and someone paused it for a deploy in March. The other is a job running exactly on schedule against a target that rejects every attempt, where the job's own state stays a perfectly truthful ENABLED. LAST and RESULT are the two columns that separate them |
| KMS keys | regional + global | 1 per location, then 1 per ring | keys rather than key rings, which is a choice about what the table is for: a ring is a folder, and its row would carry a name, a location and nothing anyone opens a tool to find. So the ring becomes a column and the N+1 to reach the keys is paid — the same trade service accounts make for key age. Two calls per location, bounded by `limits.kms_key_rings` (default 100), twelve at a time; one unreadable ring costs its own keys rather than the location, since `cryptoKeys.list` is a separate grant from the one that listed the rings. "global" is always swept because that is where most projects' first key ring lives. Rotation leads the row: a symmetric key with rotation never configured reports ENABLED forever, which is exactly what hides it. Asymmetric keys are explicitly *not* flagged — KMS cannot rotate one on a schedule, its public half is pinned by whoever verifies signatures, and a column of invented findings is a column nobody reads when a real one appears. **Metadata only, like Secret Manager**: no Decrypt, no AsymmetricSign, no key material, guarded by a test that fails on the call name |
| Cloud Functions | regional | 1 (aggregated) | the v2 API takes `locations/-` for the parent and sweeps every location in one paginated call, naming the ones that did not answer in `Unreachable` — the opposite of Cloud Run, in the same product family, which is why each lister says which answer it got. Both generations list together with a `GEN` column, because gen 2 is Cloud Run with a build attached and gen 1 is not: they scale, time out and bill differently, and "which generation is this" is the first question when one behaves unlike its neighbour. The trigger column separates HTTP functions, reachable by whoever IAM allows, from event-driven ones that only fire on their source |
| VPC networks | global | 1 | one call |
| Firewall rules | global | 1 | ordered by evaluation priority rather than name, because that is the only order a rule set can be reasoned about |
| VPC routes | global | 1 | network, destination, priority and next hop; a route carrying an API warning is surfaced as `DEGRADED` rather than looking healthy |
| Cloud Routers | regional | 1 (aggregated) | one sweep covers every region; the row shows both BGP shape and how many inline NAT configurations the router owns |
| Reserved static IP addresses | global + regional | 1 (aggregated) | named Address resources only, so ephemeral VM addresses are intentionally absent. `RESERVED` versus `IN_USE` makes unused allocations visible |
| Load balancers | global + regional | 2 | forwarding rules; the one kind needing two calls, as global and regional rules live in separate collections and missing the global one hides every external HTTP(S) load balancer |
| Cloud DNS zones | global | 1 | paginated, via the generated REST client |
| VPN tunnels | regional | 1 (aggregated) | `aggregatedList` sweeps every region server-side |
| Interconnect attachments | regional | 1 (aggregated) | attachments rather than circuits: a circuit being up says nothing about whether a given VPC can reach it |
| PSC service attachments | regional | 1 (aggregated) | producer side only; a consumer endpoint *is* a forwarding rule, so listing those here would double-count |
| Secret Manager secrets | global | 1 | **metadata only — never values.** One paginated call to `secrets.list`; `AccessSecretVersion` is never called, so no payload enters the process |
| Service accounts | global | 1, then 1 per account | one paginated call for the accounts, then `keys.list` per account — bounded by `limits.service_account_key_lookups` (default 200), twelve at a time. N+1 is the only shape on offer, and it is worth paying: key age is the standing audit question, and putting it on the row is the difference between a table you scan and one you have to interrogate account by account. User-managed keys only; Google-managed ones rotate themselves and would put "2 keys" on every row |

Eighteen listings hang underneath these, reached with `enter` on the row rather
than a hotkey of their own. A row may hold more than one — `tab` moves between
them the way it moves between kinds one level up:

| Drill-down | Parent | Requests to open | Notes |
|---|---|---|---|
| A VM's attached disks | an instance | 0 — already fetched | free: `aggregatedList` already returns the attachments inline. These are the attachments rather than the disks — the size and source are here, the disk's own state and idle time live on the Disk resource, which is now its own kind. Auto-delete gets both a column and the row's status, because it is the one setting on an attachment that loses data when the VM goes |
| Managed VM instances | a managed instance group | 1 | fetched only for the selected group, with pagination for both zonal and regional MIGs. The row keeps the instance's current action beside its runtime state and intended template/version, which is the context a flat VM list loses |
| Storage objects and folders | a bucket | 1+ per page | query-shaped by design rather than a capped full-bucket sweep. Directory mode sends the current prefix plus `/` as the delimiter, so the service returns immediate objects and child prefixes; `enter` changes the prefix, `:cd` jumps directly, and `:find` uses the API's server-side glob. The default UI page is 500 combined rows and preserves `nextPageToken` for explicit continuation with `space`; the iterator may make another request to fill 500 when Cloud Storage returns a short service page. A `+` on the count makes incompleteness visible without treating an ordinary next page as a warning |
| Bucket lifecycle rules | a bucket | 0 — already fetched | free — the buckets listing already carries them. Two questions a bucket row cannot touch: "why did my data disappear" is usually a Delete rule nobody remembered, and "why is nothing being archived" is usually a SetStorageClass rule that was never added or whose condition never matches. Delete is the only irreversible action and is the only one coloured. Rule order is kept rather than sorted, because GCS evaluates every matching rule and the written order is how the set is edited |
| Dataproc jobs on a cluster | a cluster | 1 | one call, and *cheaper* than the parent kind rather than more expensive: `ListJobs` takes a `ClusterName` filter, so this is one region rather than a fan-out across every configured one. The axis you are already on when a cluster rather than a job is the thing behaving oddly. `limits.cluster_jobs` (default 200) |
| GKE node pools | a cluster | 0 — already fetched | free: `clusters.list` already returns node pools inline. Node counts are multiplied out across the pool's zones, because a pool of 2 across three zones runs six VMs and reading the 2 as the total is how a cluster ends up mis-sized on paper |
| Cloud SQL databases | an instance | 1 | one call. The first half of the pair that made a row allowed more than one listing |
| Cloud SQL users | an instance | 1 | one call. `tab` moves between this and the databases; a disabled account still appears in the list, which is exactly the row worth colouring — it looks like access that exists and is not |
| Load balancer backend health | a forwarding rule | 4+ | the expensive one, and the argument for the whole mechanism: rule → target proxy → URL map → every backend service it routes to → `getHealth` per backend group. Four-plus round trips to answer for one row, which nobody would pay on every refresh of the load balancers table and everybody would pay once, on the rule they are actually looking at. `limits.backend_groups` (default 40) bounds *requests* rather than rows, so raising it costs latency |
| Subnets | a VPC network | 1 (aggregated) | one `aggregatedList` covers every region server-side, then filtered to this network. Filtered on the last URL segment rather than the whole self-link: the two references come back from different calls and the API is not consistent about the host or api-version prefix it writes, so comparing full strings silently matches nothing and renders as a network with no subnets. Secondary ranges are shown named, because "which one is pods" is the question a GKE range is opened for |
| Cloud NAT gateways | a Cloud Router | 0 — already fetched | free: NAT configurations are inline entries on the router, not independent Compute resources. The drill-down exposes public/private type, automatic/manual IP allocation, selected source ranges, minimum ports and logging without duplicating the router as another top-level row |
| BigQuery tables | a dataset | 1 | one paginated call, `limits.bigquery_tables` (default 1000). No row counts or byte sizes — `tables.list` does not return them and a `Get` per table would be thousands of calls for a listing that is mostly scrolled past. The cost question it *can* answer without them is the one that bites: whether a partitioned table requires a filter, which is the difference between a query reading one day and one reading four years |
| Cloud Run revisions | a service | 1 | one call, joined with the parent. The revisions come from the list; the traffic split is a field on the *service*, so answering "which revision is actually serving" takes both halves. A service row can say READY while the revision serving all its traffic is three deploys old, which is most of "I deployed but nothing changed" |
| Subscriptions on a topic | a topic | 1 | one call, filtered — deliberately not `topics.subscriptions.list`, which returns names only and would need a `Get` each to fill a row. A topic with no subscriptions says so as a warning rather than rendering an empty table that reads as a failed call: anything published to it is discarded, and the topics table cannot show that |
| DNS record sets | a zone | 1 | one paginated call, `limits.dns_record_sets` (default 1000). Grouped by name rather than sorted flat, so a name's A and AAAA sit on adjacent rows — they are read as a group |
| Secret versions | a secret | 1 | **metadata only, exactly like the parent.** `versions.list` returns names, states and timestamps; `AccessSecretVersion` is not called here either, and the test that guarantees it now scans every file in the package rather than just `secrets.go` — the single-file scan could have been sidestepped by adding one, which is precisely what this drill-down did. What it adds: the secrets row shows the rotation *policy*, this shows whether rotation actually happened. A secret set to rotate every 30 days whose newest enabled version is eight months old looks perfectly healthy one level up |
| Cloud Run job executions | a job | 1 | one call. The jobs row leads with the *last* execution's result, which answers "is this broken now" and nothing at all about "how long has it been broken" — one failure is an incident, the same failure every night for a week is a different conversation. The task tally separates one bad shard from a whole run collapsing |
| Service account keys | an account | 0 — already fetched | free: the accounts listing fetched them to compute the oldest-key age. Oldest first — that is the row the table was opened to find |

Plus, across all kinds: a per-project dashboard with status rollups, a merged
*All Resources* table, filtering, describe-as-YAML, Console/Airflow deep links,
clipboard yank over OSC 52, and SSH to a running VM.

Thirty-four kinds is well past what the digits cover, so the hotkey sequence
continues into letters — `1`-`9`, then `b c e f h i m n t u v w x z`, then shift
for `A` through `Z`, skipping every letter already bound to an action. Each
kind's key is printed beside it on the dashboard and in the tab strip, and
`tab`/`shift+tab`, `0`/`a` and `:<kind>` still reach everything. The strip
scrolls around the active tab and marks hidden tabs with `‹`/`›`.

## Tabs and drill-downs

Lowercase ran out at twenty-three. That is the whole usable alphabet: twelve
letters are actions, fourteen are kinds, and the digits carry the first nine.
The twenty-third key came from folding `c` (open in Cloud Console) into `o`
(open), which did the same thing on every kind but Composer — the last
redundancy there was to spend.

The run then continues into shift: `A` through `Z`, minus `G` (jump to bottom)
and `L` (log in without a browser). Forty-seven keys, all still one press, all
still printed beside the kind. Extending the scheme mechanically beat making the
twenty-fourth kind a special case reachable only by typing `:<kind>`.

That is a keyspace, not a target. A dashboard with forty rows is not a good
dashboard, and the kinds still worth having are mostly not project-wide lists at
all. Node pools belong to a cluster; keys belong to an account; record sets
belong to a zone. "Every node pool in the project", stripped of which cluster
each is in, is not a question anyone asks. What changed when the run reached
shift is only that the keyspace stopped being the thing deciding which kinds are
allowed to exist — the question is now whether a kind reads better as a tab or
as a drill-down, which is the question it should have been all along.

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

A forty-eighth top-level kind would silently lose its hotkey, so a test fails
the build instead — the reminder to reach for a drill-down.

## Next up

The ones that would earn their place first, roughly in order.

Everything here is a drill-down, and that is not a coincidence — see above.

| Resource | Parent | Why it's near the top |
|---|---|---|
| Roles held by a service account | an account | one `getIamPolicy`, filtered to the member. "What can this thing actually do" is the other half of the key-age question, and nothing in the tool answers it yet |

## Candidates

Plausible, lower priority, grouped by area.

**Compute and serverless** — the Compute Engine inventory planned in this
roadmap is shipped.
Still open are Batch jobs and Cloud TPU queued resources/reservations, which are
separate services rather than Compute Engine collections.

**Data** — Cloud SQL and BigQuery are shipped, down to databases, users and
tables. Still open:
Bigtable instances, Spanner instances and databases, Memorystore
(Redis / Memcached), Firestore databases, Datastream streams, Data Fusion
instances, Artifact Registry repositories, BigQuery reservations.

**Networking** — the core is shipped, including record sets, backend health,
subnets, routes, Cloud Routers, Cloud NAT and reserved static IPs.

**Security and identity** — service accounts and KMS keys are shipped. Still
open: Certificate Manager certificates, Binary Authorization policies, VPC
Service Controls perimeters, Org Policy constraints in effect. Project IAM
policy bindings are the awkward one and the reason they are not done yet: a
binding is a (role, member) pair, not a resource with a location and a status,
so it fits `Resource` badly and would want a table shaped differently from
every other kind.

**Operations** — Cloud Scheduler is shipped. Still open: Monitoring alert
policies and which are firing, Error Reporting groups, Cloud Tasks queues,
Cloud Build history. Cloud Logging is deliberately not on this list: log
entries are an unbounded, query-driven stream with no location or status axis,
so they are not a `Lister` and pretending otherwise would produce a table that
lies about what it contains.

**Cost and quota** — quota usage against limits per service and region, and
current-month spend per project. Both are genuinely awkward rather than merely
unstarted: spend needs a billing export plenty of projects have never set up,
and quota comes back as nested consumer-quota metrics rather than as resources
with a name and a state.

## Row caps are settings now

Nine listings stop at a cap. Each was a constant compiled into its lister, which
made the bound the tool's opinion rather than the reader's: a project with 3,000
BigQuery jobs in the window saw 500 and a footer warning, and there was nothing
to do about it. They now live in `defaults.limits`, documented in
[Row limits](README.md#row-limits), and can be raised, lowered, or removed with
`-1`.

The defaults did not change, so upgrading changes nothing on its own. What
changed is who decides.

Three of the nine bound *requests* rather than rows — backend health does one
`getHealth` per group, service accounts one `keys.list` per account, KMS one
`cryptoKeys.list` per ring. Raising those costs latency and quota rather than
memory, which is a different trade and is called out per key.

Two limits are deliberately not settings. Secret Manager and KMS are metadata
only: names, versions, rotation, expiry, never the value or the key material.
That is not a cap waiting to be raised, it is the point of listing them at all —
`gcloud secrets versions access` is audit-logged and is the right way to read a
secret. Both are guarded by tests that fail on the call name, and both guards
were verified by injecting a violation and watching them fail.

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
