package certmgr

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"time"

	cfprovider "autocert/internal/cloudflare"
	"autocert/internal/config"

	"github.com/go-acme/lego/v4/certcrypto"
	"github.com/go-acme/lego/v4/certificate"
	"github.com/go-acme/lego/v4/challenge/dns01"
	"github.com/go-acme/lego/v4/lego"
	"github.com/go-acme/lego/v4/registration"
)

type Manager struct {
	cfg    *config.Config
	logger *slog.Logger
	now    func() time.Time
}

func New(cfg *config.Config, logger *slog.Logger) *Manager {
	if logger == nil {
		logger = slog.Default()
	}

	return &Manager{
		cfg:    cfg,
		logger: logger,
		now:    time.Now,
	}
}

func (m *Manager) Reconcile(ctx context.Context) error {
	var errs []error

	for _, certCfg := range m.cfg.Certificates {
		if err := m.reconcileCertificate(ctx, certCfg); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", certCfg.Name, err))
		}
	}

	return errors.Join(errs...)
}

func (m *Manager) reconcileCertificate(ctx context.Context, certCfg config.CertificateConfig) error {
	logger := m.logger.With("certificate", certCfg.Name)

	client, err := m.newClient(certCfg)
	if err != nil {
		return err
	}

	stored, err := loadStoredCertificate(certCfg.OutputDir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("load certificate files: %w", err)
	}

	decision, err := evaluateRenewal(m.now(), m.cfg, client, certCfg, stored)
	if err != nil {
		return err
	}

	switch decision.Action {
	case actionSkip:
		// We normally do not reload on skip, but if a previous write succeeded
		// and the reload failed, retry it here.
		if err := m.retryPendingReload(ctx, logger, certCfg); err != nil {
			return err
		}
		logger.Info("skipping certificate", "reason", decision.Reason, "not_after", decision.NotAfter)
		return nil
	case actionObtain:
		logger.Info("obtaining certificate", "reason", decision.Reason, "domains", strings.Join(certCfg.Domains, ","))
		res, err := m.obtainCertificate(client, certCfg, stored)
		if err != nil {
			return fmt.Errorf("obtain certificate: %w", err)
		}
		return m.persistAndReload(ctx, logger, certCfg, res)
	case actionRenew:
		logger.Info("renewing certificate", "reason", decision.Reason, "domains", strings.Join(certCfg.Domains, ","))
		res, err := m.renewCertificate(client, certCfg, stored)
		if err != nil {
			return fmt.Errorf("renew certificate: %w", err)
		}
		return m.persistAndReload(ctx, logger, certCfg, res)
	default:
		return fmt.Errorf("unknown certificate action %q", decision.Action)
	}
}

func (m *Manager) newClient(certCfg config.CertificateConfig) (*lego.Client, error) {
	keyType, err := parseKeyType(m.cfg.ACME.KeyType)
	if err != nil {
		return nil, err
	}

	store := newAccountStore(m.cfg.ACME.StorageDir, m.cfg.ACME.Email, m.cfg.ACME.DirectoryURL)
	account, err := store.LoadOrCreate(keyType)
	if err != nil {
		return nil, err
	}

	legoConfig := lego.NewConfig(account)
	legoConfig.CADirURL = m.cfg.ACME.DirectoryURL
	legoConfig.UserAgent = "autocert/0.1"
	legoConfig.Certificate.KeyType = keyType

	client, err := lego.NewClient(legoConfig)
	if err != nil {
		return nil, fmt.Errorf("create ACME client: %w", err)
	}

	if account.Registration == nil {
		registrationResource, err := client.Registration.ResolveAccountByKey()
		if err != nil {
			registrationResource, err = client.Registration.Register(registration.RegisterOptions{
				TermsOfServiceAgreed: true,
			})
			if err != nil {
				return nil, fmt.Errorf("register ACME account: %w", err)
			}
			m.logger.Info("registered ACME account", "email", m.cfg.ACME.Email)
		}

		account.Registration = registrationResource
		if err := store.Save(account); err != nil {
			return nil, err
		}
	}

	provider, err := m.cloudflareProvider(certCfg)
	if err != nil {
		return nil, err
	}

	var opts []dns01.ChallengeOption
	if len(m.cfg.DNS.RecursiveNameservers) > 0 {
		opts = append(opts, dns01.AddRecursiveNameservers(m.cfg.DNS.RecursiveNameservers))
	}
	if m.cfg.DNS.DisableAuthoritativeNSCheck {
		opts = append(opts, dns01.DisableAuthoritativeNssPropagationRequirement())
	}

	if err := client.Challenge.SetDNS01Provider(provider, opts...); err != nil {
		return nil, fmt.Errorf("configure DNS challenge: %w", err)
	}

	return client, nil
}

func (m *Manager) cloudflareProvider(certCfg config.CertificateConfig) (*cfprovider.Provider, error) {
	cloudflareCfg := m.cfg.EffectiveCloudflare(certCfg)

	userToken, err := envValue(cloudflareCfg.UserTokenEnv)
	if err != nil {
		return nil, err
	}

	provider, err := cfprovider.NewProvider(cfprovider.ProviderConfig{
		AuthToken:          userToken,
		BaseURL:            cloudflareCfg.BaseURL,
		TTL:                120,
		PropagationTimeout: m.cfg.DNS.PropagationTimeout.Duration,
		PollingInterval:    m.cfg.DNS.PollingInterval.Duration,
	})
	if err != nil {
		return nil, fmt.Errorf("create Cloudflare DNS provider: %w", err)
	}

	return provider, nil
}

