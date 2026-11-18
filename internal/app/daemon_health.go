package app

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

var errStoreLocked = errors.New("another process holds the lock")
var buildOnce sync.Once
var buildIdentity string

func currentBuildID() string {
	buildOnce.Do(func() {
		path, e := os.Executable()
		if e != nil {
			return
		}
		f, e := os.Open(path)
		if e != nil {
			return
		}
		defer f.Close()
		hash := sha256.New()
		if _, e = io.Copy(hash, f); e == nil {
			buildIdentity = hex.EncodeToString(hash.Sum(nil))
		}
	})
	return buildIdentity
}
func configurationError(err error) string {
	var syntax *json.SyntaxError
	var fieldType *json.UnmarshalTypeError
	switch {
	case errors.As(err, &syntax):
		return fmt.Sprintf("invalid configuration JSON at byte %d", syntax.Offset)
	case errors.As(err, &fieldType):
		return "configuration contains a field with the wrong JSON type"
	case errors.Is(err, os.ErrPermission):
		return "cannot read config.json: permission denied"
	case strings.HasPrefix(err.Error(), "json: unknown field"):
		return "configuration uses unsupported fields; rebuild and restart the daemon"
	case strings.HasPrefix(err.Error(), "unsupported schema version"):
		return "configuration schema is unsupported; rebuild and restart the daemon"
	default:
		return "configuration validation failed; check the configuration with the current binary and restart an older daemon"
	}
}
func (s Store) writeRuntime(r Runtime) {
	data, _ := json.Marshal(r)
	if err := atomicWrite(filepath.Join(s.Dir, "runtime.json"), data); err != nil {
		fmt.Fprintln(os.Stderr, "cannot save runtime status")
	}
}
