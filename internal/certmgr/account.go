package certmgr

import (
	"crypto"
	"encoding/json"
	"fmt"
	"path/filepath"

	"github.com/go-acme/lego/v4/certcrypto"
	"github.com/go-acme/lego/v4/registration"
)

type account struct {
	Email        string                 `json:"email"`
	Registration *registration.Resource `json:"registration,omitempty"`

	privateKey crypto.PrivateKey
}

func (a *account) GetEmail() string {
	return a.Email
}

func (a *account) GetRegistration() *registration.Resource {
	return a.Registration
}

func (a *account) GetPrivateKey() crypto.PrivateKey {
	return a.privateKey
}

type accountStore struct {
	email        string
	directoryURL string
	dir          string
}

func newAccountStore(storageDir, email, directoryURL string) accountStore {
	return accountStore{
		email:        email,
		directoryURL: directoryURL,
		dir: filepath.Join(
			storageDir,
			"accounts",
			sanitizePathSegment(directoryURL),
			sanitizePathSegment(email),
		),
	}
}

func (s accountStore) LoadOrCreate(keyType certcrypto.KeyType) (*account, error) {
	acc := &account{Email: s.email}

	keyBytes, err := readOptionalFile(filepath.Join(s.dir, "account.key.pem"))
	if err != nil {
		return nil, fmt.Errorf("load account private key: %w", err)
	}

	if len(keyBytes) == 0 {
		privateKey, err := certcrypto.GeneratePrivateKey(keyType)
		if err != nil {
			return nil, fmt.Errorf("generate account private key: %w", err)
		}
		acc.privateKey = privateKey
		if err := s.Save(acc); err != nil {
			return nil, err
		}
	} else {
		privateKey, err := certcrypto.ParsePEMPrivateKey(keyBytes)
		if err != nil {
			return nil, fmt.Errorf("parse account private key: %w", err)
		}
		acc.privateKey = privateKey
	}

	regBytes, err := readOptionalFile(filepath.Join(s.dir, "account.json"))
	if err != nil {
		return nil, fmt.Errorf("load account metadata: %w", err)
	}
	if len(regBytes) > 0 {
		var reg registration.Resource
		if err := json.Unmarshal(regBytes, &reg); err != nil {
			return nil, fmt.Errorf("parse account metadata: %w", err)
		}
		acc.Registration = &reg
	}

	return acc, nil
}

func (s accountStore) Save(acc *account) error {
	if acc == nil {
		return fmt.Errorf("account cannot be nil")
	}

	if err := ensureDir(s.dir); err != nil {
		return fmt.Errorf("create account directory: %w", err)
	}

	if acc.privateKey != nil {
		if err := writeFileAtomic(filepath.Join(s.dir, "account.key.pem"), certcrypto.PEMEncode(acc.privateKey), 0o600); err != nil {
			return fmt.Errorf("write account private key: %w", err)
		}
	}

	if acc.Registration != nil {
		payload, err := json.MarshalIndent(acc.Registration, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal account metadata: %w", err)
		}
		if err := writeFileAtomic(filepath.Join(s.dir, "account.json"), payload, 0o644); err != nil {
			return fmt.Errorf("write account metadata: %w", err)
		}
	}

	return nil
}
