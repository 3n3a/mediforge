package config

import (
	"path/filepath"
	"strings"
	"testing"
)

// setEnv applies env via t.Setenv so it's reset after the test. Keys with
// an empty string value are unset so LoadMaster doesn't see them.
func setEnv(t *testing.T, kv map[string]string) {
	t.Helper()
	for k, v := range kv {
		if v == "" {
			t.Setenv(k, "")
			continue
		}
		t.Setenv(k, v)
	}
}

// baseEnv is the minimum viable master config — one library + worker creds.
func baseEnv(t *testing.T, libRoot string) map[string]string {
	return map[string]string{
		"MEDIA_LIBRARIES": "tv:" + libRoot,
		"WORKER_URL":      "http://localhost:8080",
		"WORKER_TOKEN":    "secret",
	}
}

func TestLoadMaster_ReplaceModeDefault(t *testing.T) {
	dir := t.TempDir()
	setEnv(t, baseEnv(t, dir))

	cfg, err := LoadMaster()
	if err != nil {
		t.Fatalf("LoadMaster: %v", err)
	}
	if cfg.ArchiveMode != "replace" {
		t.Fatalf("ArchiveMode = %q, want replace", cfg.ArchiveMode)
	}
	if cfg.ArchiveDir != "" {
		t.Fatalf("ArchiveDir = %q, want empty in replace mode", cfg.ArchiveDir)
	}
}

func TestLoadMaster_ArchiveModeRequiresArchiveDir(t *testing.T) {
	dir := t.TempDir()
	env := baseEnv(t, dir)
	env["ARCHIVE_MODE"] = "archive"
	setEnv(t, env)

	_, err := LoadMaster()
	if err == nil {
		t.Fatal("expected error when ARCHIVE_DIR is missing")
	}
	if !strings.Contains(err.Error(), "ARCHIVE_DIR") {
		t.Fatalf("error should mention ARCHIVE_DIR: %v", err)
	}
}

func TestLoadMaster_ArchiveModeRejectsRelativeDir(t *testing.T) {
	dir := t.TempDir()
	env := baseEnv(t, dir)
	env["ARCHIVE_MODE"] = "archive"
	env["ARCHIVE_DIR"] = "./relative/path"
	setEnv(t, env)

	_, err := LoadMaster()
	if err == nil {
		t.Fatal("expected error on relative ARCHIVE_DIR")
	}
	if !strings.Contains(err.Error(), "absolute") {
		t.Fatalf("error should mention absolute: %v", err)
	}
}

func TestLoadMaster_ArchiveModeRejectsInsideLibrary(t *testing.T) {
	libRoot := t.TempDir()
	archive := filepath.Join(libRoot, "archived-under-lib")
	env := baseEnv(t, libRoot)
	env["ARCHIVE_MODE"] = "archive"
	env["ARCHIVE_DIR"] = archive
	setEnv(t, env)

	_, err := LoadMaster()
	if err == nil {
		t.Fatal("expected error when ARCHIVE_DIR is inside library root")
	}
	if !strings.Contains(err.Error(), "inside library") {
		t.Fatalf("error should mention inside library: %v", err)
	}
}

func TestLoadMaster_ArchiveModeValid(t *testing.T) {
	libRoot := t.TempDir()
	archive := t.TempDir()
	env := baseEnv(t, libRoot)
	env["ARCHIVE_MODE"] = "archive"
	env["ARCHIVE_DIR"] = archive
	setEnv(t, env)

	cfg, err := LoadMaster()
	if err != nil {
		t.Fatalf("LoadMaster: %v", err)
	}
	if cfg.ArchiveMode != "archive" {
		t.Fatalf("ArchiveMode = %q", cfg.ArchiveMode)
	}
	if cfg.ArchiveDir != archive {
		t.Fatalf("ArchiveDir = %q, want %q", cfg.ArchiveDir, archive)
	}
}

func TestLoadMaster_ArchiveModeCreatesDir(t *testing.T) {
	libRoot := t.TempDir()
	archive := filepath.Join(t.TempDir(), "nested", "new-archive")
	env := baseEnv(t, libRoot)
	env["ARCHIVE_MODE"] = "archive"
	env["ARCHIVE_DIR"] = archive
	setEnv(t, env)

	cfg, err := LoadMaster()
	if err != nil {
		t.Fatalf("LoadMaster: %v", err)
	}
	if cfg.ArchiveDir != archive {
		t.Fatalf("ArchiveDir = %q, want %q", cfg.ArchiveDir, archive)
	}
}

func TestLoadMaster_RejectsUnknownArchiveMode(t *testing.T) {
	env := baseEnv(t, t.TempDir())
	env["ARCHIVE_MODE"] = "teleport"
	setEnv(t, env)

	_, err := LoadMaster()
	if err == nil {
		t.Fatal("expected error on invalid mode")
	}
	if !strings.Contains(err.Error(), "unsupported") && !strings.Contains(err.Error(), "ARCHIVE_MODE") {
		t.Fatalf("error should reject invalid mode: %v", err)
	}
}
