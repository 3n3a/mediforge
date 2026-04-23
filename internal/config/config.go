package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Library struct {
	Name string
	Root string
}

type Master struct {
	Libraries         []Library
	WorkerURL         string
	WorkerToken       string
	ArchiveMode       string
	ArchiveDir        string
	MaxRetries        int
	DBPath            string
	UploadTimeout     time.Duration
	EncodeTimeout     time.Duration
	MediaExtensions   []string
	FFmpegBin         string
	FFprobeBin        string
	LogLevel          string
	LogFormat         string
	JellyfinEnabled   bool
	JellyfinURL       string
	JellyfinAPIKey    string
}

type Worker struct {
	ListenAddr  string
	WorkerToken string
	WorkDir     string
	FFmpegBin   string
	FFprobeBin  string
	Encoder     string
	LogLevel    string
	LogFormat   string
}

var defaultExtensions = []string{"mkv", "avi", "mp4", "mov", "ts", "wmv", "flv", "m4v", "webm"}

func LoadMaster() (*Master, error) {
	cfg := &Master{
		ArchiveMode:     getenv("ARCHIVE_MODE", "replace"),
		DBPath:          getenv("MEDIFORGE_DB", "/var/lib/mediforge/mediforge.db"),
		FFmpegBin:       getenv("FFMPEG_BIN", "ffmpeg"),
		FFprobeBin:      getenv("FFPROBE_BIN", "ffprobe"),
		LogLevel:        getenv("LOG_LEVEL", "info"),
		LogFormat:       getenv("LOG_FORMAT", "text"),
		JellyfinEnabled: getenvBool("JELLYFIN_INTEGRATION_ENABLED", false),
	}

	libsRaw := os.Getenv("MEDIA_LIBRARIES")
	if libsRaw == "" {
		return nil, errors.New("MEDIA_LIBRARIES is required (format: name:/path,name2:/path2)")
	}
	libs, err := parseLibraries(libsRaw)
	if err != nil {
		return nil, fmt.Errorf("MEDIA_LIBRARIES: %w", err)
	}
	cfg.Libraries = libs

	cfg.WorkerURL = os.Getenv("WORKER_URL")
	if cfg.WorkerURL == "" {
		return nil, errors.New("WORKER_URL is required")
	}
	cfg.WorkerToken = os.Getenv("WORKER_TOKEN")
	if cfg.WorkerToken == "" {
		return nil, errors.New("WORKER_TOKEN is required")
	}

	switch cfg.ArchiveMode {
	case "replace":
	case "archive":
		cfg.ArchiveDir = os.Getenv("ARCHIVE_DIR")
		if cfg.ArchiveDir == "" {
			return nil, errors.New("ARCHIVE_MODE=archive requires ARCHIVE_DIR")
		}
		if !filepath.IsAbs(cfg.ArchiveDir) {
			return nil, fmt.Errorf("ARCHIVE_DIR %q must be an absolute path", cfg.ArchiveDir)
		}
		cfg.ArchiveDir = filepath.Clean(cfg.ArchiveDir)
		for _, lib := range cfg.Libraries {
			if cfg.ArchiveDir == lib.Root || strings.HasPrefix(cfg.ArchiveDir, lib.Root+string(filepath.Separator)) {
				return nil, fmt.Errorf("ARCHIVE_DIR %q must not be inside library %q root %q", cfg.ArchiveDir, lib.Name, lib.Root)
			}
		}
		if err := os.MkdirAll(cfg.ArchiveDir, 0o755); err != nil {
			return nil, fmt.Errorf("create ARCHIVE_DIR %q: %w", cfg.ArchiveDir, err)
		}
	default:
		return nil, fmt.Errorf("ARCHIVE_MODE=%q unsupported (replace|archive)", cfg.ArchiveMode)
	}

	maxRetries, err := getenvInt("MAX_RETRIES", 2)
	if err != nil {
		return nil, err
	}
	if maxRetries < 1 {
		return nil, errors.New("MAX_RETRIES must be >= 1")
	}
	cfg.MaxRetries = maxRetries

	cfg.UploadTimeout, err = getenvDuration("HTTP_TIMEOUT_UPLOAD", 30*time.Minute)
	if err != nil {
		return nil, err
	}
	cfg.EncodeTimeout, err = getenvDuration("HTTP_TIMEOUT_ENCODE", 6*time.Hour)
	if err != nil {
		return nil, err
	}

	if ext := os.Getenv("MEDIA_EXTENSIONS"); ext != "" {
		cfg.MediaExtensions = splitCSV(ext)
	} else {
		cfg.MediaExtensions = append(cfg.MediaExtensions, defaultExtensions...)
	}
	for i, e := range cfg.MediaExtensions {
		cfg.MediaExtensions[i] = strings.ToLower(strings.TrimPrefix(e, "."))
	}

	if cfg.JellyfinEnabled {
		cfg.JellyfinURL = os.Getenv("JELLYFIN_URL")
		cfg.JellyfinAPIKey = os.Getenv("JELLYFIN_API_KEY")
		if cfg.JellyfinURL == "" || cfg.JellyfinAPIKey == "" {
			return nil, errors.New("JELLYFIN_INTEGRATION_ENABLED=true requires JELLYFIN_URL and JELLYFIN_API_KEY")
		}
	}

	return cfg, nil
}

