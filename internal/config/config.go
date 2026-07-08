// Package config reads the family-standard config.json (same keys as the
// DeroLuna/zig miners: "daemon-address", "wallet", "threads"). Precedence is
// resolved in main: explicit CLI flags > config.json > compiled-in defaults.
package config

import (
	"encoding/json"
	"fmt"
	"os"
)

// File holds config.json values; nil pointer = key absent.
type File struct {
	DaemonAddress *string `json:"daemon-address"`
	Wallet        *string `json:"wallet"`
	Threads       *int    `json:"threads"`
}

// Save writes the family-shape config.json (the same three keys --setup
// prompts for; matches the zig miner's writer).
func Save(path, daemonAddress, wallet string, threads int) error {
	f := struct {
		DaemonAddress string `json:"daemon-address"`
		Wallet        string `json:"wallet"`
		Threads       int    `json:"threads"`
	}{daemonAddress, wallet, threads}
	data, err := json.MarshalIndent(&f, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

// Load reads path. A missing file returns (nil, nil) — config.json is
// optional. A malformed file is a hard error: silently mining with wrong
// settings is worse than refusing to start. Unknown keys are ignored so a
// fuller DeroLuna-style config works as-is.
func Load(path string) (*File, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var f File
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("%s is not valid JSON: %w", path, err)
	}
	return &f, nil
}
