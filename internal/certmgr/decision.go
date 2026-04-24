package certmgr

import (
	"crypto/x509"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"autocert/internal/config"

	"github.com/go-acme/lego/v4/acme/api"
	"github.com/go-acme/lego/v4/certcrypto"
	"github.com/go-acme/lego/v4/certificate"
	"github.com/go-acme/lego/v4/lego"
)

type certificateAction string

const (
	actionSkip   certificateAction = "skip"
	actionObtain certificateAction = "obtain"
	actionRenew  certificateAction = "renew"
)

type renewalDecision struct {
	Action   certificateAction
	Reason   string
	NotAfter time.Time
}

func evaluateRenewal(now time.Time, cfg *config.Config, client *lego.Client, spec config.CertificateConfig, stored *storedCertificate) (renewalDecision, error) {
	if stored == nil || !stored.HasCertificate() {
		return renewalDecision{
			Action: actionObtain,
			Reason: "no certificate found on disk",
		}, nil
	}

	leaf, err := certcrypto.ParsePEMCertificate(stored.Certificate)
	if err != nil {
		action := actionObtain
		if stored.HasManagedResource() {
			action = actionRenew
		}

		return renewalDecision{
			Action: action,
			Reason: fmt.Sprintf("existing certificate is unreadable: %v", err),
		}, nil
	}

	currentCN, currentDomains := certificateIdentity(leaf)
	if !sameDomains(currentCN, currentDomains, spec.Domains) {
		return renewalDecision{
			Action:   actionObtain,
			Reason:   "configured domains differ from the certificate on disk",
			NotAfter: leaf.NotAfter,
		}, nil
	}

	// Prefer ARI when the CA provides it; otherwise fall back to the local
	// renew_before window below.
	if cfg.ACME.ARIEnabled() {
		info, err := client.Certificate.GetRenewalInfo(certificate.RenewalInfoRequest{Cert: leaf})
		switch {
		case err == nil:
			if renewAt := info.ShouldRenewAt(now.UTC(), 0); renewAt != nil {
				if !renewAt.After(now.UTC()) {
					return renewalDecision{
						Action:   preferredRenewAction(stored),
						Reason:   "ARI renewal window has started",
						NotAfter: leaf.NotAfter,
					}, nil
				}
				return renewalDecision{
					Action:   actionSkip,
					Reason:   fmt.Sprintf("ARI suggests renewing at %s", renewAt.Format(time.RFC3339)),
					NotAfter: leaf.NotAfter,
				}, nil
			}
		case errors.Is(err, api.ErrNoARI):
		default:
			return renewalDecision{}, fmt.Errorf("fetch ARI renewal info: %w", err)
		}
	}

	if !now.UTC().Before(leaf.NotAfter.Add(-cfg.ACME.RenewBefore.Duration)) {
		return renewalDecision{
			Action:   preferredRenewAction(stored),
			Reason:   fmt.Sprintf("certificate expires at %s", leaf.NotAfter.Format(time.RFC3339)),
			NotAfter: leaf.NotAfter,
		}, nil
	}

	return renewalDecision{
		Action:   actionSkip,
		Reason:   fmt.Sprintf("certificate is healthy until %s", leaf.NotAfter.Format(time.RFC3339)),
		NotAfter: leaf.NotAfter,
	}, nil
}

func preferredRenewAction(stored *storedCertificate) certificateAction {
	if stored != nil && stored.HasManagedResource() {
		return actionRenew
	}
	return actionObtain
}

func certificateIdentity(leaf *x509.Certificate) (string, []string) {
	seen := map[string]struct{}{}
	domains := make([]string, 0, len(leaf.DNSNames)+1)

	currentCN := normalizeDomain(leaf.Subject.CommonName)
	if currentCN != "" {
		domains = append(domains, currentCN)
		seen[currentCN] = struct{}{}
	}

	for _, dnsName := range leaf.DNSNames {
		normalized := normalizeDomain(dnsName)
		if normalized == "" {
			continue
		}
		if _, exists := seen[normalized]; exists {
			continue
		}
		seen[normalized] = struct{}{}
		domains = append(domains, normalized)
	}

	return currentCN, domains
}

func sameDomains(currentCN string, currentDomains, desiredDomains []string) bool {
	if len(desiredDomains) == 0 {
		return false
	}

	desiredCN := desiredDomains[0]
	if currentCN == "" {
		currentCN = desiredCN
	}

	if currentCN != desiredCN {
		return false
	}

	currentSet := append([]string(nil), currentDomains...)
	desiredSet := append([]string(nil), desiredDomains...)
	slices.Sort(currentSet)
	slices.Sort(desiredSet)
	return slices.Equal(currentSet, desiredSet)
}

func normalizeDomain(domain string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(domain)), ".")
}
