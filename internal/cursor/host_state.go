package cursor

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
)

const hostStateSnapshotVersion = 1

type snapshotValue struct {
	Exists bool `json:"exists"`
	Value  any  `json:"value,omitempty"`
}

type snapshotBytes struct {
	Exists bool   `json:"exists"`
	Value  []byte `json:"value,omitempty"`
}

type hostStateSnapshot struct {
	Version         int                      `json:"version"`
	SettingsPath    string                   `json:"settingsPath"`
	StateDBPath     string                   `json:"stateDbPath"`
	OriginalSetting map[string]snapshotValue `json:"originalSettings"`
	AppliedSetting  map[string]snapshotValue `json:"appliedSettings"`
	OriginalState   map[string]snapshotBytes `json:"originalState"`
	AppliedState    map[string]snapshotBytes `json:"appliedState"`
	OriginalNodeCA  snapshotValue            `json:"originalNodeExtraCaCerts"`
	AppliedNodeCA   snapshotValue            `json:"appliedNodeExtraCaCerts"`
}

// ApplyHostTakeover records every modified Cursor value before applying the
// local proxy. A stale snapshot is restored first so an earlier crash cannot
// turn the next start into a new baseline.
func ApplyHostTakeover(snapshotPath, proxyURL, caCertPath, email, token string) error {
	if strings.TrimSpace(snapshotPath) == "" {
		return errors.New("host state snapshot path is required")
	}
	if err := RestoreHostTakeover(snapshotPath); err != nil {
		return fmt.Errorf("restore stale host state: %w", err)
	}
	settingsPath, err := resolveCursorSettingsPath()
	if err != nil {
		return err
	}
	stateDBPath, err := resolveCursorStateDBPath()
	if err != nil {
		return err
	}
	settingKeys := append([]string(nil), injectedCursorSettingsKeys...)
	stateKeys := takeoverStateKeys()
	originalSettings, err := readSettingsSnapshot(settingsPath, settingKeys)
	if err != nil {
		return err
	}
	snapshot := hostStateSnapshot{
		Version:         hostStateSnapshotVersion,
		SettingsPath:    settingsPath,
		StateDBPath:     stateDBPath,
		OriginalSetting: originalSettings,
		OriginalState:   readStateSnapshot(stateDBPath, stateKeys),
		OriginalNodeCA:  readNodeExtraCASnapshot(),
	}
	if err := writeHostStateSnapshot(snapshotPath, snapshot); err != nil {
		return err
	}

	rollback := func(applyErr error) error {
		if restoreErr := restoreHostStateSnapshot(snapshotPath, snapshot); restoreErr != nil {
			return errors.Join(applyErr, fmt.Errorf("rollback host state: %w", restoreErr))
		}
		return applyErr
	}
	if runtime.GOOS == "darwin" || runtime.GOOS == "linux" {
		if err := SetSystemNodeExtraCACerts(caCertPath); err != nil {
			return rollback(err)
		}
		snapshot.AppliedNodeCA = readNodeExtraCASnapshot()
		if err := writeHostStateSnapshot(snapshotPath, snapshot); err != nil {
			return rollback(err)
		}
	}
	if err := WriteUserProxySettings(proxyURL); err != nil {
		return rollback(err)
	}
	snapshot.AppliedSetting, err = readSettingsSnapshot(settingsPath, settingKeys)
	if err != nil {
		return rollback(err)
	}
	if err := writeHostStateSnapshot(snapshotPath, snapshot); err != nil {
		return rollback(err)
	}
	if err := InjectCursorUserInfo(email, token); err != nil {
		return rollback(err)
	}
	snapshot.AppliedState = readStateSnapshot(stateDBPath, stateKeys)
	if err := writeHostStateSnapshot(snapshotPath, snapshot); err != nil {
		return rollback(err)
	}
	return nil
}

func RestoreHostTakeover(snapshotPath string) error {
	payload, err := os.ReadFile(snapshotPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read host state snapshot: %w", err)
	}
	var snapshot hostStateSnapshot
	if err := json.Unmarshal(payload, &snapshot); err != nil {
		return fmt.Errorf("decode host state snapshot: %w", err)
	}
	if snapshot.Version != hostStateSnapshotVersion {
		return fmt.Errorf("unsupported host state snapshot version %d", snapshot.Version)
	}
	return restoreHostStateSnapshot(snapshotPath, snapshot)
}

func restoreHostStateSnapshot(snapshotPath string, snapshot hostStateSnapshot) error {
	var result error
	if err := restoreSettingsSnapshot(snapshot.SettingsPath, snapshot.OriginalSetting, snapshot.AppliedSetting); err != nil {
		result = errors.Join(result, err)
	}
	if err := restoreStateSnapshot(snapshot.StateDBPath, snapshot.OriginalState, snapshot.AppliedState); err != nil {
		result = errors.Join(result, err)
	}
	if err := restoreNodeExtraCASnapshot(snapshot.OriginalNodeCA, snapshot.AppliedNodeCA); err != nil {
		result = errors.Join(result, err)
	}
	if result != nil {
		return result
	}
	if err := os.Remove(snapshotPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove host state snapshot: %w", err)
	}
	return nil
}

func readSettingsSnapshot(path string, keys []string) (map[string]snapshotValue, error) {
	settings, err := readCursorSettings(path)
	if err != nil {
		return nil, err
	}
	result := make(map[string]snapshotValue, len(keys))
	for _, key := range keys {
		value, exists := settings[key]
		result[key] = snapshotValue{Exists: exists, Value: value}
	}
	return result, nil
}

