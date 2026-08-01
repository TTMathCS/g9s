package gcp

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	iam "google.golang.org/api/iam/v1"
	"google.golang.org/api/option"

	"github.com/TTMathCS/g9s/internal/config"
)

const (
	// staleKeyAge is when a user-managed key stops being a key and starts being
	// a finding. Google's own guidance is to rotate at ninety days; past that
	// the row is worth drawing the eye to even though nothing is broken.
	staleKeyAge = 90 * 24 * time.Hour

	// maxKeyLookups bounds the per-account key listing. There is no aggregated
	// call — keys.list is one request per service account — so a project with
	// hundreds of accounts would otherwise turn one refresh into hundreds of
	// requests. The accounts past the cap still list; only their key columns go
	// unanswered, and the listing says so.

	// keyLookupConcurrency is how many of those requests are in flight at once.
	// High enough that a hundred accounts do not serialise into a visible
	// stall, low enough not to look like a burst to IAM's quota.
	keyLookupConcurrency = 12
)

// ServiceAccountLister lists service accounts and how old their keys are.
//
// Key age is the standing audit question, and it is not on the account: keys
// come from a separate call, one per account, with no aggregated form. So this
// pays N+1 requests — bounded and concurrent — to put the answer in the table
// rather than making you drill into every account to find the one bad row.
//
// The keys themselves hang off the account as a drill-down. That listing costs
// nothing extra: it reads what this one already fetched.
//
// Only user-managed keys are counted. Google-managed keys rotate on their own
// and are never downloadable, so their age is not a finding, and including them
// would put "2 keys" on every account in the project and hide the real ones.
type ServiceAccountLister struct{}

func (ServiceAccountLister) Kind() Kind {
	return Kind{
		ID:    "sa",
		Title: "Service Accounts",
		Columns: []Column{
			{Title: "NAME", Width: 5},
			{Title: "DISPLAY NAME", Width: 4},
			{Title: "KEYS", Width: 1},
			{Title: "OLDEST KEY", Width: 2},
			{Title: "STATUS", Width: 2},
		},
	}
}

func (ServiceAccountLister) List(ctx context.Context, cfg *config.Config, p config.Project, opts []option.ClientOption) (Result, error) {
	svc, err := iam.NewService(ctx, opts...)
	if err != nil {
		return Result{}, fmt.Errorf("iam client: %w", err)
	}

	var accounts []*iam.ServiceAccount
	err = svc.Projects.ServiceAccounts.List("projects/"+p.ProjectID).
		Pages(ctx, func(page *iam.ListServiceAccountsResponse) error {
			accounts = append(accounts, page.Accounts...)
			return nil
		})
	if err != nil {
		return Result{}, err
	}

	// Deterministic order before the cap, so which accounts lose their key
	// columns does not change between refreshes.
	sort.SliceStable(accounts, func(i, j int) bool { return accounts[i].Email < accounts[j].Email })

	keys, warnings := accountKeys(ctx, svc, accounts, cfg.LimitServiceAccountKeyLookups())

	var result Result
	result.Warnings = warnings
	for _, a := range accounts {
		result.Resources = append(result.Resources, serviceAccountResource(p, a, keys[a.Name]))
	}
	sortResources(result.Resources)
	return result, nil
}