func (m *Manager) obtainCertificate(client *lego.Client, certCfg config.CertificateConfig, stored *storedCertificate) (*certificate.Resource, error) {
	request := certificate.ObtainRequest{
		Domains:        append([]string(nil), certCfg.Domains...),
		Bundle:         false,
		PreferredChain: certCfg.PreferredChain,
		Profile:        certCfg.Profile,
		EmailAddresses: append([]string(nil), certCfg.EmailAddresses...),
	}

	if certCfg.ReusePrivateKey && stored != nil && len(stored.PrivateKeyPEM) > 0 {
		privateKey, err := certcrypto.ParsePEMPrivateKey(stored.PrivateKeyPEM)
		if err != nil {
			return nil, fmt.Errorf("parse existing private key: %w", err)
		}
		request.PrivateKey = privateKey
	}

	resource, err := client.Certificate.Obtain(request)
	if err != nil {
		return nil, err
	}

	return resource, nil
}

func (m *Manager) renewCertificate(client *lego.Client, certCfg config.CertificateConfig, stored *storedCertificate) (*certificate.Resource, error) {
	if stored == nil || !stored.HasManagedResource() {
		return m.obtainCertificate(client, certCfg, stored)
	}

	resource := stored.Resource()
	if !certCfg.ReusePrivateKey {
		resource.PrivateKey = nil
	}

	renewed, err := client.Certificate.RenewWithOptions(resource, &certificate.RenewOptions{
		Bundle:         false,
		PreferredChain: certCfg.PreferredChain,
		Profile:        certCfg.Profile,
		EmailAddresses: append([]string(nil), certCfg.EmailAddresses...),
	})
	if err != nil {
		return nil, err
	}

	return renewed, nil
}

func (m *Manager) persistAndReload(ctx context.Context, logger *slog.Logger, certCfg config.CertificateConfig, res *certificate.Resource) error {
	if err := writeStoredCertificate(certCfg.OutputDir, certCfg.Domains, res); err != nil {
		return err
	}

	notAfter := ""
	if leaf, err := certcrypto.ParsePEMCertificate(res.Certificate); err == nil {
		notAfter = leaf.NotAfter.Format(time.RFC3339)
	}

	logger.Info("certificate written", "output_dir", certCfg.OutputDir, "not_after", notAfter)

	if len(certCfg.ReloadCommand) > 0 {
		// Mark reload as pending before running the command so a later timer run
		// can retry if the service reload fails after certs were written.
		if err := markPendingReload(certCfg.OutputDir); err != nil {
			return fmt.Errorf("mark pending reload: %w", err)
		}
		if err := runCommand(ctx, certCfg.ReloadCommand); err != nil {
			return fmt.Errorf("run reload command: %w", err)
		}
		if err := clearPendingReload(certCfg.OutputDir); err != nil {
			return fmt.Errorf("clear pending reload marker: %w", err)
		}
		logger.Info("reload command finished", "command", strings.Join(certCfg.ReloadCommand, " "))
	}

	return nil
}

func (m *Manager) retryPendingReload(ctx context.Context, logger *slog.Logger, certCfg config.CertificateConfig) error {
	if len(certCfg.ReloadCommand) == 0 {
		return nil
	}

	pending, err := hasPendingReload(certCfg.OutputDir)
	if err != nil {
		return fmt.Errorf("check pending reload marker: %w", err)
	}
	if !pending {
		return nil
	}

	// This path is only for recovering from a previous reload failure; it does
	// not mean a new certificate was issued during the current run.
	logger.Warn("retrying pending reload", "command", strings.Join(certCfg.ReloadCommand, " "))
	if err := runCommand(ctx, certCfg.ReloadCommand); err != nil {
		return fmt.Errorf("retry reload command: %w", err)
	}
	if err := clearPendingReload(certCfg.OutputDir); err != nil {
		return fmt.Errorf("clear pending reload marker: %w", err)
	}
	logger.Info("pending reload completed")
	return nil
}

func runCommand(ctx context.Context, command []string) error {
	if len(command) == 0 {
		return nil
	}

	cmd := exec.CommandContext(ctx, command[0], command[1:]...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func envValue(name string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("required environment variable name is empty")
	}

	value, ok := os.LookupEnv(name)
	if !ok || strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("environment variable %s is not set", name)
	}

	return value, nil
}

func parseKeyType(value string) (certcrypto.KeyType, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "ec256", "p256":
		return certcrypto.EC256, nil
	case "ec384", "p384":
		return certcrypto.EC384, nil
	case "rsa2048", "2048":
		return certcrypto.RSA2048, nil
	case "rsa3072", "3072":
		return certcrypto.RSA3072, nil
	case "rsa4096", "4096":
		return certcrypto.RSA4096, nil
	case "rsa8192", "8192":
		return certcrypto.RSA8192, nil
	default:
		return "", fmt.Errorf("unsupported acme.key_type %q", value)
	}
}
