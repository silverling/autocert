package config

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	defaultDirectoryURL  = "https://acme-v02.api.letsencrypt.org/directory"
	defaultStorageDir    = "./data"
	defaultKeyType       = "ec256"
	defaultRenewBefore   = 30 * 24 * time.Hour
	defaultCheckInterval = 24 * time.Hour
	defaultDNSProvider   = "cloudflare"
	defaultUserTokenEnv  = "CLOUDFLARE_USER_TOKEN"
)

type Config struct {
	ACME         ACMEConfig          `yaml:"acme"`
	DNS          DNSConfig           `yaml:"dns"`
	Certificates []CertificateConfig `yaml:"certificates"`

	baseDir string `yaml:"-"`
}

type ACMEConfig struct {
	Email         string   `yaml:"email"`
	DirectoryURL  string   `yaml:"directory_url"`
	StorageDir    string   `yaml:"storage_dir"`
	KeyType       string   `yaml:"key_type"`
	RenewBefore   Duration `yaml:"renew_before"`
	CheckInterval Duration `yaml:"check_interval"`
	UseARI        *bool    `yaml:"use_ari"`
}

type DNSConfig struct {
	Provider                    string           `yaml:"provider"`
	PropagationTimeout          Duration         `yaml:"propagation_timeout"`
	PollingInterval             Duration         `yaml:"polling_interval"`
	RecursiveNameservers        []string         `yaml:"recursive_nameservers"`
	DisableAuthoritativeNSCheck bool             `yaml:"disable_authoritative_ns_check"`
	Cloudflare                  CloudflareConfig `yaml:"cloudflare"`
}

type CloudflareConfig struct {
	UserTokenEnv string `yaml:"user_token_env"`
	BaseURL      string `yaml:"base_url"`
}

type CertificateConfig struct {
	Name            string           `yaml:"name"`
	Domains         []string         `yaml:"domains"`
	OutputDir       string           `yaml:"output_dir"`
	ReloadCommand   []string         `yaml:"reload_command"`
	PreferredChain  string           `yaml:"preferred_chain"`
	Profile         string           `yaml:"profile"`
	EmailAddresses  []string         `yaml:"email_addresses"`
	ReusePrivateKey bool             `yaml:"reuse_private_key"`
	Cloudflare      CloudflareConfig `yaml:"cloudflare"`
}

func Load(path string) (*Config, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve config path: %w", err)
	}

	content, err := os.ReadFile(absPath)
	if err != nil {
		return nil, fmt.Errorf("read config file: %w", err)
	}

	cfg := &Config{baseDir: filepath.Dir(absPath)}
	decoder := yaml.NewDecoder(bytes.NewReader(content))
	decoder.KnownFields(true)
	if err := decoder.Decode(cfg); err != nil {
		return nil, fmt.Errorf("parse config file: %w", err)
	}

	cfg.applyDefaults()
	if err := cfg.normalizeAndValidate(); err != nil {
		return nil, err
	}

	return cfg, nil
}

func (c *Config) applyDefaults() {
	c.ACME.Email = strings.TrimSpace(c.ACME.Email)
	c.ACME.DirectoryURL = strings.TrimSpace(c.ACME.DirectoryURL)
	c.ACME.StorageDir = strings.TrimSpace(c.ACME.StorageDir)
	c.ACME.KeyType = strings.ToLower(strings.TrimSpace(c.ACME.KeyType))
	c.DNS.Provider = strings.ToLower(strings.TrimSpace(c.DNS.Provider))
	c.DNS.Cloudflare.UserTokenEnv = strings.TrimSpace(c.DNS.Cloudflare.UserTokenEnv)
	c.DNS.Cloudflare.BaseURL = strings.TrimSpace(c.DNS.Cloudflare.BaseURL)
	c.DNS.RecursiveNameservers = normalizeStringSlice(c.DNS.RecursiveNameservers)

	if c.ACME.DirectoryURL == "" {
		c.ACME.DirectoryURL = defaultDirectoryURL
	}

	if c.ACME.StorageDir == "" {
		c.ACME.StorageDir = defaultStorageDir
	}

	if c.ACME.KeyType == "" {
		c.ACME.KeyType = defaultKeyType
	}

	if c.ACME.RenewBefore.Duration == 0 {
		c.ACME.RenewBefore.Duration = defaultRenewBefore
	}

	if c.ACME.CheckInterval.Duration == 0 {
		c.ACME.CheckInterval.Duration = defaultCheckInterval
	}

	if c.DNS.Provider == "" {
		c.DNS.Provider = defaultDNSProvider
	}

	if c.ACME.UseARI == nil {
		defaultTrue := true
		c.ACME.UseARI = &defaultTrue
	}

	applyCloudflareDefaults(&c.DNS.Cloudflare)

	c.ACME.StorageDir = resolvePath(c.baseDir, c.ACME.StorageDir)

	for i := range c.Certificates {
		cert := &c.Certificates[i]
		cert.Name = strings.TrimSpace(cert.Name)
		cert.OutputDir = strings.TrimSpace(cert.OutputDir)
		cert.PreferredChain = strings.TrimSpace(cert.PreferredChain)
		cert.Profile = strings.TrimSpace(cert.Profile)
		cert.Cloudflare.UserTokenEnv = strings.TrimSpace(cert.Cloudflare.UserTokenEnv)
		cert.Cloudflare.BaseURL = strings.TrimSpace(cert.Cloudflare.BaseURL)
		applyCloudflareDefaults(&cert.Cloudflare)

		if cert.Name == "" && len(cert.Domains) > 0 {
			cert.Name = defaultCertificateName(cert.Domains[0])
		}

		if cert.OutputDir == "" {
			cert.OutputDir = filepath.Join(c.ACME.StorageDir, "live", cert.Name)
		}

		cert.OutputDir = resolvePath(c.baseDir, cert.OutputDir)
	}
}

