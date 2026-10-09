package github

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// Automic Vault prints "automic vault: human approval required" ahead of gh's
// own output. CombinedOutput folds stderr into the same buffer as stdout, so
// either stream puts that line in front of the JSON the parsers expect.

// unmarshalGHJSON decodes gh's JSON output, skipping any leading lines that
// are not JSON. Output that already parses is decoded unchanged; output with no
// JSON line reports what gh printed instead of a bare decoder error.
func unmarshalGHJSON(out []byte, v any) error {
	err := json.Unmarshal(out, v)
	if err == nil {
		return nil
	}
	payload, ok := fromFirstJSONLine(out)
	if !ok {
		return fmt.Errorf("no JSON in gh output (first line %q): %w", firstLine(out), err)
	}
	return json.Unmarshal(payload, v)
}

// fromFirstJSONLine returns out from the first line that opens a JSON array or
// object.
func fromFirstJSONLine(out []byte) ([]byte, bool) {
	for rest := out; len(rest) > 0; {
		if trimmed := bytes.TrimLeft(rest, " \t\r"); len(trimmed) > 0 && (trimmed[0] == '[' || trimmed[0] == '{') {
			return trimmed, true
		}
		i := bytes.IndexByte(rest, '\n')
		if i < 0 {
			break
		}
		rest = rest[i+1:]
	}
	return nil, false
}

func firstLine(out []byte) string {
	line, _, _ := strings.Cut(strings.TrimSpace(string(out)), "\n")
	const limit = 120
	if len(line) > limit {
		line = line[:limit] + "..."
	}
	return line
}

// lastLine returns the final non-empty line of out. It reads the single value a
// `--jq` expression prints, which comes after any Vault line.
func lastLine(out []byte) string {
	trimmed := strings.TrimSpace(string(out))
	if i := strings.LastIndexByte(trimmed, '\n'); i >= 0 {
		trimmed = trimmed[i+1:]
	}
	return strings.TrimSpace(trimmed)
}
