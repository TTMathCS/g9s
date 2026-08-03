package gcp

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	cloudkms "google.golang.org/api/cloudkms/v1"
	"google.golang.org/api/option"

	"github.com/TTMathCS/g9s/internal/config"
)

// maxKeyRings bounds the per-location N+1. A project with more key rings than
// this in one location is not a project anyone is auditing from a table.

// keyRingConcurrency is how many rings are read at once within a location.
// Matches the service-account key lookup: enough to hide the latency, not
// enough to look like an attack on the API.
const keyRingConcurrency = 12

// KMSKeyLister lists Cloud KMS crypto keys.
//
// Keys rather than key rings, which is a deliberate choice about what the table
// is for. A ring is a folder: its row would carry a name, a location and
// nothing anyone opens a tool to find. The key is where the audit question
// lives, so the ring becomes a column and the N+1 to get there is paid — the
// same trade service accounts make for key age, and for the same reason.
//
// Two calls per location, then: one for the rings, one per ring for its keys.
// Neither the ring nor the key listing takes a `-` wildcard for location — the
// API documents the parent as `projects/*/locations/*` — so this fans out the
// way Cloud Run and Dataproc do, and always includes "global", which is where
// most projects' first key ring lives.
//
// Never touches key material. `Decrypt`, `AsymmetricSign` and the raw key bytes
// are not reachable from a listing and are not called here: this is metadata,
// exactly like the Secret Manager kind. The rotation columns are the point.
type KMSKeyLister struct{}

func (KMSKeyLister) Kind() Kind {
	return Kind{
		ID:    "kms",
		Title: "KMS Keys",
		Columns: []Column{
			{Title: "NAME", Width: 4},
			{Title: "LOCATION", Width: 2},
			{Title: "KEY RING", Width: 3},
			{Title: "PURPOSE", Width: 3},
			{Title: "ROTATION", Width: 2},
			{Title: "NEXT", Width: 2},
			{Title: "LEVEL", Width: 2},
			{Title: "STATE", Width: 3},
		},
	}
}

func (KMSKeyLister) List(ctx context.Context, cfg *config.Config, p config.Project, opts []option.ClientOption) (Result, error) {
	locations := cfg.KMSLocations(p)

	svc, err := cloudkms.NewService(ctx, opts...)
	if err != nil {
		return Result{}, fmt.Errorf("kms client: %w", err)
	}

	return fanOut(ctx, locations, func(ctx context.Context, location string) (Result, error) {
		return kmsKeysIn(ctx, svc, p, location, cfg.LimitKMSKeyRings())
	}), nil
}

// kmsKeysIn lists every key in one location, ring by ring.
func kmsKeysIn(ctx context.Context, svc *cloudkms.Service, p config.Project, location string, maxKeyRings int) (Result, error) {
	var (
		out   Result
		rings []string
	)

	parent := fmt.Sprintf("projects/%s/locations/%s", p.ProjectID, location)
	err := svc.Projects.Locations.KeyRings.List(parent).
		Pages(ctx, func(page *cloudkms.ListKeyRingsResponse) error {
			for _, r := range page.KeyRings {
				if r != nil && r.Name != "" {
					rings = append(rings, r.Name)
				}
			}
			return nil
		})
	if err != nil {
		return out, err
	}
	if len(rings) == 0 {
		return out, nil
	}
	if len(rings) > maxKeyRings {
		out.Warnings = append(out.Warnings, fmt.Sprintf(
			"%s: keys read for the first %d of %d key rings", location, maxKeyRings, len(rings)))
		rings = rings[:maxKeyRings]
	}

	var (
		mu     sync.Mutex
		wg     sync.WaitGroup
		sem    = make(chan struct{}, keyRingConcurrency)
		denied int
	)
	for _, ring := range rings {
		wg.Add(1)
		go func(ring string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			var keys []Resource
			err := safely(ring, func() error {
				return svc.Projects.Locations.KeyRings.CryptoKeys.List(ring).
					Pages(ctx, func(page *cloudkms.ListCryptoKeysResponse) error {
						for _, k := range page.CryptoKeys {
							if k != nil {
								keys = append(keys, cryptoKeyResource(p, location, ring, k))
							}
						}
						return nil
					})
			})

			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				// One unreadable ring is not a failed location:
				// cloudkms.cryptoKeys.list is a separate grant from the one
				// that listed the rings, and a ring locked down harder than
				// its neighbours is the normal shape of a real project.
				denied++
				return
			}
			out.Resources = append(out.Resources, keys...)
		}(ring)
	}
	wg.Wait()

	if denied > 0 {
		out.Warnings = append(out.Warnings, fmt.Sprintf(
			"%s: keys could not be read for %d of %d key rings — needs cloudkms.cryptoKeys.list",
			location, denied, len(rings)))
	}
	return out, nil
}

