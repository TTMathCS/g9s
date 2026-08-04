package gcp

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	certificatemanager "google.golang.org/api/certificatemanager/v1"
	"google.golang.org/api/option"

	"github.com/TTMathCS/g9s/internal/config"
)

// certificateExpiryWarning is how long before expiry a certificate starts
// reading as a problem.
//
// Thirty days because that is roughly the window in which a renewal that needs
// a human — a DNS record to add, a CA to talk to, an approval — can still be
// done calmly. Inside it, the row should be amber whether or not anyone has
// noticed.
const certificateExpiryWarning = 30 * 24 * time.Hour

// CertificateLister lists Certificate Manager certificates.
//
// Certificates are the classic silent deadline: everything works perfectly
// until a date, and then nothing does. The console shows an expiry column, but
// nobody opens the console to check a date they are not already worried about,
// which is exactly why the finding has to be in the status.
//
// Google-managed certificates renew themselves, and mostly this table is
// reassurance for those. The rows that matter are the self-managed ones, which
// renew when somebody remembers, and any managed certificate stuck in a
// provisioning state — a managed certificate that never became ACTIVE is not
// protecting anything, and its expiry date is not the reason.
type CertificateLister struct{}

func (CertificateLister) Kind() Kind {
	return Kind{
		ID:    "certs",
		Title: "Certificates",
		Columns: []Column{
			{Title: "NAME", Width: 4},
			{Title: "LOCATION", Width: 2},
			{Title: "DOMAINS", Width: 5},
			{Title: "MANAGED", Width: 2},
			{Title: "EXPIRES", Width: 2},
			{Title: "STATUS", Width: 3},
		},
	}
}

func (CertificateLister) List(ctx context.Context, cfg *config.Config, p config.Project, opts []option.ClientOption) (Result, error) {
	svc, err := certificatemanager.NewService(ctx, opts...)
	if err != nil {
		return Result{}, fmt.Errorf("certificate manager client: %w", err)
	}

	// "global" is where load-balancer certificates live and is not in anyone's
	// configured region list, so it is added rather than assumed — a
	// certificate table that misses every global certificate would be
	// confidently empty on most projects.
	locations := append([]string{"global"}, cfg.Regions(p)...)

	return fanOut(ctx, locations, func(ctx context.Context, location string) (Result, error) {
		parent := fmt.Sprintf("projects/%s/locations/%s", p.ProjectID, location)

		var out Result
		err := svc.Projects.Locations.Certificates.List(parent).
			Pages(ctx, func(page *certificatemanager.ListCertificatesResponse) error {
				for _, c := range page.Certificates {
					if c != nil {
						out.Resources = append(out.Resources, certificateResource(p, location, c))
					}
				}
				return nil
			})
		return out, err
	}), nil
}

func certificateResource(p config.Project, location string, c *certificatemanager.Certificate) Resource {
	name := lastSegment(c.Name)

	return Resource{
		Name:     name,
		Location: location,
		Status:   certificateStatus(c),
		Row: []string{
			name,
			location,
			certificateDomains(c),
			certificateManaged(c),
			certificateExpiry(c),
			certificateStatus(c),
		},
		Raw: c,
		ConsoleURL: fmt.Sprintf(
			"https://console.cloud.google.com/security/ccm/list/certificates?project=%s",
			url.QueryEscape(p.ProjectID)),
	}
}

// certificateDomains is what the certificate actually covers, which is the
// only thing that says whether its expiry matters to the service you are
// looking at.
func certificateDomains(c *certificatemanager.Certificate) string {
	if len(c.SanDnsnames) > 0 {
		return clip(strings.Join(c.SanDnsnames, ", "), 60)
	}
	if c.Managed != nil && len(c.Managed.Domains) > 0 {
		return clip(strings.Join(c.Managed.Domains, ", "), 60)
	}
	return "-"
}

func certificateManaged(c *certificatemanager.Certificate) string {
	if c.Managed != nil {
		return "google"
	}
	return "self"
}

func certificateExpiry(c *certificatemanager.Certificate) string {
	t, err := time.Parse(time.RFC3339, c.ExpireTime)
	if err != nil {
		return "-"
	}
	remaining := time.Until(t)
	if remaining < 0 {
		return "expired"
	}
	return "in " + shortDuration(remaining)
}

// certificateStatus ranks expiry against provisioning state.
//
// A managed certificate that never provisioned outranks an expiry date,
// because it is not protecting anything now and the date is not why. Expired
// comes next, then the renewal window — and a self-managed certificate inside
// that window is the row this table exists for, since nothing renews it on its
// own.
func certificateStatus(c *certificatemanager.Certificate) string {
	if c.Managed != nil {
		switch strings.ToUpper(c.Managed.State) {
		case "PROVISIONING":
			return "PROVISIONING"
		case "FAILED":
			return "PROVISIONING_FAILED"
		}
	}

	t, err := time.Parse(time.RFC3339, c.ExpireTime)
	if err != nil {
		return "UNKNOWN"
	}
	switch remaining := time.Until(t); {
	case remaining < 0:
		return "EXPIRED"
	case remaining < certificateExpiryWarning:
		return "EXPIRING_SOON"
	default:
		return "ACTIVE"
	}
}
