package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// StateFileVersion is the schema version of state.json. Version 2 adds the
// per-agent `detail` and `lastTs` fields; files written by version 1 load
// unchanged (missing fields zero-value).
const StateFileVersion = 2

// StateFile is the on-disk snapshot shared between `tmon status` (writer)
// and `tmon dashboard` (reader).
type StateFile struct {
	Version int          `json:"version"`
	Agents  []AgentState `json:"agents"`
}

// NewState returns an empty snapshot with the current schema version.
func NewState() *StateFile {
	return &StateFile{Version: StateFileVersion}
}

// LoadState reads the snapshot from path. A missing file yields an empty
// snapshot; a corrupt one returns an error (callers may start fresh).
func LoadState(path string) (*StateFile, error) {
	sf := NewState()
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return sf, nil
		}
		return nil, err
	}
	if err := json.Unmarshal(b, sf); err != nil {
		return nil, err
	}
	return sf, nil
}

// Save writes the snapshot atomically: temp file + rename, both inside the
// state dir — never /tmp.
func (sf *StateFile) Save(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(sf, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".state.json.tmp*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // no-op if the rename succeeded
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
