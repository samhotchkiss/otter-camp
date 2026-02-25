package browser

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/url"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var defaultDomainDenylist = []string{
	"accounts.google.com",
	"login.microsoftonline.com",
	"*.auth0.com",
	"*.okta.com",
	"signin.aws.amazon.com",
	"console.aws.amazon.com",
	"*.stripe.com",
	"*.paypal.com",
	"*.braintreegateway.com",
	"*.checkout.com",
	"*.klarna.com",
}

type BrowserPolicy struct {
	Allowlist []string `json:"allowlist"`
	Denylist  []string `json:"denylist"`
}

type OrganizationPolicyReader interface {
	GetBrowserPolicy(ctx context.Context, organizationID uuid.UUID) (BrowserPolicy, error)
}

type DomainPolicyChecker interface {
	IsAllowed(ctx context.Context, organizationID uuid.UUID, rawURL string) bool
}

type organizationPolicyRepository struct {
	db *pgxpool.Pool
}

func NewOrganizationPolicyRepository(pool *pgxpool.Pool) OrganizationPolicyReader {
	return &organizationPolicyRepository{db: pool}
}

func (r *organizationPolicyRepository) GetBrowserPolicy(ctx context.Context, organizationID uuid.UUID) (BrowserPolicy, error) {
	var settings []byte
	if err := r.db.QueryRow(ctx, `SELECT settings FROM organization WHERE id = $1`, organizationID).Scan(&settings); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return BrowserPolicy{}, ErrNotFound
		}
		return BrowserPolicy{}, err
	}

	var root map[string]any
	if err := json.Unmarshal(settings, &root); err != nil {
		return BrowserPolicy{}, err
	}

	rawPolicy, _ := root["browser_policy"].(map[string]any)
	if len(rawPolicy) == 0 {
		return BrowserPolicy{}, nil
	}

	allow := parseStringList(rawPolicy["allowlist"])
	if len(allow) == 0 {
		allow = parseStringList(rawPolicy["allow_list"])
	}
	deny := parseStringList(rawPolicy["denylist"])
	if len(deny) == 0 {
		deny = parseStringList(rawPolicy["deny_list"])
	}

	return BrowserPolicy{Allowlist: allow, Denylist: deny}, nil
}

type domainPolicyChecker struct {
	reader OrganizationPolicyReader
}

func NewDomainPolicyChecker(reader OrganizationPolicyReader) DomainPolicyChecker {
	return &domainPolicyChecker{reader: reader}
}

func (c *domainPolicyChecker) IsAllowed(ctx context.Context, organizationID uuid.UUID, rawURL string) bool {
	host := extractHost(rawURL)
	if host == "" {
		return false
	}
	domain := etldPlusOne(host)
	if domain == "" {
		domain = host
	}

	if strings.Contains(host, "bank") || strings.Contains(domain, "bank") {
		return false
	}

	policy := BrowserPolicy{}
	if c.reader != nil {
		loaded, err := c.reader.GetBrowserPolicy(ctx, organizationID)
		if err != nil && !errors.Is(err, ErrNotFound) {
			return false
		}
		policy = loaded
	}

	if len(policy.Allowlist) > 0 {
		allowed := false
		for _, entry := range policy.Allowlist {
			if matchDomainEntry(domain, host, entry) {
				allowed = true
				break
			}
		}
		if !allowed {
			return false
		}
	}

	for _, entry := range defaultDomainDenylist {
		if matchDomainEntry(domain, host, entry) {
			return false
		}
	}
	for _, entry := range policy.Denylist {
		if matchDomainEntry(domain, host, entry) {
			return false
		}
	}
	return true
}

func extractHost(rawURL string) string {
	trimmed := strings.TrimSpace(rawURL)
	if trimmed == "" {
		return ""
	}
	parsed, err := url.Parse(trimmed)
	if err != nil || parsed.Host == "" {
		parsed, err = url.Parse("https://" + trimmed)
		if err != nil {
			return ""
		}
	}
	host := strings.ToLower(strings.TrimSpace(parsed.Hostname()))
	if host == "" {
		return ""
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.String()
	}
	return host
}

func etldPlusOne(host string) string {
	parts := strings.Split(host, ".")
	if len(parts) < 2 {
		return host
	}
	return strings.Join(parts[len(parts)-2:], ".")
}

func matchDomainEntry(domain, host, entry string) bool {
	needle := strings.ToLower(strings.TrimSpace(entry))
	if needle == "" {
		return false
	}
	if needle == domain || needle == host {
		return true
	}
	if strings.HasPrefix(needle, "*.") {
		suffix := strings.TrimPrefix(needle, "*")
		return strings.HasSuffix(host, suffix) || strings.HasSuffix(domain, suffix)
	}
	return strings.Contains(host, needle)
}

func parseStringList(value any) []string {
	raw, ok := value.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		asString, ok := item.(string)
		if !ok {
			continue
		}
		trimmed := strings.TrimSpace(asString)
		if trimmed == "" {
			continue
		}
		out = append(out, trimmed)
	}
	return out
}
