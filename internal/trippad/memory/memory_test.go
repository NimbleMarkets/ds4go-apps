package memory

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	ds4 "github.com/NimbleMarkets/ds4go"
	"golang.org/x/sys/unix"
)

func openTest(t *testing.T, dir, session string) *Memory {
	t.Helper()
	m, err := Open(dir, session)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Close)
	return m
}
func callTest(t *testing.T, m *Memory, scope, op string, args any) string {
	t.Helper()
	b, _ := json.Marshal(args)
	s, err := m.Call(context.Background(), scope, op, b)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestScopesPersistAndRegister(t *testing.T) {
	dir := t.TempDir()
	a := openTest(t, dir, "one")
	b := openTest(t, dir, "two")
	callTest(t, a, "global", "set", map[string]string{"key": "lessons", "value": "shared fact"})
	if err := a.Set(context.Background(), "plan", "private plan"); err != nil {
		t.Fatal(err)
	}
	if got := callTest(t, b, "global", "get", map[string]string{"key": "lessons"}); !strings.Contains(got, "shared fact") {
		t.Fatal(got)
	}
	if got := callTest(t, b, "session", "get", map[string]string{"key": "plan"}); !strings.HasPrefix(got, "ERROR:") {
		t.Fatal("session leaked:", got)
	}
	c := openTest(t, dir, "one")
	if got := callTest(t, c, "session", "get", map[string]string{"key": "plan"}); !strings.Contains(got, "private plan") {
		t.Fatal(got)
	}
	reg := ds4.NewToolRegistry()
	if err := b.Register(reg); err != nil {
		t.Fatal(err)
	}
	out, err := reg.ExecuteToolCalls(context.Background(), []ds4.ToolCall{{ID: "g", Name: "global_scratch_get", Arguments: `{"key":"lessons"}`}, {ID: "s", Name: "scratch_list", Arguments: `{}`}})
	if err != nil || len(out) != 2 || !strings.Contains(out[0].Content, "shared fact") || strings.Contains(out[1].Content, "plan") {
		t.Fatalf("%+v %v", out, err)
	}
	if _, err := Open(dir, "../escape"); err == nil {
		t.Fatal("accepted traversal")
	}
}

func TestModelCannotChangeHostKeys(t *testing.T) {
	m := openTest(t, t.TempDir(), "protected")
	reg := ds4.NewToolRegistry()
	if err := m.Register(reg); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"latest-request", "last-result", "checkpoint"} {
		if err := m.Set(context.Background(), key, "host value"); err != nil {
			t.Fatal(err)
		}
		for _, operation := range []string{"set", "append", "delete"} {
			raw, _ := json.Marshal(map[string]string{"key": key, "value": "model changed it"})
			out, err := reg.ExecuteToolCalls(context.Background(), []ds4.ToolCall{{ID: "blocked", Name: "scratch_" + operation, Arguments: string(raw)}})
			if err != nil || len(out) != 1 || !strings.Contains(out[0].Content, "read-only") {
				t.Fatalf("mutation was not rejected: %+v %v", out, err)
			}
			got := callTest(t, m, "session", "get", map[string]string{"key": key})
			if !strings.HasSuffix(got, "host value") {
				t.Fatalf("protected value changed: %s", got)
			}
		}
	}
	for _, name := range []string{"scratch_set", "global_scratch_set"} {
		out, err := reg.ExecuteToolCalls(context.Background(), []ds4.ToolCall{{ID: "ok", Name: name, Arguments: `{"key":"findings","value":"useful note"}`}})
		if err != nil || !strings.Contains(out[0].Content, "OK:") {
			t.Fatalf("ordinary notes broken: %+v %v", out, err)
		}
	}
}

func TestIndependentHandlesSerializeAppendAndCancellation(t *testing.T) {
	dir := t.TempDir()
	a, b := openTest(t, dir, "a"), openTest(t, dir, "b")
	var wg sync.WaitGroup
	for _, m := range []*Memory{a, b} {
		wg.Go(func() {
			for i := 0; i < 40; i++ {
				out, err := m.Call(context.Background(), "global", "append", json.RawMessage(`{"key":"counter","value":"x"}`))
				if err != nil || strings.HasPrefix(out, "ERROR:") {
					t.Errorf("%s %v", out, err)
					return
				}
			}
		})
	}
	wg.Wait()
	out := callTest(t, a, "global", "get", map[string]string{"key": "counter"})
	_, value, _ := strings.Cut(out, "\n")
	if value != strings.Repeat("x", 80) {
		t.Fatalf("lost append: %q", value)
	}
	fd := int(a.pads["global"].lock.Fd())
	if err := unix.Flock(fd, unix.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	defer unix.Flock(fd, unix.LOCK_UN)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err := b.Call(ctx, "global", "list", json.RawMessage(`{}`)); err != context.DeadlineExceeded {
		t.Fatalf("lock cancellation: %v", err)
	}
}

func TestBriefBoundedAndErrorsVisible(t *testing.T) {
	m := openTest(t, t.TempDir(), "test")
	callTest(t, m, "global", "set", map[string]string{"key": "lessons", "value": strings.Repeat("fact ", 3000)})
	if err := m.Set(context.Background(), "plan", strings.Repeat("plan ", 3000)); err != nil {
		t.Fatal(err)
	}
	brief, err := m.Brief(context.Background())
	if err != nil || len(brief) > 3000 || !strings.Contains(brief, "truncated=true") {
		t.Fatalf("brief length %d: %v", len(brief), err)
	}
	if err := m.Set(context.Background(), "plan", strings.Repeat("x", 17000)); err == nil {
		t.Fatal("quota failure hidden")
	}
}