func (c *Config) normalizeAndValidate() error {
	if c.ACME.Email == "" {
		return fmt.Errorf("acme.email is required")
	}

	if c.DNS.Provider != defaultDNSProvider {
		return fmt.Errorf("dns.provider must be %q", defaultDNSProvider)
	}

	if c.ACME.RenewBefore.Duration <= 0 {
		return fmt.Errorf("acme.renew_before must be greater than 0")
	}

	if c.ACME.CheckInterval.Duration <= 0 {
		return fmt.Errorf("acme.check_interval must be greater than 0")
	}

	if len(c.Certificates) == 0 {
		return fmt.Errorf("at least one certificate entry is required")
	}

	seenNames := make(map[string]struct{}, len(c.Certificates))
	for i := range c.Certificates {
		cert := &c.Certificates[i]
		if cert.Name == "" {
			return fmt.Errorf("certificate[%d].name is required", i)
		}

		if _, exists := seenNames[cert.Name]; exists {
			return fmt.Errorf("duplicate certificate name %q", cert.Name)
		}
		seenNames[cert.Name] = struct{}{}

		if cert.OutputDir == "" {
			return fmt.Errorf("certificate %q output_dir cannot be empty", cert.Name)
		}

		normalizedDomains := make([]string, 0, len(cert.Domains))
		seenDomains := map[string]struct{}{}
		for _, domain := range cert.Domains {
			normalized := normalizeDomain(domain)
			if normalized == "" {
				continue
			}
			if _, exists := seenDomains[normalized]; exists {
				continue
			}
			seenDomains[normalized] = struct{}{}
			normalizedDomains = append(normalizedDomains, normalized)
		}

		if len(normalizedDomains) == 0 {
			return fmt.Errorf("certificate %q must define at least one domain", cert.Name)
		}

		cert.Domains = normalizedDomains
		for idx := range cert.EmailAddresses {
			cert.EmailAddresses[idx] = strings.TrimSpace(cert.EmailAddresses[idx])
		}
	}

	return nil
}

func (c *Config) EffectiveCloudflare(cert CertificateConfig) CloudflareConfig {
	effective := c.DNS.Cloudflare
	if cert.Cloudflare.UserTokenEnv != "" {
		effective.UserTokenEnv = cert.Cloudflare.UserTokenEnv
	}
	if cert.Cloudflare.BaseURL != "" {
		effective.BaseURL = cert.Cloudflare.BaseURL
	}
	return effective
}

func (a ACMEConfig) ARIEnabled() bool {
	return a.UseARI == nil || *a.UseARI
}

func applyCloudflareDefaults(cfg *CloudflareConfig) {
	if cfg == nil {
		return
	}

	if cfg.UserTokenEnv == "" {
		cfg.UserTokenEnv = defaultUserTokenEnv
	}
}

func normalizeStringSlice(values []string) []string {
	if len(values) == 0 {
		return nil
	}

	result := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		result = append(result, trimmed)
	}

	return result
}

func resolvePath(baseDir, path string) string {
	if path == "" || filepath.IsAbs(path) {
		return path
	}

	return filepath.Clean(filepath.Join(baseDir, path))
}

func normalizeDomain(domain string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(domain)), ".")
}

func defaultCertificateName(domain string) string {
	domain = normalizeDomain(domain)
	if domain == "" {
		return "certificate"
	}

	var b strings.Builder
	lastDash := false
	for _, r := range domain {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
			lastDash = false
		case r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		default:
			if !lastDash {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}

	name := strings.Trim(b.String(), "-")
	if name == "" {
		return "certificate"
	}

	return name
}
