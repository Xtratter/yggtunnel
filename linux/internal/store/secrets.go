package store

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/chacha20poly1305"
)

const encPrefix = "enc:"

// key returns the daemon's secret key, creating it on first use.
func (s *Store) key() ([]byte, error) {
	p := filepath.Join(s.Dir, "key")
	if b, err := os.ReadFile(p); err == nil {
		if len(b) != chacha20poly1305.KeySize {
			return nil, errors.New("store key file is damaged")
		}
		return b, nil
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	k := make([]byte, chacha20poly1305.KeySize)
	if _, err := rand.Read(k); err != nil {
		return nil, err
	}
	return k, writeAtomic(p, k)
}

func (s *Store) seal(plain string) (string, error) {
	k, err := s.key()
	if err != nil {
		return "", err
	}
	aead, err := chacha20poly1305.NewX(k)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	return encPrefix + base64.StdEncoding.EncodeToString(aead.Seal(nonce, nonce, []byte(plain), nil)), nil
}

func (s *Store) open(sealed string) (string, error) {
	if !strings.HasPrefix(sealed, encPrefix) {
		return "", errors.New("secret is not encrypted")
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(sealed, encPrefix))
	if err != nil {
		return "", errors.New("secret is damaged")
	}
	k, err := s.key()
	if err != nil {
		return "", err
	}
	aead, err := chacha20poly1305.NewX(k)
	if err != nil {
		return "", err
	}
	if len(raw) < aead.NonceSize() {
		return "", errors.New("secret is damaged")
	}
	plain, err := aead.Open(nil, raw[:aead.NonceSize()], raw[aead.NonceSize():], nil)
	if err != nil {
		return "", errors.New("secret cannot be decrypted")
	}
	return string(plain), nil
}

// writeAtomic writes a 0600 file through a temporary file and a rename.
func writeAtomic(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Chmod(tmp, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
