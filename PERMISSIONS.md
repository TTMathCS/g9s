# Required APIs and IAM permissions

What to enable and what to grant so g9s can read a project. Every entry is
derived from the API call the lister actually makes, not from a general
description of the service.

**Almost everything here is read-only.** Every permission in the tables below
is a `list`, `get` or `getIamPolicy`, and none of them permits a change.

The exception is the three VM power actions, which are **not** granted by any
of the above and have their own section: [Actions](#actions). If you do not
grant those, g9s still works completely — the actions simply fail with a
permission error, which is a perfectly reasonable way to run it.

## The short version

For a support engineer who should see everything g9s can show, one predefined
role covers almost all of it:

```sh
gcloud projects add-iam-policy-binding PROJECT_ID \
  --member="user:someone@example.com" \
  --role="roles/viewer"
```

`roles/viewer` covers most of what g9s reads. Two things it reliably does not
cover, and a third that depends on your organization:

| Gap | Add | Why it is separate |
|---|---|---|
| KMS keys | `roles/cloudkms.viewer` | Cloud KMS is excluded from the basic roles by design. Key metadata only; never key material |
| Service account keys | a custom role with `iam.serviceAccountKeys.list` (see below) | No predefined read-only role grants key listing |
| Secret Manager | `roles/secretmanager.viewer` | Grant it if the Secrets table reports `permission denied`. **Never** `roles/secretmanager.secretAccessor` — that grants secret *values*, which g9s does not read and does not need |

Exactly which permissions `roles/viewer` carries varies with the service and
changes over time, so treat the list above as the common case rather than the
whole truth. The reliable check is the tool itself: any table that reports
`permission denied` is naming a grant you are missing, and the per-kind tables
below say which one. An empty table and an unreadable one never look alike.

### About service account keys

Listing a service account's keys needs `iam.serviceAccountKeys.list`. No
predefined *read-only* role grants it — `roles/iam.serviceAccountKeyAdmin`
does, but it also permits creating and deleting keys, which is exactly the
authority a read-only tool should not be the reason to hand out. Use a custom
role instead:

```sh
gcloud iam roles create g9sServiceAccountKeyReader --project=PROJECT_ID \
  --title="g9s service account key reader" \
  --permissions=iam.serviceAccountKeys.list
```

Without it, the Service Accounts table still lists accounts; the key-age column
is blank and a warning names how many accounts could not be read. That is a
reasonable state to run in — key age is an audit signal, not a prerequisite.

## Enabling the APIs

A disabled API is not an error in g9s: the kind reports nothing and stays
quiet, because most services are unused in most projects and a warning on every
refresh would bury the real ones. The cost is that "nothing here" and "never
enabled" look the same, so enable what you expect to browse.

```sh
gcloud services enable --project=PROJECT_ID \
  compute.googleapis.com \
  container.googleapis.com \
  sqladmin.googleapis.com \
  storage.googleapis.com \
  bigquery.googleapis.com
```

## Top-level kinds

| Kind | API to enable | Permissions |
|---|---|---|
| VM Instances | `compute.googleapis.com` | `compute.instances.list` |
| Compute Disks | `compute.googleapis.com` | `compute.disks.list` |
| Disk Snapshots | `compute.googleapis.com` | `compute.snapshots.list`, `compute.regionSnapshots.list` |
| Managed Instance Groups | `compute.googleapis.com` | `compute.instanceGroupManagers.list`, `compute.regionInstanceGroupManagers.list` |
| Instance Templates | `compute.googleapis.com` | `compute.instanceTemplates.list`, `compute.regionInstanceTemplates.list` |
| Compute Reservations | `compute.googleapis.com` | `compute.reservations.list` |
| GKE Clusters | `container.googleapis.com` | `container.clusters.list` |
| Cloud SQL Instances | `sqladmin.googleapis.com` | `cloudsql.instances.list` |
| Storage Buckets | `storage.googleapis.com` | `storage.buckets.list` |
| BigQuery Datasets | `bigquery.googleapis.com` | `bigquery.datasets.get` |
| BigQuery Jobs | `bigquery.googleapis.com` | `bigquery.jobs.list` (all users: `bigquery.jobs.listAll`) |
| BigQuery Reservations | `bigqueryreservation.googleapis.com` | `bigquery.reservations.list` |
| Dataproc Clusters | `dataproc.googleapis.com` | `dataproc.clusters.list` |
| Dataproc Jobs | `dataproc.googleapis.com` | `dataproc.jobs.list` |
| Composer Environments | `composer.googleapis.com` | `composer.environments.list` |
| Dataflow Jobs | `dataflow.googleapis.com` | `dataflow.jobs.list` |
| Spanner Instances | `spanner.googleapis.com` | `spanner.instances.list` |
| Bigtable Instances | `bigtableadmin.googleapis.com` | `bigtable.instances.list` |
| Firestore Databases | `firestore.googleapis.com` | `datastore.databases.list` |
| Memorystore Redis | `redis.googleapis.com` | `redis.instances.list` |
| Memorystore Memcached | `memcache.googleapis.com` | `memcache.instances.list` |
| Datastream Streams | `datastream.googleapis.com` | `datastream.streams.list` |
| Data Fusion | `datafusion.googleapis.com` | `datafusion.instances.list` |
| Artifact Repositories | `artifactregistry.googleapis.com` | `artifactregistry.repositories.list` |
| Pub/Sub Topics | `pubsub.googleapis.com` | `pubsub.topics.list` |
| Pub/Sub Subscriptions | `pubsub.googleapis.com` + `monitoring.googleapis.com` | `pubsub.subscriptions.list`, `monitoring.timeSeries.list` (backlog) |
| Cloud Run Services | `run.googleapis.com` | `run.services.list` |
| Cloud Run Jobs | `run.googleapis.com` | `run.jobs.list` |
| Cloud Functions | `cloudfunctions.googleapis.com` | `cloudfunctions.functions.list` |
| Scheduler Jobs | `cloudscheduler.googleapis.com` | `cloudscheduler.jobs.list` |
| Cloud Build | `cloudbuild.googleapis.com` | `cloudbuild.builds.list` |
| Task Queues | `cloudtasks.googleapis.com` | `cloudtasks.queues.list` |
| Alert Policies | `monitoring.googleapis.com` | `monitoring.alertPolicies.list` |
| Batch Jobs | `batch.googleapis.com` | `batch.jobs.list` |
| Cloud TPUs | `tpu.googleapis.com` | `tpu.nodes.list` |
| Error Groups | `clouderrorreporting.googleapis.com` | `errorreporting.groups.list` |
| Certificates | `certificatemanager.googleapis.com` | `certificatemanager.certs.list` |
| IAM Bindings | `cloudresourcemanager.googleapis.com` | `resourcemanager.projects.getIamPolicy` |
| VPC Networks | `compute.googleapis.com` | `compute.networks.list` |
| Firewall Rules | `compute.googleapis.com` | `compute.firewalls.list` |
| VPC Routes | `compute.googleapis.com` | `compute.routes.list` |
| Cloud Routers | `compute.googleapis.com` | `compute.routers.list` |
| Reserved IP Addresses | `compute.googleapis.com` | `compute.addresses.list`, `compute.globalAddresses.list` |
| Load Balancers | `compute.googleapis.com` | `compute.forwardingRules.list`, `compute.globalForwardingRules.list` |
| Cloud DNS Zones | `dns.googleapis.com` | `dns.managedZones.list` |
| VPN Tunnels | `compute.googleapis.com` | `compute.vpnTunnels.list` |
| Interconnect Attachments | `compute.googleapis.com` | `compute.interconnectAttachments.list` |
| PSC Service Attachments | `compute.googleapis.com` | `compute.serviceAttachments.list` |
| Secret Manager Secrets | `secretmanager.googleapis.com` | `secretmanager.secrets.list` — **not** `secretmanager.versions.access` |
| Service Accounts | `iam.googleapis.com` | `iam.serviceAccounts.list` |
| KMS Keys | `cloudkms.googleapis.com` | `cloudkms.keyRings.list`, `cloudkms.cryptoKeys.list` |

## Drill-downs

A drill-down opens from a parent row. Several need no permission of their own
because the data already arrived on the parent response — those are marked
*(no extra call)*, and they work wherever the parent does.

| Drill-down | Opens from | Permissions |
|---|---|---|
| Disks | VM Instances | *(no extra call)* |
| Managed Instances | Managed Instance Groups | `compute.instanceGroupManagers.list`, `compute.regionInstanceGroupManagers.list` |
| Objects | Storage Buckets | `storage.objects.list` |
| Lifecycle | Storage Buckets | *(no extra call)* |
| Jobs | Dataproc Clusters | `dataproc.jobs.list` |
| Node Pools | GKE Clusters | *(no extra call)* |
| Subscriptions | Pub/Sub Topics | `pubsub.topics.getSubscriptions` |
| Databases | Cloud SQL Instances | `cloudsql.databases.list` |
| Users | Cloud SQL Instances | `cloudsql.users.list` |
| Tables | BigQuery Datasets | `bigquery.tables.list` |
| Databases | Spanner Instances | `spanner.databases.list` |
| Clusters | Bigtable Instances | `bigtable.clusters.list` |
| Revisions | Cloud Run Services | `run.revisions.list` |
| Executions | Cloud Run Jobs | `run.executions.list` |
| Subnets | VPC Networks | `compute.subnetworks.list` |
| NAT Gateways | Cloud Routers | *(no extra call)* |
| Backend Health | Load Balancers | `compute.backendServices.get`, `compute.backendServices.getHealth` |
| Record Sets | Cloud DNS Zones | `dns.resourceRecordSets.list` |
| Versions | Secret Manager Secrets | `secretmanager.versions.list` — **not** `.access` |
| Keys | Service Accounts | `iam.serviceAccountKeys.list` (see above) |
| Project Roles | Service Accounts | `resourcemanager.projects.getIamPolicy` |

## Actions

g9s can start, stop and reset a VM. Nothing else it does changes anything, and
these are the only permissions on this page that are not read-only.

| Action | Permission |
|---|---|
| `:start` | `compute.instances.start` |
| `:stop` | `compute.instances.stop` |
| `:reset` | `compute.instances.reset` |

`roles/compute.instanceAdmin.v1` covers all three, but it also grants create
and **delete**, which g9s never uses and which no read-mostly console should be
the reason to hand out. The narrow grant:

```sh
gcloud iam roles create g9sInstancePower --project=PROJECT_ID \
  --title="g9s instance power actions" \
  --permissions=compute.instances.start,compute.instances.stop,compute.instances.reset
```

Granting none of these is a supported configuration and the right default for
anyone piloting g9s as an inventory tool. Every table still works; the actions
report a permission error if attempted.

## Outside the resource tables

| Feature | Needs |
|---|---|
| Login (`l` / `L`) | Nothing in IAM; gcloud performs the OAuth flow |
| Credential check | A live token exchange against the ADC file; no project permission |
| SSH to a VM (`s`) | `compute.instances.osLogin` or a project SSH key, plus firewall access to port 22 or IAP TCP forwarding. This is the one action that is not a read |
| Open in Console (`o`) | Nothing from g9s; the browser authenticates separately |
| Cross-project sweep (`:fleet` / `:diff`) | Nothing extra — it runs the same listings against each configured project, so it needs whatever those kinds need, in every project you want an answer for |
| Terraform overlay (`:tf`) | `storage.objects.list` and `storage.objects.get` on the **state bucket**, which is often not one of the buckets `roles/viewer` on the project already covers |

The Terraform overlay is worth a second look before you grant it. A state file
carries every value the provider round-trips — database passwords, generated
keys, TLS material — so read access to a state bucket is a far broader grant
than read access to the resources it describes.

g9s reads only the resource type and name out of each state file and drops the
attributes before anything else in the process can see them, guarded by a test
that fails if a value from `attributes` survives parsing. That is a property of
g9s, not of the grant: anyone holding `storage.objects.get` on that bucket can
read the whole file by other means. Grant it to the bucket, not the project:

```sh
gcloud storage buckets add-iam-policy-binding gs://STATE_BUCKET \
  --member="user:someone@example.com" \
  --role="roles/storage.objectViewer"
```

Not granting it is a supported configuration. `:tf` then reports a permission
error and every other table is unaffected.

## Checking what you have

`g9s doctor` verifies that credentials exist and mint a token for the expected
identity, but it does not enumerate per-kind permissions — that answer only
comes from the APIs themselves. The direct check:

```sh
gcloud projects test-iam-permissions PROJECT_ID \
  --permissions=compute.instances.list,container.clusters.list,cloudsql.instances.list
```

In the TUI, the same answer arrives per kind: a table that says
`permission denied` for a scope is telling you exactly which grant is missing.
