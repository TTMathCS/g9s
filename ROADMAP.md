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

## Shipped

| Resource | Scope | Notes |
|---|---|---|
| Compute Engine instances | zonal, aggregated | one `aggregatedList` call covers every zone |
| GKE clusters | zonal + regional, aggregated | `parent: projects/*/locations/-` covers every zone and region in one call, same as Compute; no fan-out despite being both zonal and regional |
| Cloud SQL instances | global | one paginated `Instances.List`; the only lister whose partial failures arrive as `Warnings` in the response body rather than as an error, so unreachable regions are collected from there instead of through `fanOut` |
| Cloud Storage buckets | global | the simplest lister here — one call, no fan-out, no aggregation trick needed |
| Dataproc clusters | **regional** | needs a client per region at `<region>-dataproc.googleapis.com`; `global` always swept |
| Cloud Composer environments | location-scoped | one client, location in the request parent |
| VPC networks | global | one call |
| Firewall rules | global | ordered by evaluation priority rather than name, because that is the only order a rule set can be reasoned about |
| Load balancers | global + regional | forwarding rules; the one kind needing two calls, as global and regional rules live in separate collections and missing the global one hides every external HTTP(S) load balancer |
| Cloud DNS zones | global | paginated, via the generated REST client |
| VPN tunnels | regional, aggregated | `aggregatedList` sweeps every region server-side |
| Interconnect attachments | regional, aggregated | attachments rather than circuits: a circuit being up says nothing about whether a given VPC can reach it |
| PSC service attachments | regional, aggregated | producer side only; a consumer endpoint *is* a forwarding rule, so listing those here would double-count |

Plus, across all kinds: a per-project dashboard with status rollups, a merged
*All Resources* table, filtering, describe-as-YAML, Console/Airflow deep links,
clipboard yank over OSC 52, and SSH to a running VM.

Thirteen kinds is past what the number keys cover: `1`-`9` reach the first
nine, while `tab`/`shift+tab`, `0`/`a` and `:<kind>` reach all of them. The tab
strip scrolls around the active tab and marks hidden tabs with `‹`/`›`. Further
kinds are cheap to add mechanically, but the next ones are probably better
shaped as drill-downs than as more top-level tabs — GKE node pools under a
cluster, DNS record sets under a zone.

## Next up

The ones that would earn their place first, roughly in order.

| Resource | Scope | Why it's near the top |
|---|---|---|
| BigQuery datasets and recent jobs | global | where the data actually is; jobs answer "what is running" |
| Pub/Sub topics and subscriptions | global | subscription backlog is the number people want |
| Secret Manager secrets | global | **names and versions only, never values** |
| Cloud Run services and jobs | regional | replaces a lot of "is it deployed" Console trips |
| Dataproc **jobs** | regional | clusters without jobs only tells half the story |
| Dataflow jobs | regional | |
| Service accounts and their keys | global | key age is a standing audit question |
| GKE node pools | per-cluster | one call per cluster once you know which clusters exist; a natural drill-down from the GKE row rather than another top-level tab |

## Candidates

Plausible, lower priority, grouped by area.

**Compute and serverless** — Cloud Functions, Batch jobs, instance groups and
templates, Compute disks and snapshots, GPU/TPU reservations.

**Data** — Bigtable instances, Spanner instances and databases, Memorystore
(Redis / Memcached), Firestore databases, Datastream streams, Data Fusion
instances, Artifact Registry repositories, BigQuery reservations.

**Networking** — the core is shipped (see above). Still open: subnets, routes,
Cloud NAT and routers, backend services with their health, DNS record sets,
reserved static IPs.

**Security and identity** — project IAM policy bindings, KMS keyrings and keys
(with rotation age), Certificate Manager certificates, Binary Authorization
policies, VPC Service Controls perimeters, Org Policy constraints in effect.

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
- **Displaying secret values.** Secret Manager support means names, versions
  and rotation age. Values belong in `gcloud secrets versions access`, where
  the access is logged against your identity and not sitting in a TUI's scroll
  buffer or your clipboard.
