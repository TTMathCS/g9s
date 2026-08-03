package ui

import (
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
)

// Adaptive colours so the tool is legible on light and dark terminals.
var (
	colorAccent = lipgloss.AdaptiveColor{Light: "#1a56db", Dark: "#7aa2f7"}
	colorMuted  = lipgloss.AdaptiveColor{Light: "#6b7280", Dark: "#8b93a7"}
	colorGood   = lipgloss.AdaptiveColor{Light: "#047857", Dark: "#9ece6a"}
	colorWarn   = lipgloss.AdaptiveColor{Light: "#b45309", Dark: "#e0af68"}
	colorBad    = lipgloss.AdaptiveColor{Light: "#b91c1c", Dark: "#f7768e"}
	colorText   = lipgloss.AdaptiveColor{Light: "#111827", Dark: "#c0caf5"}
)

var (
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#ffffff")).
			Background(colorAccent).
			Padding(0, 1)

	crumbStyle = lipgloss.NewStyle().Foreground(colorMuted).Padding(0, 1)

	tabActiveStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorAccent).
			Underline(true).
			Padding(0, 1)

	tabStyle = lipgloss.NewStyle().Foreground(colorMuted).Padding(0, 1)

	headerRowStyle = lipgloss.NewStyle().Bold(true).Foreground(colorMuted)

	selectedRowStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("#ffffff")).
				Background(colorAccent)

	rowStyle = lipgloss.NewStyle().Foreground(colorText)

	mutedStyle = lipgloss.NewStyle().Foreground(colorMuted)
	goodStyle  = lipgloss.NewStyle().Foreground(colorGood)
	warnStyle  = lipgloss.NewStyle().Foreground(colorWarn)
	badStyle   = lipgloss.NewStyle().Foreground(colorBad)

	helpKeyStyle  = lipgloss.NewStyle().Bold(true).Foreground(colorAccent)
	helpDescStyle = lipgloss.NewStyle().Foreground(colorMuted)

	panelStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorMuted).
			Padding(0, 1)
)

