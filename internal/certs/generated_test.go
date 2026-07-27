package certs

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestCompromisedCAFingerprintIsRecognized(t *testing.T) {
	if !isCompromisedCAFingerprint(compromisedCAFingerprint) {
		t.Fatal("published CA fingerprint must remain on the revocation list")
	}
	if isCompromisedCAFingerprint("different-installation-ca") {
		t.Fatal("installation-scoped CA must not be treated as compromised")
	}
}

func TestLoadOrCreateManagerCreatesInstallationScopedCA(t *testing.T) {
	dir := t.TempDir()
	certPath := filepath.Join(dir, "ca.crt")
	keyPath := filepath.Join(dir, "ca.key")

	first, firstCert, err := LoadOrCreateManager(certPath, keyPath)
	if err != nil {
		t.Fatalf("create CA: %v", err)
	}
	second, secondCert, err := LoadOrCreateManager(certPath, keyPath)
	if err != nil {
		t.Fatalf("reload CA: %v", err)
	}
	if first == nil || second == nil || !bytes.Equal(firstCert, secondCert) {
		t.Fatal("installation CA was not reused")
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(keyPath)
		if err != nil {
			t.Fatalf("stat private key: %v", err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("private key mode = %o, want 600", info.Mode().Perm())
		}
	}
}
