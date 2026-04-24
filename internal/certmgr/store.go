package certmgr

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-acme/lego/v4/certificate"
)

type certState struct {
	Resource  certificate.Resource `json:"resource"`
	Domains   []string             `json:"domains"`
	UpdatedAt time.Time            `json:"updated_at"`
}

type storedCertificate struct {
	State         *certState
	Certificate   []byte
	Issuer        []byte
	PrivateKeyPEM []byte
}

func loadStoredCertificate(dir string) (*storedCertificate, error) {
	certPEM, certErr := readOptionalFile(filepath.Join(dir, "cert.pem"))
	if certErr != nil {
		return nil, certErr
	}
	issuerPEM, issuerErr := readOptionalFile(filepath.Join(dir, "chain.pem"))
	if issuerErr != nil {
		return nil, issuerErr
	}
	keyPEM, keyErr := readOptionalFile(filepath.Join(dir, "privkey.pem"))
	if keyErr != nil {
		return nil, keyErr
	}
	stateBytes, stateErr := readOptionalFile(filepath.Join(dir, "resource.json"))
	if stateErr != nil {
		return nil, stateErr
	}

	if len(certPEM) == 0 && len(issuerPEM) == 0 && len(keyPEM) == 0 && len(stateBytes) == 0 {
		return nil, os.ErrNotExist
	}

	stored := &storedCertificate{
		Certificate:   certPEM,
		Issuer:        issuerPEM,
		PrivateKeyPEM: keyPEM,
	}

	if len(stateBytes) > 0 {
		var state certState
		if err := json.Unmarshal(stateBytes, &state); err != nil {
			return nil, fmt.Errorf("parse %s: %w", filepath.Join(dir, "resource.json"), err)
		}
		stored.State = &state
	}

	return stored, nil
}

func hasPendingReload(dir string) (bool, error) {
	content, err := readOptionalFile(filepath.Join(dir, ".reload-pending"))
	if err != nil {
		return false, err
	}
	return len(content) > 0, nil
}

func markPendingReload(dir string) error {
	return writeFileAtomic(filepath.Join(dir, ".reload-pending"), []byte(time.Now().UTC().Format(time.RFC3339)), 0o644)
}

func clearPendingReload(dir string) error {
	err := os.Remove(filepath.Join(dir, ".reload-pending"))
	if err == nil || errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func (s *storedCertificate) HasCertificate() bool {
	return s != nil && len(s.Certificate) > 0
}

func (s *storedCertificate) HasManagedResource() bool {
	return s != nil && s.State != nil && s.State.Resource.CertURL != ""
}

func (s *storedCertificate) Resource() certificate.Resource {
	var res certificate.Resource
	if s != nil && s.State != nil {
		res = s.State.Resource
	}

	if s != nil {
		res.Certificate = s.Certificate
		res.IssuerCertificate = s.Issuer
		res.PrivateKey = s.PrivateKeyPEM
	}

	return res
}

func writeStoredCertificate(dir string, domains []string, res *certificate.Resource) error {
	if res == nil {
		return fmt.Errorf("certificate resource cannot be nil")
	}

	if err := ensureDir(dir); err != nil {
		return fmt.Errorf("create certificate directory: %w", err)
	}

	if err := writeFileAtomic(filepath.Join(dir, "cert.pem"), res.Certificate, 0o644); err != nil {
		return fmt.Errorf("write cert.pem: %w", err)
	}
	if err := writeFileAtomic(filepath.Join(dir, "chain.pem"), res.IssuerCertificate, 0o644); err != nil {
		return fmt.Errorf("write chain.pem: %w", err)
	}
	if err := writeFileAtomic(filepath.Join(dir, "fullchain.pem"), buildFullchain(res.Certificate, res.IssuerCertificate), 0o644); err != nil {
		return fmt.Errorf("write fullchain.pem: %w", err)
	}
	if err := writeFileAtomic(filepath.Join(dir, "privkey.pem"), res.PrivateKey, 0o600); err != nil {
		return fmt.Errorf("write privkey.pem: %w", err)
	}

	state := certState{
		Resource: certificate.Resource{
			Domain:        res.Domain,
			CertURL:       res.CertURL,
			CertStableURL: res.CertStableURL,
		},
		Domains:   append([]string(nil), domains...),
		UpdatedAt: time.Now().UTC(),
	}

	payload, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal resource.json: %w", err)
	}

	if err := writeFileAtomic(filepath.Join(dir, "resource.json"), payload, 0o644); err != nil {
		return fmt.Errorf("write resource.json: %w", err)
	}

	return nil
}

func buildFullchain(certPEM, issuerPEM []byte) []byte {
	if len(issuerPEM) == 0 {
		return certPEM
	}

	if len(certPEM) == 0 {
		return issuerPEM
	}

	combined := make([]byte, 0, len(certPEM)+len(issuerPEM)+1)
	combined = append(combined, certPEM...)
	if !strings.HasSuffix(string(certPEM), "\n") {
		combined = append(combined, '\n')
	}
	combined = append(combined, issuerPEM...)
	return combined
}

func ensureDir(path string) error {
	return os.MkdirAll(path, 0o755)
}

func writeFileAtomic(path string, content []byte, perm fs.FileMode) error {
	if err := ensureDir(filepath.Dir(path)); err != nil {
		return err
	}

	tmpFile, err := os.CreateTemp(filepath.Dir(path), ".autocert-*")
	if err != nil {
		return err
	}

	tmpName := tmpFile.Name()
	defer func() {
		_ = os.Remove(tmpName)
	}()

	if _, err := tmpFile.Write(content); err != nil {
		_ = tmpFile.Close()
		return err
	}

	if err := tmpFile.Chmod(perm); err != nil {
		_ = tmpFile.Close()
		return err
	}

	if err := tmpFile.Close(); err != nil {
		return err
	}

	return os.Rename(tmpName, path)
}

func readOptionalFile(path string) ([]byte, error) {
	content, err := os.ReadFile(path)
	if err == nil {
		return content, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	return nil, err
}

func sanitizePathSegment(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" {
		return "default"
	}

	var b strings.Builder
	lastDash := false
	for _, r := range value {
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

	sanitized := strings.Trim(b.String(), "-")
	if sanitized == "" {
		return "default"
	}

	return sanitized
}