// statusStyle colours a resource status the way an operator reads it: green is
// running, amber is in flight, red needs attention.
func statusStyle(status string) lipgloss.Style {
	switch strings.ToUpper(status) {
	// Each API spells "healthy" its own way: RUNNABLE is Cloud SQL's, ESTABLISHED
	// is a VPN tunnel that finished its handshake, ENABLED is a live firewall rule.
	// DONE is BigQuery's word for a job that finished; a job that finished
	// badly reports FAILED instead, which is why DONE is unambiguously green.
	// SUCCEEDED is the Cloud Run execution that came back clean. DRAINED is a
	// Dataflow job that was asked to stop and finished what it had in flight
	// first, and UPDATED is one replaced by a newer version of itself — both
	// are clean endings rather than interruptions.
	// HEALTHY is a load balancer backend passing its health check, which is the
	// one word the backend health table exists to show.
	case "RUNNING", "RUNNABLE", "ACTIVE", "READY", "ESTABLISHED", "ENABLED", "DONE",
		"SUCCEEDED", "DRAINED", "UPDATED", "HEALTHY", "STABLE", "IN_USE", "GRANTED":
		return goodStyle
	case "PROVISIONING", "STAGING", "CREATING", "UPDATING", "STOPPING", "DELETING",
		"REPAIRING", "SUSPENDING", "RECONCILING", "RECREATING", "VERIFYING",
		"ABANDONING", "REFRESHING", "STARTING", "RESUMING", "RESTARTING",
		"CREATING_WITHOUT_RETRIES", "CHANGING", "UPLOADING",
		// Cloud SQL's in-flight and maintenance states.
		"PENDING_CREATE", "PENDING_DELETE", "MAINTENANCE",
		// A BigQuery job that has been admitted but is waiting on slots, and a
		// Dataproc job between submission and its driver starting.
		"PENDING", "SETUP_DONE", "CANCEL_PENDING", "CANCEL_STARTED",
		// A secret with an expiry still ahead of it: not broken, but it and
		// every version of it will be deleted on that date.
		"EXPIRING",
		// VPN tunnels mid-handshake or being torn down.
		"WAITING_FOR_FULL_CONFIG", "FIRST_HANDSHAKE", "ALLOCATING_RESOURCES", "DEPROVISIONING",
		// Interconnect attachments waiting on the other party.
		"PARTNER_REQUEST_RECEIVED", "PENDING_CUSTOMER", "PENDING_PARTNER",
		// Something that exists but is doing nothing: a firewall rule that looks
		// like it protects an network but does not is worth drawing the eye to.
		"DISABLED",
		// A detached Pub/Sub subscription still exists and still costs nothing,
		// but it retains nothing and delivers nothing — publishers carry on
		// unaware, which is exactly the failure worth flagging.
		"DETACHED",
		// A service account carrying a user-managed key past its rotation
		// window. Nothing is broken, which is the problem: IAM reports the same
		// state whether the key is a week old or three years old.
		"STALE_KEY",
		// Dataflow jobs on their way somewhere. DRAINING is a deliberate
		// shutdown that is still processing what it has in flight.
		"DRAINING", "CANCELLING", "QUEUED", "RESOURCE_CLEANING_UP",
		// A persistent disk no instance is using. The API calls it READY, which
		// is true and is exactly what hides it: it bills every month at the
		// full rate for storage nothing reads. Amber rather than red because it
		// is a cost, not an outage — and because deleting one is irreversible.
		"UNATTACHED",
		// Reserved capacity and addresses that exist but are not fully consumed.
		// They are cost/capacity findings rather than service failures.
		"UNUSED", "PARTIAL", "RESERVED", "RESERVING",
		// A Cloud Function mid-deploy, and a disk being rebuilt from a snapshot.
		"DEPLOYING", "RESTORING",
		// A KMS key that cannot rotate because rotation was never configured,
		// or whose scheduled rotation date has passed. Amber, not red: nothing
		// is broken and nothing is down — it is the key that will still be the
		// same key in three years, which is the whole point of the column.
		"ROTATION_OFF", "ROTATION_OVERDUE",
		// A Cloud Scheduler job that is not running. Nothing errors and nothing
		// alerts; the work just stops. That is why it is coloured at all.
		"PAUSED",
		// An IAM binding with a condition is a real grant, but only while its
		// expression holds. Amber keeps it distinct from unconditional access.
		"CONDITIONAL",
		// An Artifact Registry repository nothing prunes, or one whose cleanup
		// policy is configured, reads as configured, and deletes nothing. Every
		// CI build pushes an image, and the bill grows where nobody looks.
		"NO_CLEANUP", "CLEANUP_DRY_RUN",
		// A basic-tier Memorystore instance: one node, no replica, no failover.
		// Working exactly as designed, and a surprise to whoever put state in it.
		"NO_REPLICA",
		// Memorystore with AUTH off. Reachable only inside the VPC, which is
		// why it gets left off — and why it trusts every workload on it.
		"NO_AUTH",
		// A Spanner database with drop protection off: the default, and the
		// only thing standing between it and a one-command deletion.
		"NO_DROP_PROTECTION",
		// A Bigtable instance created as DEVELOPMENT: one node, no SLA, no
		// replication. A deliberate choice for a sandbox, a discovery in prod.
		"DEVELOPMENT",
		// A Firestore database with no delete protection, or no point-in-time
		// recovery. Neither is on by default and neither reports anything when
		// off, so the row is the only place either becomes visible.
		"NO_DELETE_PROTECTION", "NO_PITR",
		// A Memcached instance whose own state is READY while some of its nodes
		// are not serving. The instance row says everything is fine.
		"NODES_DOWN":
		return warnStyle
	case "ERROR", "FAILED", "TERMINATED", "STOPPED", "SUSPENDED", "UNHEALTHY", "DEGRADED",
		// Compute routes rejected by their backend or dropped at a route limit.
		"INACTIVE", "DROPPED",
		// VPN tunnels that will never carry traffic without intervention.
		"AUTHORIZATION_ERROR", "NEGOTIATION_FAILURE", "NETWORK_ERROR", "NO_INCOMING_PACKETS", "REJECTED",
		// Interconnect attachments that are broken or never came up.
		"DEFUNCT", "UNPROVISIONED",
		// A Dataproc job that was killed or never got off the ground, and a
		// secret whose expiry has passed — it and its versions are gone.
		"CANCELLED", "ATTEMPT_FAILURE", "EXPIRED",
		// A Pub/Sub topic whose ingestion source stopped working: the topic is
		// fine, nothing is arriving on it.
		"RESOURCE_ERROR", "INGESTION_RESOURCE_ERROR",
		// A secret version that has been destroyed. The row still lists, and
		// anything still pinned to that version is already broken.
		"DESTROYED",
		// A bucket lifecycle rule that deletes. Not a failure — it is the rule
		// working — but it is the only irreversible one, and the row someone
		// scanning for "where did my data go" needs to find first.
		"DELETE",
		// A disk whose backing storage the service cannot reach. Anything using
		// it is already having a bad time.
		"UNAVAILABLE",
		// A scheduled job whose last attempt was rejected. The job's own state
		// stays ENABLED throughout, which is true and useless.
		"LAST_RUN_FAILED":
		return badStyle
	default:
		return rowStyle
	}
}

