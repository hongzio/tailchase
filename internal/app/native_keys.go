package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode"
)

const nativePrefix = "tailcat:"

func nativeID(name string) string { return nativePrefix + name }
func validNativeName(name string) bool {
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, "/\\") {
		return false
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}
func validIdentityName(name string) bool {
	if validName.MatchString(name) {
		return true
	}
	n, ok := strings.CutPrefix(name, nativePrefix)
	return ok && validNativeName(n)
}
func keySource(k Identity) string {
	if k.Source == "tailcat" {
		return "tailcat"
	}
	return "tailchase"
}
func resolveIdentity(c Config, name string) string {
	if _, ok := c.Keys[name]; ok {
		return name
	}
	if _, ok := c.Keys[nativeID(name)]; ok {
		return nativeID(name)
	}
	return name
}
func nativeIdentity(name string, b []byte) (Identity, error) {
	pub, err := privatePublic(b)
	if err != nil {
		return Identity{}, err
	}
	var data struct {
		Public struct {
			RegionID int
			Region   []json.RawMessage
		}
	}
	if err = json.Unmarshal(b, &data); err != nil {
		return Identity{}, errors.New("invalid native key metadata")
	}
	role := "client"
	if data.Public.RegionID != 0 || len(data.Public.Region) > 0 {
		role = "server"
	}
	return Identity{Role: role, Public: pub, Private: json.RawMessage(b), Source: "tailcat", NativeName: name}, nil
}
func (s Store) withNativeKeys(c Config) Config {
	// Stored references pin the identity; never substitute a rotated private key.
	for n, k := range c.Keys {
		if k.Source == "tailcat" {
			k.Private = nil
			c.Keys[n] = k
		}
	}
	discovered := map[string]Identity{}
	if s.NativeDir != "" {
		entries, err := os.ReadDir(s.NativeDir)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			c.Warnings = append(c.Warnings, "Cannot read tailcat key directory")
		}
		for _, entry := range entries {
			name, ok := strings.CutSuffix(entry.Name(), ".private.json")
			if !ok {
				continue
			}
			if entry.IsDir() || !validNativeName(name) {
				c.Warnings = append(c.Warnings, "Skipped invalid tailcat key filename")
				continue
			}
			k, err := readNativeKey(filepath.Join(s.NativeDir, entry.Name()), name)
			if err != nil {
				c.Warnings = append(c.Warnings, fmt.Sprintf("tailcat key %s unavailable: %s", name, err))
				continue
			}
			discovered[nativeID(name)] = k
		}
	}
	for n, k := range discovered {
		old, exists := c.Keys[n]
		if !exists {
			c.Keys[n] = k
			continue
		}
		if old.Source != "tailcat" {
			c.Warnings = append(c.Warnings, fmt.Sprintf("Native key %s is shadowed by a managed identity", n))
			continue
		}
		if old.Public != k.Public || old.Role != k.Role {
			c.Warnings = append(c.Warnings, fmt.Sprintf("Native key %s changed; pinned identity kept, private key unavailable", n))
			continue
		}
		old.Private = k.Private
		c.Keys[n] = old
	}
	for _, n := range names(c.Keys) {
		k := c.Keys[n]
		if k.Source == "tailcat" {
			if _, ok := discovered[n]; !ok {
				c.Warnings = append(c.Warnings, fmt.Sprintf("Native key %s missing or unreadable", n))
			}
		}
	}
	return c
}
func readNativeKey(path, name string) (Identity, error) {
	f, err := os.Open(path)
	if err != nil {
		return Identity{}, errors.New("cannot read file")
	}
	defer f.Close()
	const maxKeySize = 1024 * 1024
	data, err := io.ReadAll(io.LimitReader(f, maxKeySize+1))
	if err != nil {
		return Identity{}, errors.New("cannot read file")
	}
	if len(data) > maxKeySize {
		return Identity{}, errors.New("key file exceeds size limit")
	}
	return nativeIdentity(name, data)
}