// accountKeys fetches every account's keys concurrently, bounded.
//
// A failure here costs the key columns of one account, not the listing: an
// account whose keys cannot be read is far more common than a broken project —
// iam.serviceAccountKeys.list is a separate permission from the one that let
// you list the accounts in the first place.
func accountKeys(ctx context.Context, svc *iam.Service, accounts []*iam.ServiceAccount, maxKeyLookups int) (map[string][]*iam.ServiceAccountKey, []string) {
	var warnings []string
	if len(accounts) > maxKeyLookups {
		warnings = append(warnings, fmt.Sprintf(
			"key ages read for the first %d of %d accounts — the rest show ? rather than a number nobody checked",
			maxKeyLookups, len(accounts)))
		accounts = accounts[:maxKeyLookups]
	}

	var (
		mu     sync.Mutex
		keys   = make(map[string][]*iam.ServiceAccountKey, len(accounts))
		denied int
		wg     sync.WaitGroup
		sem    = make(chan struct{}, keyLookupConcurrency)
	)

	for _, a := range accounts {
		wg.Add(1)
		go func(a *iam.ServiceAccount) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			// User-managed only: Google-managed keys are rotated by Google and
			// cannot be exported, so their age answers no question here.
			resp, err := svc.Projects.ServiceAccounts.Keys.List(a.Name).
				KeyTypes("USER_MANAGED").Context(ctx).Do()

			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				denied++
				return
			}
			// Always non-nil on success, so an account with no keys is
			// distinguishable from one whose keys were never read.
			keys[a.Name] = append([]*iam.ServiceAccountKey{}, resp.Keys...)
		}(a)
	}
	wg.Wait()

	if denied > 0 {
		warnings = append(warnings, fmt.Sprintf(
			"keys could not be read for %d of %d accounts — needs iam.serviceAccountKeys.list",
			denied, len(accounts)))
	}
	return keys, warnings
}

func serviceAccountResource(p config.Project, a *iam.ServiceAccount, keys []*iam.ServiceAccountKey) Resource {
	// The email's local part is the name people use; the domain is the same on
	// every row and would cost half the column to say nothing.
	name := a.Email
	if i := strings.Index(name, "@"); i > 0 {
		name = name[:i]
	}
	if name == "" {
		name = lastSegment(a.Name)
	}

	display := a.DisplayName
	if display == "" {
		display = "-"
	}

	oldest, haveKeys := oldestKeyAge(keys)

	// A nil slice is "never read" — the lookup was denied or past the cap — and
	// is not the same answer as an account with no keys. Rendering it as 0
	// would clear an account that may well be carrying a five-year-old key.
	count, oldestCell := "?", "?"
	if keys != nil {
		count, oldestCell = fmt.Sprintf("%d", len(keys)), "-"
		if haveKeys {
			oldestCell = shortDuration(oldest)
		}
	}

	status := serviceAccountStatus(a, oldest, haveKeys)

	return Resource{
		Name:     name,
		Location: "global",
		Status:   status,
		Row: []string{
			name,
			display,
			count,
			oldestCell,
			status,
		},
		// The account and its keys together: the describe pane shows both, and
		// the keys drill-down reads this rather than making its own call.
		Raw: &ServiceAccountDetail{Account: a, Keys: keys},
		ConsoleURL: fmt.Sprintf(
			"https://console.cloud.google.com/iam-admin/serviceaccounts/details/%s?project=%s",
			url.PathEscape(a.UniqueId), url.QueryEscape(p.ProjectID)),
	}
}

// serviceAccountStatus is the row's one-word answer.
//
// STALE_KEY is g9s's word, not the API's: IAM has no opinion about key age, and
// an account with a two-year-old downloadable key reports exactly the same
// state as one with none. Surfacing that is the reason this kind exists, so the
// status says it rather than leaving it to whoever reads the age column.
//
// DISABLED wins over it, because a disabled account cannot authenticate at all
// and the age of a key it cannot use is the smaller problem.
func serviceAccountStatus(a *iam.ServiceAccount, oldest time.Duration, haveKeys bool) string {
	if a.Disabled {
		return "DISABLED"
	}
	if haveKeys && oldest >= staleKeyAge {
		return "STALE_KEY"
	}
	return "ACTIVE"
}

// oldestKeyAge is how long ago the longest-lived key was minted.
func oldestKeyAge(keys []*iam.ServiceAccountKey) (time.Duration, bool) {
	var oldest time.Duration
	found := false
	for _, k := range keys {
		if k == nil || k.ValidAfterTime == "" {
			continue
		}
		t, err := time.Parse(time.RFC3339, k.ValidAfterTime)
		if err != nil {
			continue
		}
		if d := time.Since(t); !found || d > oldest {
			oldest, found = d, true
		}
	}
	return oldest, found
}

