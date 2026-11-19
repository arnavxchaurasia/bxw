package cache

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"bxw/internal/osport"
)

// Put stores data in the content-addressed cache and returns its resource URI and SHA-256 hash.
func Put(data []byte, ext string) (uri string, hash string, err error) {
	p := osport.Current()
	cacheDir := p.CacheDir()
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return "", "", err
	}

	h := sha256.Sum256(data)
	hash = hex.EncodeToString(h[:])
	fileName := hash
	if ext != "" {
		if !strings.HasPrefix(ext, ".") {
			ext = "." + ext
		}
		fileName += ext
	}

	target := filepath.Join(cacheDir, fileName)
	if err := os.WriteFile(target, data, 0o644); err != nil {
		return "", "", err
	}

	uri = fmt.Sprintf("bxw://cache/%s", fileName)
	return uri, hash, nil
}

// Get retrieves data from the cache by filename or hash.
func Get(ref string) ([]byte, error) {
	p := osport.Current()
	ref = strings.TrimPrefix(ref, "bxw://cache/")
	target := filepath.Join(p.CacheDir(), ref)
	return os.ReadFile(target)
}

// FilePath returns the absolute disk path for a cached resource.
func FilePath(ref string) string {
	p := osport.Current()
	ref = strings.TrimPrefix(ref, "bxw://cache/")
	return filepath.Join(p.CacheDir(), ref)
}

// EvictLRU enforces the maxCacheMB limit by removing the oldest accessed files.
func EvictLRU(maxMB int) (evictedCount int, evictedBytes int64, err error) {
	if maxMB <= 0 {
		return 0, 0, nil
	}
	maxBytes := int64(maxMB) * 1024 * 1024

	p := osport.Current()
	cacheDir := p.CacheDir()

	entries, err := os.ReadDir(cacheDir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, 0, nil
		}
		return 0, 0, err
	}

	type fileMeta struct {
		path    string
		size    int64
		modTime time.Time
	}

	var files []fileMeta
	var totalBytes int64

	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		files = append(files, fileMeta{
			path:    filepath.Join(cacheDir, e.Name()),
			size:    info.Size(),
			modTime: info.ModTime(),
		})
		totalBytes += info.Size()
	}

	if totalBytes <= maxBytes {
		return 0, 0, nil
	}

	// Sort oldest first
	sort.Slice(files, func(i, j int) bool {
		return files[i].modTime.Before(files[j].modTime)
	})

	for _, f := range files {
		if totalBytes <= maxBytes {
			break
		}
		if err := os.Remove(f.path); err == nil {
			totalBytes -= f.size
			evictedBytes += f.size
			evictedCount++
		}
	}

	return evictedCount, evictedBytes, nil
}

// Clean removes all cached items.
func Clean() (int, int64, error) {
	p := osport.Current()
	cacheDir := p.CacheDir()
	entries, err := os.ReadDir(cacheDir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, 0, nil
		}
		return 0, 0, err
	}

	count := 0
	var bytes int64
	for _, e := range entries {
		if !e.IsDir() {
			if info, err := e.Info(); err == nil {
				bytes += info.Size()
			}
			if err := os.Remove(filepath.Join(cacheDir, e.Name())); err == nil {
				count++
			}
		}
	}
	return count, bytes, nil
}
