package ntgpu

import "testing"

func TestPipelineCacheLookupStoreInvalidateClear(t *testing.T) {
	var released []string
	c := NewPipelineCache[string](nil, func(v string) error {
		released = append(released, v)
		return nil
	})

	if _, ok := c.Lookup("shape", "h1"); ok {
		t.Fatal("empty cache hit")
	}
	if c.Misses() != 1 {
		t.Fatalf("misses = %d, want 1", c.Misses())
	}
	if old, ok := c.Store("shape", "h1", "pipe1"); ok || old != "" {
		t.Fatalf("unexpected old entry %q ok=%v", old, ok)
	}
	if got, ok := c.Lookup("shape", "h1"); !ok || got != "pipe1" {
		t.Fatalf("lookup = %q, %v; want pipe1 true", got, ok)
	}
	if old, ok := c.Store("shape", "h2", "pipe2"); !ok || old != "pipe1" {
		t.Fatalf("store old = %q, %v; want pipe1 true", old, ok)
	}
	if err := c.Invalidate("shape"); err != nil {
		t.Fatalf("invalidate: %v", err)
	}
	if len(released) != 1 || released[0] != "pipe2" {
		t.Fatalf("released = %#v, want [pipe2]", released)
	}

	c.Store("a", "h", "pa")
	c.Store("b", "h", "pb")
	if err := c.Clear(); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if c.Len() != 0 {
		t.Fatalf("len after clear = %d, want 0", c.Len())
	}
	if len(released) != 3 {
		t.Fatalf("released count = %d, want 3", len(released))
	}
}
