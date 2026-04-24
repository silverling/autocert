package cloudflare

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/go-acme/lego/v4/challenge/dns01"
)

const minTTL = 120

type ProviderConfig struct {
	AuthToken          string
	BaseURL            string
	TTL                int
	PropagationTimeout time.Duration
	PollingInterval    time.Duration
}

type Provider struct {
	client *Client
	config ProviderConfig

	recordIDs   map[string]string
	recordIDsMu sync.Mutex
	zoneIDs     map[string]string
	zoneIDsMu   sync.RWMutex
}

func NewProvider(cfg ProviderConfig) (*Provider, error) {
	if cfg.TTL < minTTL {
		cfg.TTL = minTTL
	}
	if cfg.PropagationTimeout <= 0 {
		cfg.PropagationTimeout = 2 * time.Minute
	}
	if cfg.PollingInterval <= 0 {
		cfg.PollingInterval = 5 * time.Second
	}

	client, err := NewClient(ClientConfig{
		AuthToken: cfg.AuthToken,
		BaseURL:   cfg.BaseURL,
	})
	if err != nil {
		return nil, err
	}

	return &Provider{
		client:    client,
		config:    cfg,
		recordIDs: make(map[string]string),
		zoneIDs:   make(map[string]string),
	}, nil
}

func (p *Provider) Present(domain, token, keyAuth string) error {
	info := dns01.GetChallengeInfo(domain, keyAuth)
	zoneName, err := dns01.FindZoneByFqdn(info.EffectiveFQDN)
	if err != nil {
		return fmt.Errorf("find zone for domain %q: %w", domain, err)
	}

	zoneID, err := p.lookupZoneID(context.Background(), zoneName)
	if err != nil {
		return fmt.Errorf("failed to find zone %s: %w", zoneName, err)
	}

	record, err := p.client.CreateDNSRecord(context.Background(), zoneID, Record{
		Type: "TXT",
		// Both the base domain and its wildcard authorization can map to the
		// same _acme-challenge name, so Cloudflare may temporarily show multiple
		// TXT records at the same label during one issuance.
		Name:    dns01.UnFqdn(info.EffectiveFQDN),
		Content: `"` + info.Value + `"`,
		TTL:     p.config.TTL,
	})
	if err != nil {
		return fmt.Errorf("failed to create TXT record: %w", err)
	}

	p.recordIDsMu.Lock()
	p.recordIDs[token] = record.ID
	p.recordIDsMu.Unlock()

	return nil
}

func (p *Provider) CleanUp(domain, token, keyAuth string) error {
	info := dns01.GetChallengeInfo(domain, keyAuth)
	zoneName, err := dns01.FindZoneByFqdn(info.EffectiveFQDN)
	if err != nil {
		return fmt.Errorf("find zone for domain %q: %w", domain, err)
	}

	zoneID, err := p.lookupZoneID(context.Background(), zoneName)
	if err != nil {
		return fmt.Errorf("failed to find zone %s: %w", zoneName, err)
	}

	p.recordIDsMu.Lock()
	recordID, ok := p.recordIDs[token]
	if ok {
		delete(p.recordIDs, token)
	}
	p.recordIDsMu.Unlock()

	if !ok {
		return fmt.Errorf("unknown record ID for %q", info.EffectiveFQDN)
	}

	if err := p.client.DeleteDNSRecord(context.Background(), zoneID, recordID); err != nil {
		return fmt.Errorf("failed to delete TXT record: %w", err)
	}

	return nil
}

func (p *Provider) Timeout() (timeout, interval time.Duration) {
	return p.config.PropagationTimeout, p.config.PollingInterval
}

func (p *Provider) lookupZoneID(ctx context.Context, zoneName string) (string, error) {
	zoneName = strings.TrimSuffix(strings.TrimSpace(zoneName), ".")

	p.zoneIDsMu.RLock()
	zoneID := p.zoneIDs[zoneName]
	p.zoneIDsMu.RUnlock()
	if zoneID != "" {
		return zoneID, nil
	}

	zoneID, err := p.client.ZoneIDByName(ctx, zoneName)
	if err != nil {
		return "", err
	}

	p.zoneIDsMu.Lock()
	p.zoneIDs[zoneName] = zoneID
	p.zoneIDsMu.Unlock()

	return zoneID, nil
}