// sanitizeLine makes an API-supplied string safe to render on one line:
// control characters are dropped, and newlines and tabs become spaces.
//
// This is not cosmetic. Everything on screen — resource names, statuses, bucket
// labels, Airflow URIs, API warnings, error strings — arrives from a GCP API
// response, and a terminal executes escape sequences in whatever it is handed.
// A row cell carrying an ESC byte can repaint the screen, move the cursor,
// relabel the window title, or on terminals with the feature enabled, push text
// back onto stdin. Stripping the control range means a hostile or merely
// mangled value can be ugly but not active.
func sanitizeLine(s string) string {
	return sanitizeControl(s, false)
}

// sanitizeBlock is sanitizeLine for multi-line content: the detail pane's YAML
// keeps its newlines and tabs, and loses everything else.
func sanitizeBlock(s string) string {
	return sanitizeControl(s, true)
}

// isControl reports whether r is a control character: the C0 range and DEL,
// plus the C1 range, which is where a bare 0x9b is a CSI introducer all by
// itself on terminals that decode it.
func isControl(r rune) bool {
	return r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f)
}

func sanitizeControl(s string, keepBreaks bool) string {
	// The overwhelmingly common case is a clean string, and this runs on every
	// cell of every frame, so check before building anything.
	//
	// Invalid UTF-8 counts as unclean even with no control rune in sight: a
	// stray 0x9b byte does not decode to U+009B, so IndexFunc walks straight
	// past it, and a terminal in 8-bit mode reads it as a CSI introducer.
	// Rebuilding turns it into U+FFFD, which is inert.
	if utf8.ValidString(s) && strings.IndexFunc(s, isControl) < 0 {
		return s
	}

	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r == '\n' || r == '\t':
			if keepBreaks {
				b.WriteRune(r)
			} else {
				b.WriteRune(' ')
			}
		case isControl(r):
			// Dropped, not replaced: a run of them should not stretch a column.
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// truncate shortens s to width display cells, marking the cut with an
// ellipsis. Cells, not runes: a CJK character occupies two columns, and
// counting runes would let one wide name push every column after it out of
// alignment.
//
// Every cell, header, crumb, footer and flash string in the UI goes through
// here, which makes it the one place worth sanitizing at. Nothing already
// styled is ever passed in — styles are applied to the result, never before —
// so stripping escapes here cannot eat the tool's own colours.
func truncate(s string, width int) string {
	if width <= 0 {
		return ""
	}
	s = sanitizeLine(s)
	if lipgloss.Width(s) <= width {
		return s
	}

	var b strings.Builder
	used := 0
	for _, r := range s {
		w := runewidth.RuneWidth(r)
		if used+w > width-1 {
			break
		}
		b.WriteRune(r)
		used += w
	}
	return b.String() + "…"
}

// pad right-pads s to width, truncating when it does not fit.
func pad(s string, width int) string {
	s = truncate(s, width)
	if n := width - lipgloss.Width(s); n > 0 {
		return s + strings.Repeat(" ", n)
	}
	return s
}
