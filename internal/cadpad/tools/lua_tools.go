package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	ds4 "github.com/NimbleMarkets/ds4go"
	"github.com/NimbleMarkets/ds4go-apps/internal/cadpad/lua"
	"github.com/NimbleMarkets/ds4go-apps/internal/cadpad/render"
	"github.com/NimbleMarkets/ds4go-apps/internal/cadpad/world"
)

// LuaFileTools holds the dependencies needed for the Lua file manipulation tools.
type LuaFileTools struct {
	Workspace string // absolute path to the allowed directory for .lua files
	W         *world.World
	R         *render.Renderer
}

// RegisterLuaFileTools registers tools for building and running Lua programs
// inside a scoped workspace directory. This is the primary way the LLM
// interacts with cadpad in the new design.
//
// Safe by construction:
// - All paths are cleaned and must stay inside Workspace.
// - Only .lua files are allowed for read/write/execute.
// - No arbitrary filesystem access.
func RegisterLuaFileTools(reg *ds4.ToolRegistry, lft LuaFileTools) error {
	if reg == nil {
		return fmt.Errorf("nil registry")
	}
	if lft.Workspace == "" {
		return fmt.Errorf("lua workspace directory must be set")
	}

	absWS, err := filepath.Abs(lft.Workspace)
	if err != nil {
		return fmt.Errorf("failed to resolve lua workspace: %w", err)
	}
	lft.Workspace = absWS

	// Ensure workspace exists
	if err := os.MkdirAll(lft.Workspace, 0755); err != nil {
		return fmt.Errorf("failed to create lua workspace: %w", err)
	}

	// --- lua_list ---
	reg.RegisterFunc(ds4.ToolSchema{
		Name:        "lua_list",
		Description: "List .lua files in the current Lua workspace directory.",
		Parameters:  json.RawMessage(`{"type":"object","properties":{}}`),
	}, func(ctx context.Context, raw json.RawMessage) (string, error) {
		entries, err := os.ReadDir(lft.Workspace)
		if err != nil {
			return fmt.Sprintf("Error listing workspace: %v", err), nil
		}
		var names []string
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".lua") {
				names = append(names, e.Name())
			}
		}
		if len(names) == 0 {
			return "No .lua files in workspace yet.", nil
		}
		return strings.Join(names, "\n"), nil
	})

	// --- lua_read ---
	reg.RegisterFunc(ds4.ToolSchema{
		Name:        "lua_read",
		Description: "Read the contents of a .lua file in the workspace.",
		Parameters:  json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","description":"Relative path to .lua file inside workspace"}},"required":["path"]}`),
	}, func(ctx context.Context, raw json.RawMessage) (string, error) {
		var p struct{ Path string `json:"path"` }
		if err := json.Unmarshal(raw, &p); err != nil {
			return "", err
		}
		full, err := safeLuaPath(lft.Workspace, p.Path)
		if err != nil {
			return fmt.Sprintf("Error: %v", err), nil
		}
		data, err := os.ReadFile(full)
		if err != nil {
			return fmt.Sprintf("Error reading %s: %v", p.Path, err), nil
		}
		return string(data), nil
	})

	// --- lua_write ---
	reg.RegisterFunc(ds4.ToolSchema{
		Name:        "lua_write",
		Description: "Write (overwrite) a .lua file in the workspace. Use with care.",
		Parameters: json.RawMessage(`{
			"type":"object",
			"properties":{
				"path":{"type":"string"},
				"content":{"type":"string"}
			},
			"required":["path","content"]
		}`),
	}, func(ctx context.Context, raw json.RawMessage) (string, error) {
		var p struct {
			Path    string `json:"path"`
			Content string `json:"content"`
		}
		if err := json.Unmarshal(raw, &p); err != nil {
			return "", err
		}
		full, err := safeLuaPath(lft.Workspace, p.Path)
		if err != nil {
			return fmt.Sprintf("Error: %v", err), nil
		}
		if err := os.WriteFile(full, []byte(p.Content), 0644); err != nil {
			return fmt.Sprintf("Error writing %s: %v", p.Path, err), nil
		}
		return fmt.Sprintf("Wrote %s (%d bytes).", p.Path, len(p.Content)), nil
	})

	// --- lua_append ---
	reg.RegisterFunc(ds4.ToolSchema{
		Name:        "lua_append",
		Description: "Append content to a .lua file in the workspace.",
		Parameters: json.RawMessage(`{
			"type":"object",
			"properties":{
				"path":{"type":"string"},
				"content":{"type":"string"}
			},
			"required":["path","content"]
		}`),
	}, func(ctx context.Context, raw json.RawMessage) (string, error) {
		var p struct {
			Path    string `json:"path"`
			Content string `json:"content"`
		}
		if err := json.Unmarshal(raw, &p); err != nil {
			return "", err
		}
		full, err := safeLuaPath(lft.Workspace, p.Path)
		if err != nil {
			return fmt.Sprintf("Error: %v", err), nil
		}
		f, err := os.OpenFile(full, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			return fmt.Sprintf("Error opening %s: %v", p.Path, err), nil
		}
		defer f.Close()
		if _, err := f.WriteString(p.Content); err != nil {
			return fmt.Sprintf("Error appending to %s: %v", p.Path, err), nil
		}
		return fmt.Sprintf("Appended to %s.", p.Path), nil
	})

	// --- lua_replace ---
	reg.RegisterFunc(ds4.ToolSchema{
		Name:        "lua_replace",
		Description: "Replace the first occurrence of a target string with replacement in a .lua file.",
		Parameters: json.RawMessage(`{
			"type":"object",
			"properties":{
				"path":{"type":"string"},
				"target":{"type":"string"},
				"replacement":{"type":"string"}
			},
			"required":["path","target","replacement"]
		}`),
	}, func(ctx context.Context, raw json.RawMessage) (string, error) {
		var p struct {
			Path        string `json:"path"`
			Target      string `json:"target"`
			Replacement string `json:"replacement"`
		}
		if err := json.Unmarshal(raw, &p); err != nil {
			return "", err
		}
		full, err := safeLuaPath(lft.Workspace, p.Path)
		if err != nil {
			return fmt.Sprintf("Error: %v", err), nil
		}
		data, err := os.ReadFile(full)
		if err != nil {
			return fmt.Sprintf("Error reading %s: %v", p.Path, err), nil
		}
		content := string(data)
		if !strings.Contains(content, p.Target) {
			return fmt.Sprintf("Error: target not found in %s.", p.Path), nil
		}
		newContent := strings.Replace(content, p.Target, p.Replacement, 1)
		if err := os.WriteFile(full, []byte(newContent), 0644); err != nil {
			return fmt.Sprintf("Error writing %s: %v", p.Path, err), nil
		}
		return fmt.Sprintf("Replaced first occurrence in %s.", p.Path), nil
	})

	// --- lua_run ---
	// This is the key tool: executes a .lua file using the low-level binding.
	// The script can call sdf.* functions and sdf.register(...) to populate the World.
	reg.RegisterFunc(ds4.ToolSchema{
		Name:        "lua_run",
		Description: "Execute a .lua file from the workspace using the low-level sdf binding. This populates or updates the current 3D world. The script should use sdf.register(name, obj) for objects you want visible.",
		Parameters: json.RawMessage(`{
			"type":"object",
			"properties":{
				"path":{"type":"string","description":"Relative path to the .lua file to execute"}
			},
			"required":["path"]
		}`),
	}, func(ctx context.Context, raw json.RawMessage) (string, error) {
		var p struct{ Path string `json:"path"` }
		if err := json.Unmarshal(raw, &p); err != nil {
			return "", err
		}
		full, err := safeLuaPath(lft.Workspace, p.Path)
		if err != nil {
			return fmt.Sprintf("Error: %v", err), nil
		}

		// Fresh state per run (safer, and matches "build your own library" model)
		st := lua.NewState(lft.W, lft.R)
		defer st.Close()

		if err := st.DoFile(full); err != nil {
			return fmt.Sprintf("Lua execution error in %s:\n%s", p.Path, err.Error()), nil
		}

		return fmt.Sprintf("Executed %s successfully. World updated.", p.Path), nil
	})

	return nil
}

// safeLuaPath ensures the requested path is a .lua file inside the workspace.
func safeLuaPath(workspace, rel string) (string, error) {
	clean := filepath.Clean(rel)
	if filepath.IsAbs(clean) || strings.HasPrefix(clean, "..") {
		return "", fmt.Errorf("path must be relative and stay inside workspace")
	}
	if !strings.HasSuffix(clean, ".lua") {
		return "", fmt.Errorf("only .lua files are allowed")
	}
	full := filepath.Join(workspace, clean)
	absFull, _ := filepath.Abs(full)
	absWS, _ := filepath.Abs(workspace)
	if !strings.HasPrefix(absFull, absWS) {
		return "", fmt.Errorf("path escapes workspace")
	}
	return full, nil
}
