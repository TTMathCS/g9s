# Review notes — August 2026

This review began against the five-kind MVP and was refreshed after the larger
resource and drill-down expansion landed on `main`. The source now registers
34 top-level kinds and 18 child listings. The architectural recommendations
below still apply to the next major axis -- cross-project inventory and
comparison -- while resolved implementation findings are called out separately.

The rest below is behaviour worth a decision, not doc drift.

## Architecture and team-readiness

### Verdict

g9s is a credible read-only MVP and a good basis for a controlled pilot with a
small number of trusted GCP support engineers. The current boundary is also the
right one: it is an inventory and navigation tool, not yet a general production
operations console or a replacement for Cloud Console, Terraform or incident
tooling.

Do not add broad mutating actions or a fleet-wide comparison screen directly
to the current UI state model. The implementation is intentionally optimized
for **one project across many resource kinds**. Cross-project work adds the
other axis -- **one resource kind across many project contexts** -- and that
axis belongs in the domain model first.

### What is already strong

- Per-project credential directories avoid mutating the user's normal gcloud
  configuration and let several short-lived support identities coexist.
- Typed Google clients perform resource discovery; gcloud is limited to the
  interactive operations for which it is useful, currently login and SSH.
- The projects -> dashboard -> resources -> drill-down hierarchy is easy to
  understand and fits k9s muscle memory.
- Regional fan-out keeps successful scopes and surfaces unavailable scopes,
  rather than making a partial result look complete.
- Read-only API behavior is an excellent initial safety boundary.
- The repository already has unit and rendering tests, race-enabled CI,
  vulnerability scanning, security documentation and an explicit roadmap.
- The `Lister` and `ChildLister` interfaces separate project-wide inventory
  from resources that belong under one parent row.
- The Storage Objects browser establishes a useful exception for query-shaped
  data: one child listing can retain a path/glob plus an explicit continuation
  token without turning billions of objects into dashboard inventory. Reuse
  that paged-query boundary for similarly large namespaces; do not change
  ordinary finite resource listers into partial pages by default.

### Backend decision: keep the hybrid model

Keep typed Google API clients as the inventory, detail and future mutation
backend. Keep `gcloud` for authentication and terminal-native interactive
workflows such as SSH and IAP tunnelling.

A command typed by a person starts one `gcloud` process. The process count only
becomes an architectural issue if the dashboard itself uses `gcloud` as its
backend: loading many resource kinds would require one or more CLI invocations
per kind, or strictly serial loading that makes refreshes slower. It would also
replace typed responses with JSON parsing and make cancellation, partial
results, pagination and consistent error handling harder. The existing hybrid
boundary gets the strengths of both approaches without requiring g9s to
reimplement interactive authentication or terminal behavior.

The one temporary exception is the project-wide disk-snapshot sweep. Regional
snapshot scope is still a GCP Preview and the stable generated Go client does
not yet expose the aggregated method, so g9s uses the same authenticated Google
transport with a small local response envelope. Replace that shim with the
generated v1 method when it becomes available; it is not a reason to move the
rest of the inventory to `gcloud` or hand-written HTTP.

### The architectural pressure point

Today the UI has one active project, the cache is keyed only by resource kind,
and a `Resource` has no project or credential-context identity. `Resource.Row`
is display-oriented positional data, while resource-specific behavior such as
SSH and Airflow navigation is recovered by type-asserting `Resource.Raw`.

That is sufficient for the existing flow, but comparison, export, drift and
actions need stable domain data. Fleet mode also multiplies work as:

```text
projects x resource kinds x regions
```

The current refresh tokens prevent stale results from replacing newer ones,
but they do not cancel the old requests. Without a shared coordinator, a fleet
refresh could create a large burst of clients and API calls and leave obsolete
requests running after the user changes scope.

### Recommended target model

Introduce four concepts before building fleet UI or mutations.

1. **Context catalog.** Treat a selectable entry as an operational context,
   not only a project ID. Give it an environment, stack/group and stable
   credential profile in addition to its display name and project ID.

   ```yaml
   contexts:
     - name: data-dev
       project_id: acme-data-dev
       environment: dev
       stack: data-platform
       credential_profile: data-support
   ```

   Separating a credential profile from a context would let one support
   identity serve several projects without duplicate logins, while retaining
   project-level isolation where policy requires it. Keep a migration path from
   the current `projects` configuration rather than breaking it at once.

2. **Stable resource identity.** Give every resource a key containing context,
   project, kind, location and resource name. Replace `Row []string` as the
   primary model with typed, named fields and render table rows from those
   fields. This is the foundation for reliable comparison, sorting, exporting,
   actions and future Terraform overlays.

3. **Inventory coordinator.** Cache snapshots by `(context, kind)`. A snapshot
   should include resources, fetch time, complete/partial/failed state,
   structured scope errors and the credential identity used. Run fleet fetches
   through bounded concurrency with cancellation, per-context deadlines and
   visible progress such as `7/12 projects loaded`.

4. **Action framework.** Providers should declare the actions they support and
   their preconditions. An action receives one exact resource key and context,
   produces a preview, requests confirmation, executes, and returns an
   auditable result. The UI should not need to know which protobuf type happens
   to be SSH-capable.

### Cross-environment UI

Use two complementary views. The default fleet view should be a conventional
vertical table because it survives normal terminal widths and makes filtering
and actions unambiguous:

