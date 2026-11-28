package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"gorex/internal/atomicfile"
	"gorex/internal/rex"
)

const agentHighlightKey = "agentHighlight"

func settingsFilePath() string {
	return filepath.Join(rex.Dir(), "settings.json")
}

// readAgentHighlight returns whether agent pane highlighting is enabled in
// settings.json (true when absent).
func readAgentHighlight() (bool, error) {
	b, err := os.ReadFile(settingsFilePath())
	if errors.Is(err, os.ErrNotExist) {
		return true, nil
	}
	if err != nil {
		return true, err
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		return true, fmt.Errorf("gorex: settings.json is not valid JSON: %w", err)
	}
	v, ok := raw[agentHighlightKey]
	if !ok {
		return true, nil
	}
	var on bool
	if err := json.Unmarshal(v, &on); err != nil {
		return true, nil
	}
	return on, nil
}

// writeAgentHighlight sets or clears agentHighlight in settings.json while
// preserving all other keys and formatting.
func writeAgentHighlight(on bool) error {
	dir := rex.Dir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	path := filepath.Join(dir, "settings.json")
	raw := map[string]json.RawMessage{}
	if b, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(b, &raw); err != nil {
			return fmt.Errorf("gorex: settings.json is not valid JSON: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if on {
		delete(raw, agentHighlightKey)
	} else {
		raw[agentHighlightKey] = json.RawMessage("false")
	}
	return atomicfile.Write(path, marshalSettingsMap(raw), 0o600)
}

func marshalSettingsMap(raw map[string]json.RawMessage) []byte {
	if len(raw) == 0 {
		return []byte("{}\n")
	}
	var buf bytes.Buffer
	buf.WriteString("{")
	for i, k := range slices.Sorted(maps.Keys(raw)) {
		if i > 0 {
			buf.WriteString(",")
		}
		kb, _ := json.Marshal(k)
		buf.WriteString("\n  ")
		buf.Write(kb)
		buf.WriteString(": ")
		buf.Write(raw[k])
	}
	buf.WriteString("\n}\n")
	return buf.Bytes()
}

func parseOnOff(s string) (bool, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "on", "true", "1":
		return true, true
	case "off", "false", "0":
		return false, true
	default:
		return false, false
	}
}
