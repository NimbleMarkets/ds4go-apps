package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSafeLuaPathBasic(t *testing.T) {
	ws := "/tmp/cadpad/workspace"

	cases := []struct {
		rel  string
		want string
		err  bool
	}{
		{"hello.lua", filepath.Join(ws, "hello.lua"), false},
		{"subdir/script.lua", filepath.Join(ws, "subdir", "script.lua"), false},
		{"../escape.lua", "", true},
		{"foo/../../etc/passwd.lua", "", true},
		{"/absolute/path.lua", "", true},
		{"notalua.txt", "", true},
		{"foo.lua-evil", "", true},
	}

	for _, tc := range cases {
		got, err := safeLuaPath(ws, tc.rel)
		if tc.err {
			if err == nil {
				t.Errorf("safeLuaPath(%q) expected error, got %q", tc.rel, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("safeLuaPath(%q) unexpected error: %v", tc.rel, err)
			continue
		}
		if got != tc.want {
			t.Errorf("safeLuaPath(%q) = %q, want %q", tc.rel, got, tc.want)
		}
	}
}

func TestSafeLuaPathPrefixAttack(t *testing.T) {
	ws := "/tmp/cadpad/workspace"

	// This would pass a naive strings.HasPrefix check.
	got, err := safeLuaPath(ws, "../workspace-evil/script.lua")
	if err == nil {
		t.Errorf("expected path traversal error, got %q", got)
	}
}

type fakeDiag struct{ report string }

func (f fakeDiag) Check(_ context.Context, _, _ string) string {
	return f.report
}

func writeTempLua(t *testing.T, content string) (dir, rel, full string) {
	t.Helper()
	dir = t.TempDir()
	rel = "main.lua"
	full = filepath.Join(dir, rel)
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	return dir, rel, full
}

func TestAppendDiagnostics_NilDiagUnchanged(t *testing.T) {
	_, rel, full := writeTempLua(t, "local x = 1\n")
	base := "Wrote main.lua (11 bytes)."
	got := appendDiagnostics(context.Background(), LuaFileTools{}, rel, full, base)
	if got != base {
		t.Fatalf("nil diag should not change result; got %q", got)
	}
}

func TestAppendDiagnostics_None(t *testing.T) {
	_, rel, full := writeTempLua(t, "local x = 1\n")
	lft := LuaFileTools{Diag: fakeDiag{report: ""}}
	got := appendDiagnostics(context.Background(), lft, rel, full, "Wrote main.lua.")
	if got != "Wrote main.lua.\nDiagnostics: none." {
		t.Fatalf("got %q", got)
	}
}

func TestAppendDiagnostics_WithReport(t *testing.T) {
	_, rel, full := writeTempLua(t, "local x = \n")
	lft := LuaFileTools{Diag: fakeDiag{report: "main.lua:1:11: [error] unexpected symbol"}}
	got := appendDiagnostics(context.Background(), lft, rel, full, "Wrote main.lua.")
	want := "Wrote main.lua.\nDiagnostics:\nmain.lua:1:11: [error] unexpected symbol"
	if !strings.Contains(got, want) {
		t.Fatalf("got %q, want to contain %q", got, want)
	}
}
