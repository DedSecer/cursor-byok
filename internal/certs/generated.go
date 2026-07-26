package certs

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"time"
)

const generatedCAValidity = 10 * 365 * 24 * time.Hour

// LoadOrCreateManager loads an installation-scoped CA, creating it atomically
// when this installation has not enabled the Cursor proxy before.
func LoadOrCreateManager(certPath, keyPath string) (*Manager, []byte, error) {
	certPEM, certErr := os.ReadFile(certPath)
	keyPEM, keyErr := os.ReadFile(keyPath)
	switch {
	case certErr == nil && keyErr == nil:
		manager, err := NewManagerFromPEM(certPEM, keyPEM)
		return manager, certPEM, err
	case (certErr == nil) != (keyErr == nil):
		return nil, nil, errors.New("CA certificate and private key must either both exist or both be absent")
	case !errors.Is(certErr, os.ErrNotExist):
		return nil, nil, fmt.Errorf("read CA certificate: %w", certErr)
	case !errors.Is(keyErr, os.ErrNotExist):
		return nil, nil, fmt.Errorf("read CA private key: %w", keyErr)
	}

	certPEM, keyPEM, err := generateCA()
	if err != nil {
		return nil, nil, err
	}
	if err := os.MkdirAll(filepath.Dir(certPath), 0o700); err != nil {
		return nil, nil, fmt.Errorf("create CA directory: %w", err)
	}
	if err := writePrivateFileAtomic(keyPath, keyPEM, 0o600); err != nil {
		return nil, nil, err
	}
	if err := writePrivateFileAtomic(certPath, certPEM, 0o644); err != nil {
		_ = os.Remove(keyPath)
		return nil, nil, err
	}
	manager, err := NewManagerFromPEM(certPEM, keyPEM)
	if err != nil {
		return nil, nil, err
	}
	return manager, certPEM, nil
}

func generateCA() ([]byte, []byte, error) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 3072)
	if err != nil {
		return nil, nil, fmt.Errorf("generate CA private key: %w", err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, nil, fmt.Errorf("generate CA serial: %w", err)
	}
	publicKeyDER, err := x509.MarshalPKIXPublicKey(&privateKey.PublicKey)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal CA public key: %w", err)
	}
	subjectKeyID := sha256.Sum256(publicKeyDER)
	now := time.Now().UTC()
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName:   "CC Switch Cursor Local CA",
			Organization: []string{"CC Switch"},
		},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(generatedCAValidity),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLen:            0,
		MaxPathLenZero:        true,
		SubjectKeyId:          append([]byte(nil), subjectKeyID[:]...),
	}
	certificateDER, err := x509.CreateCertificate(rand.Reader, template, template, &privateKey.PublicKey, privateKey)
	if err != nil {
		return nil, nil, fmt.Errorf("create CA certificate: %w", err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificateDER})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(privateKey)})
	return certPEM, keyPEM, nil
}

func writePrivateFileAtomic(path string, payload []byte, mode os.FileMode) error {
	tempPath := path + ".tmp"
	if err := os.WriteFile(tempPath, payload, mode); err != nil {
		return fmt.Errorf("write %s: %w", filepath.Base(path), err)
	}
	if err := os.Chmod(tempPath, mode); err != nil {
		_ = os.Remove(tempPath)
		return fmt.Errorf("set permissions on %s: %w", filepath.Base(path), err)
	}
	if err := os.Rename(tempPath, path); err != nil {
		_ = os.Remove(tempPath)
		return fmt.Errorf("install %s: %w", filepath.Base(path), err)
	}
	return nil
}
