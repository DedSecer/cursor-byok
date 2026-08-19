package appdata

import (
	"os"
	"path/filepath"
	"strings"
)

const (
	appDirName       = ".cursor-local-assistant-v2"
	legacyAppDirName = ".cursor-local-assistant"
	rootDirEnv       = "CC_SWITCH_CURSOR_DATA_DIR"
)

// RootDir 返回应用配置根目录。sidecar 通过环境变量使用 CC Switch 管理的
// 独立目录，避免读取或迁移原 Cursor 助手的用户配置。
func RootDir() string {
	if override := strings.TrimSpace(os.Getenv(rootDirEnv)); override != "" {
		return filepath.Clean(override)
	}
	return appRootDir(appDirName)
}

func UsesManagedRoot() bool {
	return strings.TrimSpace(os.Getenv(rootDirEnv)) != ""
}

func legacyRootDir() string {
	return appRootDir(legacyAppDirName)
}

func appRootDir(dirName string) string {
	homeDir, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(homeDir) == "" {
		return dirName
	}
	return filepath.Join(homeDir, dirName)
}

// ConfigFilePath 返回统一用户配置文件路径。
func ConfigFilePath() string {
	return filepath.Join(RootDir(), "config.yaml")
}

func DataRootPath() string {
	return filepath.Join(RootDir(), "data")
}

func HistoryRootPath() string {
	return filepath.Join(RootDir(), "history")
}

func UsageFilePath() string {
	return filepath.Join(HistoryRootPath(), "usage.json")
}

func AdsRootPath() string {
	return filepath.Join(DataRootPath(), "ads")
}

func CodebaseIndexRootPath() string {
	return filepath.Join(DataRootPath(), "codebase-index")
}

func DocsIndexRootPath() string {
	return filepath.Join(DataRootPath(), "docs-index")
}

func RulesRootPath() string {
	return filepath.Join(RootDir(), "rules")
}

// LogsRootPath 返回统一日志根目录路径。
func LogsRootPath() string {
	return filepath.Join(RootDir(), "logs")
}

// CACertFilePath 返回注入给宿主的 CA 文件路径。
func CACertFilePath() string {
	return filepath.Join(DataRootPath(), "ca.crt")
}

// CAKeyFilePath 返回仅供本安装使用的 CA 私钥路径。
func CAKeyFilePath() string {
	return filepath.Join(DataRootPath(), "ca.key")
}
