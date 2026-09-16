package appinit

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	ds4 "github.com/NimbleMarkets/ds4go"
)

// resolveModelPath accepts existing paths or installed catalog aliases.
// ds4go owns alias lookup; this layer adds file validation and CLI diagnostics.
func resolveModelPath(value string) (string, error) {
	if value == "" {
		value = ds4.DefaultModelPath()
	}
	value = ds4.ResolveModelPath(value)
	if st, err := os.Stat(value); err == nil {
		if !st.Mode().IsRegular() || st.Size() == 0 {
			return "", fmt.Errorf("model file is empty or not a regular file: %s", value)
		}
		return value, nil
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("read model %q: %w", value, err)
	}
	if strings.ContainsAny(value, `/\`) || filepath.Ext(value) != "" {
		return "", fmt.Errorf("model file not found: %s", value)
	}

	catalog, err := ds4.ListModels()
	if err != nil {
		return "", fmt.Errorf("resolve model %q: %w", value, err)
	}
	for _, model := range catalog {
		if model.Alias == value {
			return "", fmt.Errorf("model %q is not installed at %s\nRun: ds4go model download %s", value, model.Path, model.Alias)
		}
	}
	return "", fmt.Errorf("unknown model alias %q\nRun: ds4go model list\nOr use --model /path/to/model.gguf", value)
}
