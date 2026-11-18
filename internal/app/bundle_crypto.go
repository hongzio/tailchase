package app

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
)

const bundleFormat = "tailchase-aliases/pbkdf2-sha256-600000/aes-256-gcm/v1"
const maxBundleFileBytes = 16 << 20
const passwordIterations = 600000
const maxPasswordBytes = 4096

type encryptedBundle struct {
	Format  string `json:"format"`
	Salt    []byte `json:"salt"`
	Payload []byte `json:"payload"`
}

func validateBundlePassword(password string) error {
	if password == "" {
		return errors.New("password is required")
	}
	if len(password) > maxPasswordBytes {
		return errors.New("password is too long")
	}
	for _, r := range password {
		if r == '\n' || r == '\r' {
			return errors.New("password must be a single line")
		}
	}
	return nil
}
func bundleCipher(password string, salt []byte) (cipher.AEAD, error) {
	key, err := pbkdf2.Key(sha256.New, password, salt, passwordIterations, 32)
	if err != nil {
		return nil, err
	}
	defer clear(key)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCMWithRandomNonce(block)
}
func encryptBundle(b Bundle, password string) ([]byte, error) {
	if err := validateBundlePassword(password); err != nil {
		return nil, err
	}
	if err := b.Validate(); err != nil {
		return nil, err
	}
	plaintext, err := json.Marshal(b)
	if err != nil {
		return nil, err
	}
	defer clear(plaintext)
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return nil, err
	}
	aead, err := bundleCipher(password, salt)
	if err != nil {
		return nil, err
	}
	envelope := encryptedBundle{Format: bundleFormat, Salt: salt, Payload: aead.Seal(nil, nil, plaintext, []byte(bundleFormat))}
	data, err := json.MarshalIndent(envelope, "", "  ")
	if err != nil {
		return nil, err
	}
	if len(data) > maxBundleFileBytes {
		return nil, errors.New("encrypted alias bundle exceeds size limit")
	}
	return data, nil
}
func decryptBundle(data []byte, password string) (Bundle, error) {
	if err := validateBundlePassword(password); err != nil {
		return Bundle{}, err
	}
	if len(data) > maxBundleFileBytes {
		return Bundle{}, errors.New("encrypted alias bundle exceeds size limit")
	}
	var envelope encryptedBundle
	if err := decode(data, &envelope); err != nil {
		return Bundle{}, errors.New("invalid encrypted alias bundle; legacy plaintext files are not supported")
	}
	if envelope.Format != bundleFormat {
		return Bundle{}, errors.New("unsupported encrypted alias bundle format")
	}
	if len(envelope.Salt) != 16 || len(envelope.Payload) < 28 {
		return Bundle{}, errors.New("invalid encrypted alias bundle")
	}
	aead, err := bundleCipher(password, envelope.Salt)
	if err != nil {
		return Bundle{}, err
	}
	plaintext, err := aead.Open(nil, nil, envelope.Payload, []byte(bundleFormat))
	if err != nil {
		return Bundle{}, errors.New("cannot decrypt alias bundle: incorrect password or damaged file")
	}
	defer clear(plaintext)
	var b Bundle
	if err := decode(plaintext, &b); err != nil {
		return Bundle{}, errors.New("invalid decrypted alias bundle")
	}
	return b, b.Validate()
}
func writeBundle(path string, b Bundle, password string) error {
	data, err := encryptBundle(b, password)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		os.Remove(path)
		return err
	}
	if closeErr != nil {
		os.Remove(path)
	}
	return closeErr
}
func readBundle(path, password string) (Bundle, error) {
	f, err := os.Open(path)
	if err != nil {
		return Bundle{}, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxBundleFileBytes+1))
	if err != nil {
		return Bundle{}, err
	}
	return decryptBundle(data, password)
}
func (s Store) exportAliases(path, password, confirmation string) (int, error) {
	if path == "" {
		return 0, errors.New("output file is required")
	}
	if err := validateBundlePassword(password); err != nil {
		return 0, err
	}
	if password != confirmation {
		return 0, errors.New("passwords do not match")
	}
	c, err := s.Load()
	if err != nil {
		return 0, err
	}
	b, err := exportBundle(c)
	if err != nil {
		return 0, err
	}
	if err := writeBundle(path, b, password); err != nil {
		return 0, err
	}
	return len(b.Aliases), nil
}
func (s Store) importAliases(path, password string, dryRun bool) (ImportSummary, error) {
	b, err := readBundle(path, password)
	if err != nil {
		return ImportSummary{}, err
	}
	if dryRun {
		c, err := s.Load()
		if err != nil {
			return ImportSummary{}, err
		}
		return mergeBundle(&c, b)
	}
	var summary ImportSummary
	err = s.Update(func(c *Config) error { var err error; summary, err = mergeBundle(c, b); return err })
	return summary, err
}
func (s ImportSummary) String() string {
	return fmt.Sprintf("%d added; %d already in Keys; %d already in Alias; %d renamed", s.Added, s.SkippedKeys, s.SkippedAliases, s.Renamed)
}
