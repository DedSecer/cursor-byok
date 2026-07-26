//go:build windows

package cursor

import (
	"crypto/sha1"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"syscall"

	"cursor/internal/logger"
)

const (
	windowsRootStoreName = "Root"
	windowsCertutilExe   = "certutil.exe"
)

// getCertThumbprint 获取证书的SHA1指纹，用于唯一标识证书
func getCertThumbprint(certPEM []byte) (string, error) {
	block, _ := pem.Decode(certPEM)
	if block == nil {
		return "", fmt.Errorf("无法解析证书 PEM")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return "", fmt.Errorf("解析证书失败: %w", err)
	}
	// SHA1 指纹，certutil 使用此格式
	thumbprint := fmt.Sprintf("%X", sha1.Sum(cert.Raw))
	return thumbprint, nil
}

// hideWindow 返回隐藏命令行窗口的 SysProcAttr
func hideWindow() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{
		HideWindow: true,
	}
}

// isCACertInstalled 检查 CA 证书是否已安装到 Windows 系统根证书存储。
// 默认不带 -user，表示 LocalMachine\Root。
func isCACertInstalled(certPEM []byte) (bool, error) {
	thumbprint, err := getCertThumbprint(certPEM)
	if err != nil {
		return false, fmt.Errorf("获取证书指纹失败: %w", err)
	}

	cmd := exec.Command(windowsCertutilExe, "-user", "-verifystore", windowsRootStoreName, thumbprint)
	cmd.SysProcAttr = hideWindow()
	output, err := cmd.CombinedOutput()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			// certutil 在找不到证书时返回非零退出码。
			logger.Infof("isCACertInstalled: cert not found in system store, thumbprint=%s exitCode=%d", thumbprint, exitErr.ExitCode())
			return false, nil
		}
		return false, fmt.Errorf("执行 certutil 检查系统证书存储失败: %w", err)
	}

	outStr := strings.ToUpper(string(output))
	if strings.Contains(outStr, thumbprint) {
		logger.Infof("isCACertInstalled: cert found in system store, thumbprint=%s", thumbprint)
		return true, nil
	}

	// 某些 Windows 语言环境下 certutil 的文本不同，这里仍然按未找到处理。
	logger.Infof("isCACertInstalled: cert not found in certutil output, thumbprint=%s", thumbprint)
	return false, nil
}

// installCACertToWindowsStore installs the CA into CurrentUser\Root. This is
// sufficient for Cursor and avoids elevating the whole desktop application.
func installCACertToWindowsStore(certPEM []byte, certPath string) error {
	thumbprint, err := getCertThumbprint(certPEM)
	if err != nil {
		return fmt.Errorf("获取证书指纹失败: %w", err)
	}

	logger.Infof("installCACertToWindowsStore: installing cert into current-user store, path=%s thumbprint=%s", certPath, thumbprint)
	cmd := exec.Command(windowsCertutilExe, "-user", "-addstore", windowsRootStoreName, certPath)
	cmd.SysProcAttr = hideWindow()
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("安装 CA 到 Windows 当前用户证书库失败: %w: %s", err, strings.TrimSpace(string(output)))
	}

	installed, err := isCACertInstalled(certPEM)
	if err != nil {
		return fmt.Errorf("验证当前用户证书安装状态失败: %w", err)
	}
	if !installed {
		return fmt.Errorf("证书导入命令已执行，但当前用户信任存储中未找到证书")
	}

	logger.Infof("installCACertToWindowsStore: cert installed successfully into current-user store, thumbprint=%s", thumbprint)
	return nil
}

// EnsureCACertInstalled 确保证书已安装到 Windows 系统信任存储。
func EnsureCACertInstalled(certPEM []byte, certPath string) error {
	installed, err := isCACertInstalled(certPEM)
	if err != nil {
		return fmt.Errorf("检查系统证书安装状态失败: %w", err)
	}

	if installed {
		logger.Infof("ensureCACertInstalled: cert already installed in system store, skipping")
		return nil
	}

	logger.Infof("ensureCACertInstalled: cert not installed in system store, installing...")
	return installCACertToWindowsStore(certPEM, certPath)
}

// RemoveCACertInstalled 从 CurrentUser\Root 移除本安装生成的 CA。
func RemoveCACertInstalled(certPEM []byte, _ string) error {
	installed, err := isCACertInstalled(certPEM)
	if err != nil {
		return err
	}
	if !installed {
		return nil
	}
	thumbprint, err := getCertThumbprint(certPEM)
	if err != nil {
		return err
	}
	cmd := exec.Command(windowsCertutilExe, "-user", "-delstore", windowsRootStoreName, thumbprint)
	cmd.SysProcAttr = hideWindow()
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("从 Windows 当前用户证书库移除 CA 失败: %w: %s", err, strings.TrimSpace(string(output)))
	}
	installed, err = isCACertInstalled(certPEM)
	if err != nil {
		return err
	}
	if installed {
		return errors.New("Windows CA 移除命令已执行，但证书仍存在")
	}
	return nil
}
