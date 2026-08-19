//go:build linux

package cursor

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const linuxCAFileName = "cc-switch-cursor-ca.crt"

type linuxTrustBackend struct {
	name        string
	targetPath  string
	update      []string
	usesAnchors bool
}

func isCACertInstalled(certPEM []byte) (bool, error) {
	backend, err := detectLinuxTrustBackend()
	if err != nil {
		return false, err
	}
	return linuxTrustedCAEquals(backend, certPEM), nil
}

func EnsureCACertInstalled(certPEM []byte, certPath string) error {
	backend, err := detectLinuxTrustBackend()
	if err != nil {
		return err
	}
	if linuxTrustedCAEquals(backend, certPEM) {
		return nil
	}
	return runElevatedLinuxCATask("install-ca", certPath, backend)
}

func RemoveCACertInstalled(certPEM []byte, certPath string) error {
	backend, err := detectLinuxTrustBackend()
	if err != nil {
		return err
	}
	if !linuxTrustedCAEquals(backend, certPEM) && !backend.usesAnchors {
		return nil
	}
	return runElevatedLinuxCATask("remove-ca", certPath, backend)
}

// EnsureLegacySharedCACertRemoved is a no-op because the shared CA releases
// predated CC Switch's Linux trust-store integration.
func EnsureLegacySharedCACertRemoved() error {
	return nil
}

func runElevatedLinuxCATask(action, certPath string, backend linuxTrustBackend) error {
	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve sidecar executable: %w", err)
	}
	if _, err := exec.LookPath("pkexec"); err != nil {
		return fmt.Errorf("pkexec is required to run Linux system CA action %s", action)
	}
	args := []string{action, "--backend", backend.name}
	if certPath != "" {
		args = append(args, "--cert", certPath)
	}
	command := exec.Command("pkexec", append([]string{executable}, args...)...)
	output, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s Linux system CA: %w: %s", action, err, strings.TrimSpace(string(output)))
	}
	return nil
}

// InstallLinuxCA is only called by the explicit elevated sidecar subcommand.
func InstallLinuxCA(certPath, backendName string) error {
	if os.Geteuid() != 0 {
		return errors.New("install-ca must run as root")
	}
	backend, err := linuxTrustBackendByName(backendName)
	if err != nil {
		return err
	}
	payload, err := readManagedLinuxCACertificate(certPath)
	if err != nil {
		return err
	}
	if backend.usesAnchors {
		command := exec.Command("trust", "anchor", filepath.Clean(certPath))
		if output, err := command.CombinedOutput(); err != nil {
			return fmt.Errorf("trust anchor failed: %w: %s", err, strings.TrimSpace(string(output)))
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(backend.targetPath), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(backend.targetPath, payload, 0o644); err != nil {
		return fmt.Errorf("write system CA: %w", err)
	}
	command := exec.Command(backend.update[0], backend.update[1:]...)
	if output, err := command.CombinedOutput(); err != nil {
		return fmt.Errorf("update system trust: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func RemoveLinuxCA(certPath, backendName string) error {
	if os.Geteuid() != 0 {
		return errors.New("remove-ca must run as root")
	}
	if _, err := readManagedLinuxCACertificate(certPath); err != nil {
		return err
	}
	backend, err := linuxTrustBackendByName(backendName)
	if err != nil {
		return err
	}
	if backend.usesAnchors {
		command := exec.Command("trust", "anchor", "--remove", filepath.Clean(certPath))
		if output, err := command.CombinedOutput(); err != nil {
			return fmt.Errorf("remove p11-kit CA anchor: %w: %s", err, strings.TrimSpace(string(output)))
		}
		return nil
	}
	if err := os.Remove(backend.targetPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove system CA: %w", err)
	}
	command := exec.Command(backend.update[0], backend.update[1:]...)
	if output, err := command.CombinedOutput(); err != nil {
		return fmt.Errorf("update system trust after CA removal: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func readManagedLinuxCACertificate(certPath string) ([]byte, error) {
	cleanPath := filepath.Clean(certPath)
	expectedSuffix := filepath.Join("cursor-runtime", "data", "ca.crt")
	if !filepath.IsAbs(cleanPath) || !strings.HasSuffix(cleanPath, expectedSuffix) {
		return nil, errors.New("CA path is outside the CC Switch cursor-runtime data directory")
	}
	info, err := os.Lstat(cleanPath)
	if err != nil {
		return nil, fmt.Errorf("inspect CA certificate: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, errors.New("CA certificate must be a regular file, not a symlink")
	}
	payload, err := os.ReadFile(cleanPath)
	if err != nil {
		return nil, fmt.Errorf("read CA certificate: %w", err)
	}
	if len(payload) == 0 || !strings.Contains(string(payload), "BEGIN CERTIFICATE") {
		return nil, errors.New("invalid CA certificate")
	}
	return payload, nil
}

func detectLinuxTrustBackend() (linuxTrustBackend, error) {
	for _, name := range []string{"debian", "fedora", "p11-kit"} {
		backend, err := linuxTrustBackendByName(name)
		if err == nil && linuxTrustBackendAvailable(backend) {
			return backend, nil
		}
	}
	return linuxTrustBackend{}, errors.New("unsupported Linux trust store: expected update-ca-certificates, update-ca-trust, or trust")
}

func linuxTrustBackendByName(name string) (linuxTrustBackend, error) {
	switch strings.TrimSpace(name) {
	case "debian":
		return linuxTrustBackend{
			name:       "debian",
			targetPath: "/usr/local/share/ca-certificates/" + linuxCAFileName,
			update:     []string{"update-ca-certificates"},
		}, nil
	case "fedora":
		return linuxTrustBackend{
			name:       "fedora",
			targetPath: "/etc/pki/ca-trust/source/anchors/" + linuxCAFileName,
			update:     []string{"update-ca-trust", "extract"},
		}, nil
	case "p11-kit":
		return linuxTrustBackend{name: "p11-kit", usesAnchors: true, update: []string{"trust"}}, nil
	default:
		return linuxTrustBackend{}, fmt.Errorf("unsupported Linux trust backend %q", name)
	}
}

func linuxTrustBackendAvailable(backend linuxTrustBackend) bool {
	if len(backend.update) == 0 {
		return false
	}
	_, err := exec.LookPath(backend.update[0])
	return err == nil
}

func linuxTrustedCAEquals(backend linuxTrustBackend, certPEM []byte) bool {
	if backend.usesAnchors {
		return linuxP11KitContainsCA(certPEM)
	}
	installed, err := os.ReadFile(backend.targetPath)
	if err != nil {
		return false
	}
	return sha256.Sum256(installed) == sha256.Sum256(certPEM)
}

func linuxP11KitContainsCA(certPEM []byte) bool {
	block, _ := pem.Decode(certPEM)
	if block == nil || block.Type != "CERTIFICATE" {
		return false
	}
	command := exec.Command("trust", "extract", "--format=pem-bundle", "--filter=ca-anchors", "-")
	output, err := command.Output()
	if err != nil {
		return false
	}
	for rest := output; len(rest) > 0; {
		candidate, next := pem.Decode(rest)
		if candidate == nil {
			break
		}
		if candidate.Type == "CERTIFICATE" && bytes.Equal(candidate.Bytes, block.Bytes) {
			return true
		}
		rest = next
	}
	return false
}

func linuxCAFingerprint(certPEM []byte) string {
	sum := sha256.Sum256(certPEM)
	return hex.EncodeToString(sum[:])
}

var _ = linuxCAFingerprint