```text
ENV   PROJECT       CLUSTER       REGION       STATE      IMAGE
dev   data-dev      etl-main      us-central1  RUNNING    2.2
uat   data-uat      etl-main      us-central1  RUNNING    2.2
prod  data-prod     etl-main      us-central1  UPDATING   2.1
```

A horizontal comparison mode can then pivot one logical resource across
environments:

```text
FIELD       DEV             UAT             PROD
state       RUNNING         RUNNING         UPDATING
image       2.2             2.2             2.1
workers     2               2               8
region      us-central1     us-central1     us-central1
```

Corresponding resources must be matched by an explicit label or configured
logical identifier. Do not silently infer identity by stripping `-dev`, `-uat`
or `-prod` from names; that will eventually compare unrelated resources.

In the comparison view, the cursor should identify one exact environment and
resource. Bulk operations across environments should come later and require
explicit multi-selection plus a confirmation summary. Avoid arbitrary shell
command broadcasting; expose typed, resource-appropriate actions instead.

### Before a support-team pilot

Remaining:

- Smoke-test against dedicated dev/uat projects with expired credentials,
  missing APIs, partial IAM access, empty projects and slow regional responses.
  This one needs real projects and cannot be done from the repository.

Done:

- ~~Make listing completeness and failures structured rather than relying on
  warning strings.~~ `gcp.Warning` carries a scope, a reason and a detail;
  `Result.Complete()` answers whether a listing is the whole picture and
  `Result.Incomplete(reason)` separates a permission to request from a setting
  to raise. Displayed text is derived from the type, so there is one source of
  truth rather than a sentence and a fact that can disagree.
- ~~Compare the configured account with the account read from the ADC file.~~
  A live token minted for an unexpected identity is refused, and the actual
  identity is shown in the header.
- ~~Apply the REST permission-error mapping.~~
- ~~Validate the permissions of pre-existing credential directories.~~
- ~~Add a `g9s doctor` command.~~
- ~~Document required APIs and least-privilege IAM permissions per resource
  kind.~~ See [PERMISSIONS.md](PERMISSIONS.md). Per-action permissions remain
  open, and become relevant only once mutations exist.
- ~~Complete and verify the first release produced by the binary pipeline.~~
  Every merge to `main` publishes checksummed, provenance-attested archives for
  four platforms.

For future mutations, keep production visually unmistakable, show the exact
account/project/resource and operation in the confirmation, require stronger
confirmation for destructive actions, disable bulk actions by default and
record action outcomes. Prefer direct GCP clients for typed mutations and keep
gcloud for genuinely interactive workflows such as SSH.

The recommended order is therefore: pilot the current read-only experience,
make context a first-class domain dimension, add fleet inventory and comparison,
then introduce a narrow action framework. That sequence avoids forcing project,
comparison and mutation concerns into the root Bubble Tea model at once.

## Inconsistencies

None outstanding.

## Resolved since the first review

- The help screen lists `] / [` alongside `tab / shift+tab`. A test now asserts
  that every key the model binds appears in the panel, so the next binding
  cannot be added without being documented.
- Panics in the goroutines g9s starts itself are recovered and reported as that
  scope's warning. bubbletea restores the terminal when a panic reaches the
  command goroutine it started, but recover does not cross goroutines, so a
  fan-out leg used to kill the process outright and leave the terminal in raw
  mode on the alternate screen.
- Oversized clipboard copies are refused rather than silently dropped. The
  check measures the encoded OSC 52 sequence against `clipboard_limit`; it
  previously measured the raw text against a limit eight times larger than a
  stock xterm accepts.
- REST providers report authorization failures in the same words as gRPC ones.
- Pre-existing credential directories are permission-checked by `g9s doctor`,
  not only newly created ones.
- `g9s doctor` checks config, gcloud, proxy and loopback reachability,
  credential permissions and live per-project identity without the TUI.
- The binary pipeline publishes a verified release on every merge to `main`.

- Switching resource kinds now consistently clears the previous kind's
  filter, including tab and hotkey navigation.
- `fanOut` now keeps rows returned before a later-page failure and attaches the
  failure as a warning.
- Listing caps moved from compiled constants into `defaults.limits`; reaching
  one is visible in the footer.
- The release workflow now builds checksummed, provenance-attested archives.
  The remaining work is to complete and verify the first release.

## Sharp edges

**OSC 52 has terminal-side limits.** `y` on a detail view pipes the whole
YAML through one escape sequence. tmux swallows it unless `set-clipboard` is
on, and several terminals cap the payload (a few KB in some), so a big VM
describe can silently copy nothing. Worth a line in the README, or flash a
warning past ~8 KB.

**REST errors miss the permission-denied mapping.** `describeFailure` only
maps gRPC codes. Storage and Composer surface `*googleapi.Error` on HTTP
paths, so a 403 there shows as a raw truncated message instead of
"permission denied". An `errors.As` on `*googleapi.Error` next to the gRPC
switch closes it.

## Cheap wins, in order

1. **Verify the first prebuilt release.** The workflow is present; exercise the
   download, checksum, provenance and macOS Gatekeeper instructions end to end.
2. **Re-check all projects from the picker.** `r` re-checks one; after a
   morning of expired sessions you want all ten. `R` for the lot.
3. **Add `[` / `]` to the help screen** so the visible help matches the keys
   accepted by `handleResourcesKey`.
