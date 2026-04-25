package cache

import (
	"path/filepath"
	"testing"
)

// openTestCache opens a cache in a temp directory for testing.
func openTestCache(t *testing.T) *Cache {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	c, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func TestDeleteProbePrefix(t *testing.T) {
	c := openTestCache(t)

	// Seed rows under /tv/series-A/
	c.PutProbe(ProbeEntry{
		Path:    "/tv/series-A/s01e01.mkv",
		Size:    1000,
		IsTarget: false,
	})
	c.PutProbe(ProbeEntry{
		Path:    "/tv/series-A/s01e02.mkv",
		Size:    2000,
		IsTarget: false,
	})
	// Seed row under /tv/series-B/ (should survive)
	c.PutProbe(ProbeEntry{
		Path:    "/tv/series-B/s01e01.mkv",
		Size:    3000,
		IsTarget: false,
	})

	// Delete all probes under /tv/series-A/
	n, err := c.DeleteProbePrefix("/tv/series-A/")
	if err != nil {
		t.Fatalf("DeleteProbePrefix: %v", err)
	}
	if n != 2 {
		t.Errorf("deleted %d probes, want 2", n)
	}

	// series-A rows should be gone
	_, hit, _ := c.GetProbe("/tv/series-A/s01e01.mkv")
	if hit {
		t.Error("series-A s01e01 probe unexpectedly still cached")
	}
	_, hit, _ = c.GetProbe("/tv/series-A/s01e02.mkv")
	if hit {
		t.Error("series-A s01e02 probe unexpectedly still cached")
	}

	// series-B row must survive
	_, hit, _ = c.GetProbe("/tv/series-B/s01e01.mkv")
	if !hit {
		t.Error("series-B probe unexpectedly deleted")
	}
}

func TestDeleteProbePrefix_NoMatch(t *testing.T) {
	c := openTestCache(t)

	c.PutProbe(ProbeEntry{
		Path:    "/tv/series-A/s01e01.mkv",
		Size:    1000,
		IsTarget: false,
	})

	// Delete with non-matching prefix
	n, err := c.DeleteProbePrefix("/tv/series-B/")
	if err != nil {
		t.Fatalf("DeleteProbePrefix: %v", err)
	}
	if n != 0 {
		t.Errorf("deleted %d probes, want 0", n)
	}

	// Original should survive
	_, hit, _ := c.GetProbe("/tv/series-A/s01e01.mkv")
	if !hit {
		t.Error("probe unexpectedly deleted")
	}
}

func TestDeleteJobPrefix(t *testing.T) {
	c := openTestCache(t)

	// Seed jobs under /tv/series-A/
	_, err := c.StartAttempt("/tv/series-A/s01e01.mkv", "tv")
	if err != nil {
		t.Fatalf("StartAttempt: %v", err)
	}
	_, err = c.StartAttempt("/tv/series-A/s01e02.mkv", "tv")
	if err != nil {
		t.Fatalf("StartAttempt: %v", err)
	}
	// Seed job under /tv/series-B/ (should survive)
	_, err = c.StartAttempt("/tv/series-B/s01e01.mkv", "tv")
	if err != nil {
		t.Fatalf("StartAttempt: %v", err)
	}

	// Delete all jobs under /tv/series-A/
	n, err := c.DeleteJobPrefix("/tv/series-A/")
	if err != nil {
		t.Fatalf("DeleteJobPrefix: %v", err)
	}
	if n != 2 {
		t.Errorf("deleted %d jobs, want 2", n)
	}

	// series-A rows should be gone
	_, hit, _ := c.GetJob("/tv/series-A/s01e01.mkv")
	if hit {
		t.Error("series-A s01e01 job unexpectedly still cached")
	}
	_, hit, _ = c.GetJob("/tv/series-A/s01e02.mkv")
	if hit {
		t.Error("series-A s01e02 job unexpectedly still cached")
	}

	// series-B job must survive
	_, hit, _ = c.GetJob("/tv/series-B/s01e01.mkv")
	if !hit {
		t.Error("series-B job unexpectedly deleted")
	}
}

func TestDeleteJobPrefix_NoMatch(t *testing.T) {
	c := openTestCache(t)

	_, err := c.StartAttempt("/tv/series-A/s01e01.mkv", "tv")
	if err != nil {
		t.Fatalf("StartAttempt: %v", err)
	}

	// Delete with non-matching prefix
	n, err := c.DeleteJobPrefix("/tv/series-B/")
	if err != nil {
		t.Fatalf("DeleteJobPrefix: %v", err)
	}
	if n != 0 {
		t.Errorf("deleted %d jobs, want 0", n)
	}

	// Original should survive
	_, hit, _ := c.GetJob("/tv/series-A/s01e01.mkv")
	if !hit {
		t.Error("job unexpectedly deleted")
	}
}