func LoadWorker() (*Worker, error) {
	cfg := &Worker{
		ListenAddr: getenv("LISTEN_ADDR", ":8080"),
		WorkDir:    getenv("WORK_DIR", filepath.Join(homeDir(), ".mediforge", "work")),
		FFmpegBin:  getenv("FFMPEG_BIN", "ffmpeg"),
		FFprobeBin: getenv("FFPROBE_BIN", "ffprobe"),
		Encoder:    getenv("ENCODER", "auto"),
		LogLevel:   getenv("LOG_LEVEL", "info"),
		LogFormat:  getenv("LOG_FORMAT", "text"),
	}
	cfg.WorkerToken = os.Getenv("WORKER_TOKEN")
	if cfg.WorkerToken == "" {
		return nil, errors.New("WORKER_TOKEN is required")
	}

	switch cfg.Encoder {
	case "auto", "videotoolbox", "libx264":
	default:
		return nil, fmt.Errorf("ENCODER=%q invalid (auto|videotoolbox|libx264)", cfg.Encoder)
	}

	return cfg, nil
}

// FilterLibraries returns the libraries matching the provided names, or all if names is empty.
// Returns an error if any name does not exist.
func (m *Master) FilterLibraries(names []string) ([]Library, error) {
	if len(names) == 0 {
		return m.Libraries, nil
	}
	byName := make(map[string]Library, len(m.Libraries))
	for _, lib := range m.Libraries {
		byName[lib.Name] = lib
	}
	out := make([]Library, 0, len(names))
	for _, n := range names {
		lib, ok := byName[n]
		if !ok {
			return nil, fmt.Errorf("unknown library %q (defined: %s)", n, libraryNames(m.Libraries))
		}
		out = append(out, lib)
	}
	return out, nil
}

func parseLibraries(raw string) ([]Library, error) {
	parts := strings.Split(raw, ",")
	out := make([]Library, 0, len(parts))
	seen := make(map[string]bool)
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		colon := strings.Index(p, ":")
		if colon <= 0 || colon == len(p)-1 {
			return nil, fmt.Errorf("invalid library entry %q (expected name:/abs/path)", p)
		}
		name := strings.TrimSpace(p[:colon])
		root := strings.TrimSpace(p[colon+1:])
		if name == "" {
			return nil, fmt.Errorf("invalid library entry %q (empty name)", p)
		}
		if !filepath.IsAbs(root) {
			return nil, fmt.Errorf("library %q: path %q is not absolute", name, root)
		}
		if seen[name] {
			return nil, fmt.Errorf("duplicate library name %q", name)
		}
		seen[name] = true
		out = append(out, Library{Name: name, Root: filepath.Clean(root)})
	}
	if len(out) == 0 {
		return nil, errors.New("no libraries defined")
	}
	return out, nil
}

func libraryNames(libs []Library) string {
	names := make([]string, len(libs))
	for i, l := range libs {
		names[i] = l.Name
	}
	return strings.Join(names, ",")
}

func getenv(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}

func getenvInt(key string, fallback int) (int, error) {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return fallback, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("%s=%q is not an integer", key, v)
	}
	return n, nil
}

func getenvDuration(key string, fallback time.Duration) (time.Duration, error) {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return fallback, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("%s=%q: %w", key, v, err)
	}
	return d, nil
}

func getenvBool(key string, fallback bool) bool {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return fallback
	}
	switch strings.ToLower(v) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	}
	return fallback
}

func splitCSV(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func homeDir() string {
	if h, err := os.UserHomeDir(); err == nil {
		return h
	}
	return "/tmp"
}