// ServiceAccountDetail is what a service account row carries: the account as
// IAM returned it, plus the user-managed keys listed for it.
//
// Exported because it is what the describe pane renders and what the keys
// drill-down reads. There is no private key material in it — keys.list never
// returns any, and keys.create, the one call that does, is not called here.
type ServiceAccountDetail struct {
	Account *iam.ServiceAccount      `json:"account"`
	Keys    []*iam.ServiceAccountKey `json:"keys"`
}

// ServiceAccountKeyLister is the keys behind one service account.
//
// Costs no API call: the accounts listing already fetched them, because the age
// of the oldest key has to be on the parent row for the table to be worth
// opening. This drill-down is where the individual keys, their ages and their
// expiry become visible.
type ServiceAccountKeyLister struct{}

func (ServiceAccountKeyLister) ParentKind() string { return "sa" }

func (ServiceAccountKeyLister) Kind() Kind {
	return Kind{
		ID:    "sakeys",
		Title: "Keys",
		Columns: []Column{
			{Title: "KEY ID", Width: 5},
			{Title: "ORIGIN", Width: 2},
			{Title: "ALGORITHM", Width: 2},
			{Title: "AGE", Width: 2},
			{Title: "EXPIRES", Width: 2},
			{Title: "STATUS", Width: 2},
		},
	}
}

func (ServiceAccountKeyLister) List(_ context.Context, _ *config.Config, p config.Project, parent Resource, _ []option.ClientOption) (Result, error) {
	detail, ok := parent.Raw.(*ServiceAccountDetail)
	if !ok {
		return Result{}, fmt.Errorf("no key data for %s", parent.Name)
	}

	var result Result
	for _, k := range detail.Keys {
		if k == nil {
			continue
		}
		result.Resources = append(result.Resources, serviceAccountKeyResource(p, parent, k))
	}
	sortKeysByAge(result.Resources)
	return result, nil
}

func serviceAccountKeyResource(p config.Project, parent Resource, k *iam.ServiceAccountKey) Resource {
	id := lastSegment(k.Name)

	status := "ACTIVE"
	switch {
	case k.Disabled:
		status = "DISABLED"
	case keyExpired(k):
		status = "EXPIRED"
	}

	return Resource{
		Name:     id,
		Location: "global",
		Status:   status,
		Row: []string{
			id,
			// USER_PROVIDED means someone uploaded their own public key, which
			// is a different provenance question from a key Google minted and
			// handed over as a downloadable JSON file.
			strings.TrimPrefix(k.KeyOrigin, "KEY_ORIGIN_"),
			strings.TrimPrefix(k.KeyAlgorithm, "KEY_ALG_"),
			age(k.ValidAfterTime),
			expiryCell(k.ValidBeforeTime),
			status,
		},
		Raw:        k,
		ConsoleURL: parent.ConsoleURL,
	}
}

// expiryCell renders when a key stops working. The API writes year 9999 for a
// key that never expires, which renders as a nonsense age unless it is named.
func expiryCell(validBefore string) string {
	if validBefore == "" {
		return "-"
	}
	t, err := time.Parse(time.RFC3339, validBefore)
	if err != nil {
		return validBefore
	}
	if t.Year() >= 9999 {
		return "never"
	}
	if d := time.Until(t); d > 0 {
		return shortDuration(d)
	}
	return "expired"
}

func keyExpired(k *iam.ServiceAccountKey) bool {
	if k.ValidBeforeTime == "" {
		return false
	}
	t, err := time.Parse(time.RFC3339, k.ValidBeforeTime)
	if err != nil {
		return false
	}
	return time.Now().After(t)
}

// sortKeysByAge puts the oldest key first: it is the one the table was opened
// to find.
func sortKeysByAge(resources []Resource) {
	sort.SliceStable(resources, func(i, j int) bool {
		vi, vj := keyValidAfter(resources[i]), keyValidAfter(resources[j])
		if vi != vj {
			return vi < vj
		}
		return resources[i].Name < resources[j].Name
	})
}

func keyValidAfter(r Resource) string {
	k, ok := r.Raw.(*iam.ServiceAccountKey)
	if !ok {
		return ""
	}
	return k.ValidAfterTime
}