func restoreSettingsSnapshot(path string, original, applied map[string]snapshotValue) error {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	settings, err := readCursorSettings(path)
	if err != nil {
		return err
	}
	changed := false
	for _, key := range sortedSnapshotKeys(original) {
		current, currentExists := settings[key]
		appliedValue := applied[key]
		if currentExists != appliedValue.Exists || (currentExists && !reflect.DeepEqual(current, appliedValue.Value)) {
			continue
		}
		originalValue := original[key]
		if originalValue.Exists {
			settings[key] = originalValue.Value
		} else {
			delete(settings, key)
		}
		changed = true
	}
	if !changed {
		return nil
	}
	return writeCursorSettings(path, settings)
}

func readCursorSettings(path string) (map[string]any, error) {
	settings := make(map[string]any)
	payload, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return settings, nil
		}
		return nil, fmt.Errorf("read Cursor settings: %w", err)
	}
	if len(bytes.TrimSpace(payload)) == 0 {
		return settings, nil
	}
	parsed, err := decodeCursorSettingsJSONC(payload)
	if err != nil {
		return nil, fmt.Errorf("decode Cursor settings: %w", err)
	}
	return parsed, nil
}

func writeCursorSettings(path string, settings map[string]any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create Cursor settings directory: %w", err)
	}
	payload, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return fmt.Errorf("encode Cursor settings: %w", err)
	}
	payload = append(payload, '\n')
	return writeAtomic(path, payload, 0o644)
}

func takeoverStateKeys() []string {
	values := buildCursorAuthStateValues("", "")
	keys := make([]string, 0, len(values)+1)
	for key := range values {
		keys = append(keys, key)
	}
	keys = append(keys, cursorStateStatsigBootstrapKey)
	sort.Strings(keys)
	return keys
}

func readStateSnapshot(path string, keys []string) map[string]snapshotBytes {
	result := make(map[string]snapshotBytes, len(keys))
	db, err := openCursorStateDB(path)
	if err != nil {
		return result
	}
	defer db.Close()
	for _, key := range keys {
		var value []byte
		err := db.QueryRow("SELECT value FROM ItemTable WHERE key = ?", key).Scan(&value)
		result[key] = snapshotBytes{Exists: err == nil, Value: append([]byte(nil), value...)}
	}
	return result
}

func restoreStateSnapshot(path string, original, applied map[string]snapshotBytes) error {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	db, err := openCursorStateDB(path)
	if err != nil {
		return err
	}
	defer db.Close()
	tx, err := db.BeginTx(context.Background(), &sql.TxOptions{})
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	for _, key := range sortedBytesKeys(original) {
		var current []byte
		queryErr := tx.QueryRow("SELECT value FROM ItemTable WHERE key = ?", key).Scan(&current)
		currentExists := queryErr == nil
		appliedValue := applied[key]
		if currentExists != appliedValue.Exists || (currentExists && !bytes.Equal(current, appliedValue.Value)) {
			continue
		}
		originalValue := original[key]
		if originalValue.Exists {
			if _, err := tx.Exec("INSERT OR REPLACE INTO ItemTable(key, value) VALUES(?, ?)", key, originalValue.Value); err != nil {
				return err
			}
		} else if _, err := tx.Exec("DELETE FROM ItemTable WHERE key = ?", key); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

func openCursorStateDB(path string) (*sql.DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(fmt.Sprintf("PRAGMA busy_timeout = %d", cursorStateSQLiteBusyTimeoutMS)); err != nil {
		_ = db.Close()
		return nil, err
	}
	if _, err := db.Exec("CREATE TABLE IF NOT EXISTS ItemTable (key TEXT UNIQUE ON CONFLICT REPLACE, value BLOB)"); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

func readNodeExtraCASnapshot() snapshotValue {
	if runtime.GOOS == "darwin" {
		output, err := exec.Command("launchctl", "getenv", "NODE_EXTRA_CA_CERTS").Output()
		value := strings.TrimSpace(string(output))
		return snapshotValue{Exists: err == nil && value != "", Value: value}
	}
	value, exists := os.LookupEnv("NODE_EXTRA_CA_CERTS")
	return snapshotValue{Exists: exists, Value: value}
}

func restoreNodeExtraCASnapshot(original, applied snapshotValue) error {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		return nil
	}
	current := readNodeExtraCASnapshot()
	if current.Exists != applied.Exists || (current.Exists && !reflect.DeepEqual(current.Value, applied.Value)) {
		return nil
	}
	if !original.Exists {
		return ClearSystemNodeExtraCACerts()
	}
	value, _ := original.Value.(string)
	return SetSystemNodeExtraCACerts(value)
}

func writeHostStateSnapshot(path string, snapshot hostStateSnapshot) error {
	payload, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return fmt.Errorf("encode host state snapshot: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create host state directory: %w", err)
	}
	return writeAtomic(path, append(payload, '\n'), 0o600)
}

func writeAtomic(path string, payload []byte, mode os.FileMode) error {
	tempPath := path + ".tmp"
	if err := os.WriteFile(tempPath, payload, mode); err != nil {
		return err
	}
	if err := os.Chmod(tempPath, mode); err != nil {
		_ = os.Remove(tempPath)
		return err
	}
	if err := os.Rename(tempPath, path); err != nil {
		_ = os.Remove(tempPath)
		return err
	}
	return nil
}

func sortedSnapshotKeys(values map[string]snapshotValue) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sortedBytesKeys(values map[string]snapshotBytes) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
