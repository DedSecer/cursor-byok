package certs

import (
	"crypto/sha256"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const compromisedCAFingerprint = "836e6bb84f6c3e63316dbb4ec257223af09f7490e7aae09030b8515ed61ee9ff"

func isCompromisedCAFingerprint(fingerprint string) bool {
	return fingerprint == compromisedCAFingerprint
}

// RemoveCompromisedCA removes the published shared CA from the trust store and
// local data directory before LoadOrCreateManager provisions an installation CA.
func RemoveCompromisedCA(
	certPath, keyPath string,
	removeTrust func([]byte, string) error,
) (bool, error) {
	certPEM, err := os.ReadFile(certPath)
	if errors.Is(err, os.ErrNotExist) {
		if removeErr := os.Remove(keyPath); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			return false, fmt.Errorf("remove orphaned CA private key: %w", removeErr)
		}
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read existing CA certificate: %w", err)
	}
	fingerprint, err := caFingerprint(certPEM)
	if err != nil {
		return false, fmt.Errorf("inspect existing CA certificate: %w", err)
	}
	if !isCompromisedCAFingerprint(fingerprint) {
		return false, nil
	}
	if err := removeTrust(certPEM, certPath); err != nil {
		return true, fmt.Errorf("remove compromised CA from system trust: %w", err)
	}
	for _, path := range []string{keyPath, certPath} {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return true, fmt.Errorf("remove compromised %s: %w", filepath.Base(path), err)
		}
	}
	return true, nil
}

func caFingerprint(certPEM []byte) (string, error) {
	block, _ := pem.Decode(certPEM)
	if block == nil || block.Type != "CERTIFICATE" {
		return "", errors.New("invalid CA certificate PEM")
	}
	certificate, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", sha256.Sum256(certificate.Raw)), nil
}
