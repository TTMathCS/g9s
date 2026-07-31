package gcp

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"cloud.google.com/go/compute/apiv1/computepb"
	"google.golang.org/api/option"

	"github.com/TTMathCS/g9s/internal/config"
)

// AttachedDiskLister is the disks attached to one VM instance.
//
// Costs no API call: aggregatedList already returns each instance's disks
// inline, so the VM row is carrying them. Same argument as node pools — the
// data arrives either way, and "every attached disk in the project", stripped
// of which instance each belongs to, is not a question anyone asks.
//
// These are the *attachments*, not the disks. The size and the source are here;
// the disk's own state, its snapshot schedule and whether anything else is
// attached to it live on the Disk resource, which is a listing of its own and
// has nowhere to bind while the hotkey alphabet is full.
type AttachedDiskLister struct{}

func (AttachedDiskLister) ParentKind() string { return "vm" }

func (AttachedDiskLister) Kind() Kind {
	return Kind{
		ID:    "vmdisks",
		Title: "Disks",
		Columns: []Column{
			{Title: "DEVICE", Width: 3},
			{Title: "SOURCE", Width: 4},
			{Title: "SIZE", Width: 1},
			{Title: "TYPE", Width: 2},
			{Title: "MODE", Width: 1},
			{Title: "ON DELETE", Width: 2},
			{Title: "ENCRYPTION", Width: 2},
		},
	}
}

func (AttachedDiskLister) List(_ context.Context, _ *config.Config, p config.Project, parent Resource, _ []option.ClientOption) (Result, error) {
	instance, ok := parent.Raw.(*computepb.Instance)
	if !ok {
		return Result{}, fmt.Errorf("no instance data for %s", parent.Name)
	}

	var result Result
	for _, d := range instance.GetDisks() {
		result.Resources = append(result.Resources, attachedDiskResource(p, instance, d))
	}
	// Attachment order, not name order: index 0 is the boot disk and the rest
	// are in the order the guest sees them, which is how they are referred to.
	return result, nil
}

func attachedDiskResource(p config.Project, inst *computepb.Instance, d *computepb.AttachedDisk) Resource {
	// The device name is what the guest sees at /dev/disk/by-id/google-*, which
	// is the name someone reading a df output is holding.
	name := d.GetDeviceName()
	if name == "" {
		name = lastSegment(d.GetSource())
	}
	if name == "" {
		name = fmt.Sprintf("disk-%d", d.GetIndex())
	}

	// A local SSD has no source disk — it is created and destroyed with the
	// instance, and a dash is more honest than an empty cell.
	source := lastSegment(d.GetSource())
	if source == "" {
		source = "-"
	}

	size := "-"
	if gb := d.GetDiskSizeGb(); gb > 0 {
		size = fmt.Sprintf("%dGB", gb)
	}

	return Resource{
		Name:     name,
		Location: parent0Zone(inst),
		Status:   attachedDiskStatus(d),
		Row: []string{
			name,
			source,
			size,
			diskKind(d),
			strings.ToLower(dashIfEmpty(d.GetMode())),
			deleteBehaviour(d),
			diskEncryption(d),
		},
		Raw: d,
		ConsoleURL: fmt.Sprintf(
			"https://console.cloud.google.com/compute/disksDetail/zones/%s/disks/%s?project=%s",
			url.PathEscape(parent0Zone(inst)), url.PathEscape(source),
			url.QueryEscape(p.ProjectID)),
	}
}

// parent0Zone is the instance's zone, which every attachment shares.
func parent0Zone(inst *computepb.Instance) string {
	return lastSegment(inst.GetZone())
}

// diskKind separates the boot disk from the rest, and names a local SSD for
// what it is.
//
// PERSISTENT with no source is scratch: a local SSD vanishes when the instance
// stops, and a row that looks like an ordinary data disk is how someone puts
// state on one.
func diskKind(d *computepb.AttachedDisk) string {
	if d.GetType() == "SCRATCH" {
		return "local ssd"
	}
	if d.GetBoot() {
		return "boot"
	}
	return "data"
}

// deleteBehaviour is the field that decides whether deleting the VM takes the
// disk with it. It is the one setting on an attachment that destroys data.
func deleteBehaviour(d *computepb.AttachedDisk) string {
	if d.GetAutoDelete() {
		return "delete"
	}
	return "keep"
}

// diskEncryption reports a customer-managed key, if there is one. The key name
// stands in for the full path, which is far too long for a column.
func diskEncryption(d *computepb.AttachedDisk) string {
	key := d.GetDiskEncryptionKey()
	if key == nil {
		return "google-managed"
	}
	if name := key.GetKmsKeyName(); name != "" {
		return lastSegment(name)
	}
	// A raw or RSA-wrapped key is customer-supplied rather than customer-managed:
	// Google never stores it, so the disk cannot be read without it being handed
	// over on every attach.
	return "customer-supplied"
}

// attachedDiskStatus flags the attachment worth a second look.
//
// An attachment has no lifecycle of its own, so the status says the thing that
// costs data: a disk set to delete with its instance. READ_ONLY is worth
// marking too — a disk shared read-only between instances is deliberate, and a
// surprise if it is not.
func attachedDiskStatus(d *computepb.AttachedDisk) string {
	if d.GetType() == "SCRATCH" {
		return "EPHEMERAL"
	}
	if d.GetAutoDelete() {
		return "AUTO_DELETE"
	}
	if strings.EqualFold(d.GetMode(), "READ_ONLY") {
		return "READ_ONLY"
	}
	return "ACTIVE"
}
