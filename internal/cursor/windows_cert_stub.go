//go:build !windows && !darwin && !linux

package cursor

import "fmt"

// EnsureCACertInstalled 非 Windows/macOS 平台的存根实现
func EnsureCACertInstalled(_ []byte, certPath string) error {
	return fmt.Errorf("ensureCACertInstalled: 当前平台暂不支持，certPath=%s", certPath)
}

func RemoveCACertInstalled(_ []byte, _ string) error {
	return fmt.Errorf("removeCACertInstalled: 当前平台暂不支持")
}