func cryptoKeyResource(p config.Project, location, ring string, k *cloudkms.CryptoKey) Resource {
	name := lastSegment(k.Name)
	ringName := lastSegment(ring)

	return Resource{
		Name:     name,
		Location: location,
		Status:   keyState(k),
		Row: []string{
			name,
			location,
			ringName,
			shortPurpose(k.Purpose),
			keyRotation(k),
			keyNextRotation(k),
			keyProtectionLevel(k),
			keyState(k),
		},
		Raw: k,
		ConsoleURL: fmt.Sprintf(
			"https://console.cloud.google.com/security/kms/key/manage/%s/%s/%s?project=%s",
			url.PathEscape(location), url.PathEscape(ringName), url.PathEscape(name),
			url.QueryEscape(p.ProjectID)),
	}
}

// rotatable reports whether automatic rotation is even available for a key.
//
// Only symmetric purposes can rotate on a schedule. An asymmetric signing key
// has a public half that verifiers have pinned, so KMS will not rotate it for
// you and never intended to. Flagging those as unrotated would fill the column
// that matters with false findings, which is how a real one stops being read.
func rotatable(purpose string) bool {
	switch purpose {
	case "ENCRYPT_DECRYPT", "RAW_ENCRYPT_DECRYPT", "MAC":
		return true
	default:
		return false
	}
}

// keyState leads with the rotation finding, falling back to the primary
// version's own state.
//
// A key that cannot rotate reports the same ENABLED as one rotating every
// ninety days, which is precisely what a table full of ENABLED hides.
func keyState(k *cloudkms.CryptoKey) string {
	if rotatable(k.Purpose) {
		if k.RotationPeriod == "" {
			return "ROTATION_OFF"
		}
		if overdue(k.NextRotationTime) {
			return "ROTATION_OVERDUE"
		}
	}
	if k.Primary != nil && k.Primary.State != "" {
		return k.Primary.State
	}
	// Asymmetric keys have no primary version — every version is addressed
	// explicitly by whoever signs with it — so there is no state to report.
	return "-"
}

// overdue reports whether a scheduled rotation time has already passed.
func overdue(next string) bool {
	if next == "" {
		return false
	}
	t, err := time.Parse(time.RFC3339, next)
	if err != nil {
		return false
	}
	return t.Before(time.Now())
}

// keyRotation renders the rotation period, which the API gives as a seconds
// string like "7776000s" — a number nobody reads as ninety days.
func keyRotation(k *cloudkms.CryptoKey) string {
	if !rotatable(k.Purpose) {
		// Not "never": the distinction between "cannot" and "was not set up" is
		// the whole difference between a finding and a fact.
		return "n/a"
	}
	if k.RotationPeriod == "" {
		return "never"
	}
	d, err := time.ParseDuration(k.RotationPeriod)
	if err != nil {
		return k.RotationPeriod
	}
	return shortDuration(d)
}

// keyNextRotation says when the next rotation is due, or how overdue it is.
func keyNextRotation(k *cloudkms.CryptoKey) string {
	if !rotatable(k.Purpose) || k.NextRotationTime == "" {
		return "-"
	}
	t, err := time.Parse(time.RFC3339, k.NextRotationTime)
	if err != nil {
		return "-"
	}
	if d := time.Until(t); d > 0 {
		return "in " + shortDuration(d)
	}
	return shortDuration(time.Since(t)) + " ago"
}

func keyProtectionLevel(k *cloudkms.CryptoKey) string {
	if k.VersionTemplate != nil && k.VersionTemplate.ProtectionLevel != "" {
		return strings.ToLower(k.VersionTemplate.ProtectionLevel)
	}
	if k.Primary != nil && k.Primary.ProtectionLevel != "" {
		return strings.ToLower(k.Primary.ProtectionLevel)
	}
	return "-"
}

// shortPurpose trims the noise off a purpose constant. ENCRYPT_DECRYPT is the
// common case and reads fine; the asymmetric ones are long enough to push the
// rotation columns off a narrow terminal.
func shortPurpose(purpose string) string {
	switch purpose {
	case "":
		return "-"
	case "ENCRYPT_DECRYPT":
		return "symmetric"
	case "RAW_ENCRYPT_DECRYPT":
		return "raw symmetric"
	case "ASYMMETRIC_SIGN":
		return "sign"
	case "ASYMMETRIC_DECRYPT":
		return "asym decrypt"
	case "MAC":
		return "mac"
	default:
		return strings.ToLower(purpose)
	}
}
