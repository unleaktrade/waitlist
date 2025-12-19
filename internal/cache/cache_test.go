package cache

import (
	"reflect"
	"testing"
)

func TestCacheAddAndIsPresent(t *testing.T) {
	c := New()
	if c == nil {
		t.Fatal("cache is nil")
	}

	key := "user-1"
	if c.IsPresent(key) {
		t.Fatalf("IsPresent(%q) = true, want false", key)
	}

	ts := int64(1700000000)
	c.Add(key, ts)

	if !c.IsPresent(key) {
		t.Fatalf("IsPresent(%q) = false, want true", key)
	}
	if got := c.m[key]; got != ts {
		t.Fatalf("timestamp mismatch, got %d, want %d", got, ts)
	}
}

func TestCacheFillSwapsMap(t *testing.T) {
	c := New()
	c.Add("old", 1)

	entries := map[string]int64{
		"new": 42,
	}
	c.Fill(entries)

	if c.IsPresent("old") {
		t.Fatalf("old entry should not be present after Fill")
	}
	if !c.IsPresent("new") {
		t.Fatalf("new entry should be present after Fill")
	}
	if got := c.m["new"]; got != 42 {
		t.Fatalf("timestamp mismatch, got %d, want %d", got, 42)
	}

	if reflect.ValueOf(c.m).Pointer() != reflect.ValueOf(entries).Pointer() {
		t.Fatalf("Fill did not swap the backing map")
	}
}

func TestCacheFillNil(t *testing.T) {
	c := New()
	c.Add("old", 1)

	c.Fill(nil)

	if c.IsPresent("old") {
		t.Fatalf("entry should not be present after Fill(nil)")
	}
	if c.m == nil || len(c.m) != 0 {
		t.Fatalf("expected empty map after Fill(nil)")
	}
}
