package domain

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// rdapClient is a shared HTTP client for RDAP queries.
var rdapClient = &http.Client{Timeout: 15 * time.Second}

// rdapBaseURLs maps TLDs (without dot) to their registry RDAP base URL.
// Only registries that block port-43 WHOIS need an entry; .li/.ch are run by
// SWITCH, which requires its web WHOIS (captcha-protected) but offers RDAP.
// Note: .li/.ch are absent from the IANA RDAP bootstrap file.
var rdapBaseURLs = map[string]string{
	"li": "https://rdap.nic.li/domain/",
	"ch": "https://rdap.nic.ch/domain/",
}

// rdapMaxResponseBytes bounds the size of RDAP responses we are willing to
// read (64 KiB is far more than any domain object needs).
const rdapMaxResponseBytes = 64 << 10

// hasRDAPForDomain reports whether the domain's TLD has a direct RDAP
// endpoint configured.
func hasRDAPForDomain(domain string) bool {
	tld := domain
	if i := strings.LastIndex(domain, "."); i >= 0 {
		tld = domain[i+1:]
	}
	_, ok := rdapBaseURLs[strings.ToLower(tld)]
	return ok
}

// rdapAvailable reports availability for domains whose registry only offers
// RDAP. Semantics: HTTP 404 => unregistered/available, HTTP 200 => taken,
// 429/5xx => rate limited or registry error, surfaced as an error so the
// caller retries instead of misclassifying.
func rdapAvailable(domain string) (bool, error) {
	tld := domain
	if i := strings.LastIndex(domain, "."); i >= 0 {
		tld = domain[i+1:]
	}
	base, ok := rdapBaseURLs[strings.ToLower(tld)]
	if !ok {
		return false, fmt.Errorf("no RDAP endpoint for .%s", tld)
	}

	name := strings.ToLower(strings.TrimPrefix(domain, "*."))
	url := base + name
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("Accept", "application/rdap+json")
	req.Header.Set("User-Agent", "domain-scanner/1.0")

	resp, err := rdapClient.Do(req)
	if err != nil {
		return false, fmt.Errorf("rdap query failed: %w", err)
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusNotFound:
		return true, nil
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		// Sanity-check the payload is actually the queried domain object,
		// not an error page served with a 200.
		var body struct {
			LDHName string `json:"ldhName"`
			Handle  string `json:"handle"`
		}
		if err := json.NewDecoder(io.LimitReader(resp.Body, rdapMaxResponseBytes)).Decode(&body); err != nil {
			return false, fmt.Errorf("rdap response parse failed: %w", err)
		}
		if !strings.EqualFold(body.LDHName, name) && body.Handle == "" {
			return false, fmt.Errorf("rdap response does not match domain %s", name)
		}
		return false, nil
	case resp.StatusCode == http.StatusTooManyRequests:
		return false, fmt.Errorf("rdap rate limited (HTTP 429)")
	default:
		return false, fmt.Errorf("rdap unexpected status %d", resp.StatusCode)
	}
}
