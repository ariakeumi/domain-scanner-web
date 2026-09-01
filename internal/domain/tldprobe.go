package domain

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"strings"
	"sync"

	"domain_scanner/internal/reserved"
)

// TLDProbe is the outcome of probing whether a TLD can actually be checked.
type TLDProbe struct {
	// Status is one of "ok", "blocked" (registry refuses queries), or
	// "unsupported" (no reachable registry service at all).
	Status string `json:"status"`
	// Detail is a human-readable explanation, suitable for UI display.
	Detail string `json:"detail"`
}

// TLD probe statuses.
const (
	TLDOK          = "ok"
	TLDProbing     = "probing"
	TLDBlocked     = "blocked"
	TLDUnsupported = "unsupported"
)

// tldProbeCache memoizes probe results per TLD for the process lifetime.
// Registries rarely change their query policy, and probing costs a real
// registry round-trip, so results are kept forever.
var (
	tldProbeMu    sync.Mutex
	tldProbeCache = make(map[string]TLDProbe)
)

// ProbeTLD checks whether the given TLD (with or without leading dot) can be
// scanned: it runs one real registry query for a probe domain and classifies
// the outcome. Results are cached per TLD.
func ProbeTLD(tld string) TLDProbe {
	tld = normalizeTLD(tld)
	if tld == "" {
		return TLDProbe{Status: TLDUnsupported, Detail: "后缀为空"}
	}

	tldProbeMu.Lock()
	if p, ok := tldProbeCache[tld]; ok {
		tldProbeMu.Unlock()
		return p
	}
	tldProbeMu.Unlock()

	probe := probeTLDUncached(tld)

	tldProbeMu.Lock()
	tldProbeCache[tld] = probe
	tldProbeMu.Unlock()
	return probe
}

// probeDomainLen is the length of the random label used for probing. A
// 12-character random label is virtually guaranteed unregistered in any TLD.
const probeDomainLen = 12

// probeTLDUncached runs up to 3 probes with fresh random labels. Retries only
// when a probe name trips the reserved-name rules (a random label hitting
// those rules means the TLD itself is special-use, e.g. .test, but one
// accidental keyword collision is retried out).
func probeTLDUncached(tld string) TLDProbe {
	if hasRDAPForDomain("probe." + tld) {
		name := randomLabel() + "." + tld
		if _, err := rdapAvailable(name); err == nil {
			return TLDProbe{Status: TLDOK, Detail: fmt.Sprintf(".%s 通过 RDAP 查询，可以正常扫描", tld)}
		}
		return TLDProbe{
			Status: TLDBlocked,
			Detail: fmt.Sprintf(".%s 的 RDAP 查询失败，可能被限流或不可达，扫描结果不可信", tld),
		}
	}

	reservedHits := 0
	for attempt := 0; attempt < 3; attempt++ {
		name := randomLabel() + "." + tld
		available, err := CheckDomainAvailability(name)
		switch {
		case err == nil && available:
			// A definitive "free" verdict requires working registry data.
			return TLDProbe{Status: TLDOK, Detail: fmt.Sprintf(".%s 通过 WHOIS 查询，可以正常扫描", tld)}
		case err == nil && !available:
			if reservedRuleHit(name) {
				reservedHits++
				continue // retry with a fresh label
			}
			return TLDProbe{
				Status: TLDBlocked,
				Detail: fmt.Sprintf(".%s 的 WHOIS 无法给出明确结果（未注册域名被返回不可用），扫描结果不可信", tld),
			}
		case err == ErrNoWhoisData:
			return TLDProbe{
				Status: TLDUnsupported,
				Detail: fmt.Sprintf(".%s 没有可用的 WHOIS/RDAP 查询服务，无法判断域名可用性", tld),
			}
		default:
			return TLDProbe{
				Status: TLDBlocked,
				Detail: fmt.Sprintf(".%s 的注册局拒绝了查询：%v", tld, err),
			}
		}
	}

	// Every probe name hit the reserved rules: the whole TLD is special-use.
	return TLDProbe{
		Status: TLDUnsupported,
		Detail: fmt.Sprintf(".%s 是特殊用途或保留后缀，域名无法注册，无法扫描", tld),
	}
}

// randomLabel returns a random 12-character DNS label.
func randomLabel() string {
	const alphabet = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, probeDomainLen)
	for i := range b {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(alphabet))))
		if err != nil {
			// crypto/rand never fails on supported platforms; fall back
			// deterministically rather than panic.
			b[i] = alphabet[i%len(alphabet)]
			continue
		}
		b[i] = alphabet[n.Int64()]
	}
	return string(b)
}

// normalizeTLD lowercases the TLD and strips the leading dot.
func normalizeTLD(tld string) string {
	return strings.ToLower(strings.Trim(strings.TrimSpace(tld), "."))
}

// reservedRuleHit reports whether the reserved-name rules match name; used by
// the probe to tell "inconclusive registry" apart from "policy-reserved name".
func reservedRuleHit(name string) bool {
	return reserved.IsReservedDomain(name)
}
