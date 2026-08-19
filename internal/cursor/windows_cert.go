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
	windowsRootStoreName  = "Root"
	windowsCertutilExe    = "certutil.exe"
	windowsPowerShellExe  = "powershell.exe"
	windowsUserCancelCode = 1223
	legacySharedCASHA1    = "C14B7488C5AB83F098BEB2603F1135595A381FC0"
)

func getCertThumbprint(certPEM []byte) (string, error) {
	block, _ := pem.Decode(certPEM)
	if block == nil {
		return "", fmt.Errorf("无法解析证书 PEM")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return "", fmt.Errorf("解析证书失败: %w", err)
	}
	return fmt.Sprintf("%X", sha1.Sum(cert.Raw)), nil
}

func hideWindow() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{HideWindow: true}
}

func isCACertInstalled(certPEM []byte) (bool, error) {
	thumbprint, err := getCertThumbprint(certPEM)
	if err != nil {
		return false, fmt.Errorf("获取证书指纹失败: %w", err)
	}
	return isCurrentUserCACertThumbprintInstalled(thumbprint)
}

func isCurrentUserCACertThumbprintInstalled(thumbprint string) (bool, error) {
	return isCACertThumbprintInstalled([]string{"-user"}, thumbprint)
}

func isLocalMachineCACertThumbprintInstalled(thumbprint string) (bool, error) {
	return isCACertThumbprintInstalled(nil, thumbprint)
}

func isCACertThumbprintInstalled(scopeArgs []string, thumbprint string) (bool, error) {
	args := append(append([]string{}, scopeArgs...), "-verifystore", windowsRootStoreName, thumbprint)
	cmd := exec.Command(windowsCertutilExe, args...)
	cmd.SysProcAttr = hideWindow()
	output, err := cmd.CombinedOutput()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			logger.Infof("isCACertInstalled: cert not found, thumbprint=%s exitCode=%d", thumbprint, exitErr.ExitCode())
			return false, nil
		}
		return false, fmt.Errorf("执行 certutil 检查证书存储失败: %w", err)
	}
	installed := strings.Contains(strings.ToUpper(string(output)), strings.ToUpper(thumbprint))
	logger.Infof("isCACertInstalled: installed=%t thumbprint=%s", installed, thumbprint)
	return installed, nil
}

// EnsureLegacySharedCACertRemoved removes the published legacy CA from both
// CurrentUser and LocalMachine stores. The managed installation CA remains in
// CurrentUser\Root so normal startup does not require elevation.
func EnsureLegacySharedCACertRemoved() error {
	userInstalled, err := isCurrentUserCACertThumbprintInstalled(legacySharedCASHA1)
	if err != nil {
		return fmt.Errorf("检查当前用户旧版共享 CA 失败: %w", err)
	}
	if userInstalled {
		cmd := exec.Command(windowsCertutilExe, "-user", "-delstore", windowsRootStoreName, legacySharedCASHA1)
		cmd.SysProcAttr = hideWindow()
		if output, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("从 Windows 当前用户信任存储删除旧版共享 CA 失败: %w: %s", err, strings.TrimSpace(string(output)))
		}
	}

	machineInstalled, err := isLocalMachineCACertThumbprintInstalled(legacySharedCASHA1)
	if err != nil {
		return fmt.Errorf("检查系统旧版共享 CA 失败: %w", err)
	}
	if machineInstalled {
		if err := runElevatedCertutil("-delstore", windowsRootStoreName, legacySharedCASHA1); err != nil {
			return fmt.Errorf("从 Windows 系统信任存储删除旧版共享 CA 失败: %w", err)
		}
	}
	return nil
}

func quotePowerShellLiteral(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func runElevatedCertutil(args ...string) error {
	quotedArgs := make([]string, 0, len(args))
	for _, arg := range args {
		quotedArgs = append(quotedArgs, quotePowerShellLiteral(arg))
	}
	script := fmt.Sprintf(
		"$process = Start-Process -FilePath %s -ArgumentList @(%s) -Verb RunAs -WindowStyle Hidden -Wait -PassThru; exit $process.ExitCode",
		quotePowerShellLiteral(windowsCertutilExe),
		strings.Join(quotedArgs, ","),
	)
	cmd := exec.Command(
		windowsPowerShellExe,
		"-NoProfile",
		"-NonInteractive",
		"-ExecutionPolicy",
		"Bypass",
		"-Command",
		script,
	)
	cmd.SysProcAttr = hideWindow()
	output, err := cmd.CombinedOutput()
	if err == nil {
		return nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == windowsUserCancelCode {
		return fmt.Errorf("用户取消了管理员权限授予")
	}
	trimmedOutput := strings.TrimSpace(string(output))
	if trimmedOutput == "" {
		return fmt.Errorf("通过管理员权限执行 certutil 失败: %w", err)
	}
	return fmt.Errorf("通过管理员权限执行 certutil 失败: %w, output: %s", err, trimmedOutput)
}

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
	return nil
}

func EnsureCACertInstalled(certPEM []byte, certPath string) error {
	installed, err := isCACertInstalled(certPEM)
	if err != nil {
		return fmt.Errorf("检查当前用户证书安装状态失败: %w", err)
	}
	if installed {
		return nil
	}
	return installCACertToWindowsStore(certPEM, certPath)
}

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
