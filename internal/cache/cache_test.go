package cache

import (
	"bytes"
	"testing"
)

func TestCachePutAndGet(t *testing.T) {
	sample := []byte("<!doctype html><html><body><h1>Hello Cached World</h1></body></html>")
	uri, hash, err := Put(sample, ".html")
	if err != nil {
		t.Fatalf("Put failed: %v", err)
	}

	if hash == "" {
		t.Errorf("expected non-empty hash")
	}
	if uri != "bxw://cache/"+hash+".html" {
		t.Errorf("unexpected uri: %s", uri)
	}

	retrieved, err := Get(uri)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if !bytes.Equal(retrieved, sample) {
		t.Errorf("content mismatch")
	}

	// Test eviction calculation without deleting other tests
	_, _, err = EvictLRU(500)
	if err != nil {
		t.Errorf("EvictLRU failed: %v", err)
	}
}
