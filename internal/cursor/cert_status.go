package cursor

import (
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
)

// CACertStatus 返回当前安装级 CA 的系统信任状态和 SHA-256 指纹。
func CACertStatus(certPEM []byte) (bool, string, error) {
	fingerprint, err := caCertSHA256Fingerprint(certPEM)
	if err != nil {
		return false, "", err
	}
	installed, err := isCACertInstalled(certPEM)
	return installed, fingerprint, err
}

func caCertSHA256Fingerprint(certPEM []byte) (string, error) {
	block, _ := pem.Decode(certPEM)
	if block == nil || block.Type != "CERTIFICATE" {
		return "", errors.New("invalid CA certificate PEM")
	}
	certificate, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(certificate.Raw)
	return hex.EncodeToString(sum[:]), nil
}
