package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"flag"

	"github.com/3n3a/mediforge/internal/cache"
	"github.com/3n3a/mediforge/internal/client"
	"github.com/3n3a/mediforge/internal/config"
	"github.com/3n3a/mediforge/internal/dispatch"
	"github.com/3n3a/mediforge/internal/jellyfin"
	"github.com/3n3a/mediforge/internal/logx"
	"github.com/3n3a/mediforge/internal/probe"
)

var version = "dev"

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	sub := os.Args[1]
	args := os.Args[2:]

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	switch sub {
	case "dispatch":
		os.Exit(cmdDispatch(ctx, args))
	case "probe":
		os.Exit(cmdProbe(ctx, args))
	case "cache":
		os.Exit(cmdCache(ctx, args))
	case "jobs":
		os.Exit(cmdJobs(ctx, args))
	case "version", "--version", "-v":
		fmt.Println("mediforge", version)
	case "help", "-h", "--help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", sub)
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `mediforge — encode media libraries to H.264 High + AAC MP4

Usage:
  mediforge dispatch [library...]       Walk libraries, encode files not in target format
  mediforge probe <file>                Probe a single file, print JSON
  mediforge cache stats                 Show probe/jobs statistics
  mediforge cache evict <path>          Forget a path (probe + jobs)
  mediforge jobs list [--status=STATUS] List jobs (status: active|done|failed|failed_permanent)
  mediforge jobs retry <path>           Clear failed_permanent flag, reset attempts to 0
  mediforge version                     Print version

Flags for 'dispatch':
  --dry-run              Probe and log decisions; do not upload or mutate files.
  --force                Ignore probe cache and failed_permanent flag.
  --max-retries N        Override MAX_RETRIES for this run.
  --log-level LEVEL      debug|info|warn|error
  --db PATH              Override MEDIFORGE_DB.

Environment:
  MEDIA_LIBRARIES, WORKER_URL, WORKER_TOKEN, ARCHIVE_MODE, MAX_RETRIES,
  MEDIFORGE_DB, HTTP_TIMEOUT_UPLOAD, HTTP_TIMEOUT_ENCODE, MEDIA_EXTENSIONS,
  LOG_LEVEL, JELLYFIN_INTEGRATION_ENABLED, JELLYFIN_URL, JELLYFIN_API_KEY`)
}

// ---------------- dispatch ----------------

func cmdDispatch(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("dispatch", flag.ExitOnError)
	dryRun := fs.Bool("dry-run", false, "do not encode, just report")
	force := fs.Bool("force", false, "ignore cache and failed_permanent flag")
	maxRetries := fs.Int("max-retries", 0, "override MAX_RETRIES for this run (0 = use env)")
	logLevel := fs.String("log-level", "", "override LOG_LEVEL")
	dbOverride := fs.String("db", "", "override MEDIFORGE_DB")
	_ = fs.Parse(args)

	if *dbOverride != "" {
		_ = os.Setenv("MEDIFORGE_DB", *dbOverride)
	}

	cfg, err := config.LoadMaster()
	if err != nil {
		fmt.Fprintln(os.Stderr, "config:", err)
		return 2
	}
	if *logLevel != "" {
		cfg.LogLevel = *logLevel
	}
	log := logx.New(cfg.LogLevel, cfg.LogFormat)
	slog.SetDefault(log)

	libs, err := cfg.FilterLibraries(fs.Args())
	if err != nil {
		fmt.Fprintln(os.Stderr, "library selection:", err)
		return 2
	}
	for _, lib := range libs {
		if st, err := os.Stat(lib.Root); err != nil || !st.IsDir() {
			fmt.Fprintf(os.Stderr, "library %q: root %s is not a directory\n", lib.Name, lib.Root)
			return 2
		}
	}

	if err := os.MkdirAll(filepath.Dir(cfg.DBPath), 0o755); err != nil {
		fmt.Fprintln(os.Stderr, "mkdir db:", err)
		return 2
	}
	c, err := cache.Open(cfg.DBPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "open cache:", err)
		return 2
	}
	defer c.Close()

	cli := client.New(cfg.WorkerURL, cfg.WorkerToken, cfg.UploadTimeout, cfg.EncodeTimeout)

	opts := dispatch.Options{
		DryRun:     *dryRun,
		Force:      *force,
		MaxRetries: cfg.MaxRetries,
		FFmpegBin:  cfg.FFmpegBin,
		FFprobeBin: cfg.FFprobeBin,
		Libraries:  libs,
	}
	if *maxRetries > 0 {
		opts.MaxRetries = *maxRetries
	}
	if cfg.JellyfinEnabled {
		opts.Jellyfin = jellyfin.New(cfg.JellyfinURL, cfg.JellyfinAPIKey)
	}

	runner := dispatch.NewRunner(cfg, c, cli, log)
	code, err := runner.Run(ctx, opts)
	if err != nil {
		log.Error("dispatch failed", slog.String("err", err.Error()))
	}
	return code
}

// ---------------- probe ----------------

func cmdProbe(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("probe", flag.ExitOnError)
	_ = fs.Parse(args)
	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "usage: mediforge probe <file>")
		return 2
	}
	file := fs.Arg(0)

	st, err := os.Stat(file)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	if st.IsDir() {
		fmt.Fprintln(os.Stderr, "expected a file")
		return 2
	}

	ffprobeBin := "ffprobe"
	if cfg, err := config.LoadMaster(); err == nil {
		ffprobeBin = cfg.FFprobeBin
	}

	pctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	res, err := probe.Run(pctx, ffprobeBin, file)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	// Opportunistically update cache if configured.
	if _, err := config.LoadMaster(); err == nil {
		cfg, _ := config.LoadMaster()
		if c, cerr := cache.Open(cfg.DBPath); cerr == nil {
			_ = c.PutProbe(cache.ProbeEntry{
				Path:         file,
				Size:         st.Size(),
				MtimeUnix:    st.ModTime().Unix(),
				Container:    res.Container,
				VideoCodec:   res.VideoCodec,
				VideoProfile: res.VideoProfile,
				VideoLevel:   res.VideoLevel,
				AudioCodec:   res.AudioCodec,
				IsTarget:     res.IsTarget,
				ProbedAtUnix: time.Now().Unix(),
			})
			_ = c.Close()
		}
	}

	out := map[string]any{
		"path":          file,
		"container":     res.Container,
		"video_codec":   res.VideoCodec,
		"video_profile": res.VideoProfile,
		"video_level":   res.VideoLevel,
		"audio_codec":   res.AudioCodec,
		"is_target":     res.IsTarget,
	}
	if res.Reason != "" {
		out["reason"] = res.Reason
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(out)
	return 0
}

// ---------------- cache ----------------

func cmdCache(ctx context.Context, args []string) int {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: mediforge cache {stats|evict <path>}")
		return 2
	}
	cfg, err := config.LoadMaster()
	if err != nil {
		fmt.Fprintln(os.Stderr, "config:", err)
		return 2
	}
	c, err := cache.Open(cfg.DBPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "open cache:", err)
		return 2
	}
	defer c.Close()

	switch args[0] {
	case "stats":
		s, err := c.Stats()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		fmt.Printf("probes:      %d total (%d in target format)\n", s.ProbeRows, s.ProbeTargets)
		fmt.Printf("jobs:        active=%d done=%d failed=%d failed_permanent=%d\n",
			s.JobsActive, s.JobsDone, s.JobsFailed, s.JobsPermanent)
		if st, err := os.Stat(cfg.DBPath); err == nil {
			fmt.Printf("db file:     %s (%s)\n", cfg.DBPath, humanBytes(st.Size()))
		}
		return 0
	case "evict":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: mediforge cache evict <path>")
			return 2
		}
		path := args[1]
		if err := c.DeleteProbe(path); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		if err := c.DeleteJob(path); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		fmt.Printf("evicted %s\n", path)
		return 0
	default:
		fmt.Fprintln(os.Stderr, "unknown cache subcommand:", args[0])
		return 2
	}
}

// ---------------- jobs ----------------

func cmdJobs(ctx context.Context, args []string) int {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: mediforge jobs {list [--status=STATUS]|retry <path>}")
		return 2
	}
	cfg, err := config.LoadMaster()
	if err != nil {
		fmt.Fprintln(os.Stderr, "config:", err)
		return 2
	}
	c, err := cache.Open(cfg.DBPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "open cache:", err)
		return 2
	}
	defer c.Close()

	switch args[0] {
	case "list":
		fs := flag.NewFlagSet("jobs list", flag.ExitOnError)
		statusFilter := fs.String("status", "", "active|done|failed|failed_permanent")
		_ = fs.Parse(args[1:])
		jobs, err := c.ListJobs(*statusFilter)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		if len(jobs) == 0 {
			fmt.Println("(no jobs)")
			return 0
		}
		for _, j := range jobs {
			ts := ""
			if j.LastAttemptUnix > 0 {
				ts = time.Unix(j.LastAttemptUnix, 0).Format(time.RFC3339)
			}
			line := fmt.Sprintf("%-18s attempts=%d last=%s path=%s",
				j.Status, j.Attempts, ts, j.Path)
			if j.LastError != "" {
				line += fmt.Sprintf(" err=%q", truncate(j.LastError, 120))
			}
			fmt.Println(line)
		}
		return 0
	case "retry":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: mediforge jobs retry <path>")
			return 2
		}
		ok, err := c.ResetJob(args[1])
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		if !ok {
			fmt.Fprintln(os.Stderr, "no job found for:", args[1])
			return 1
		}
		fmt.Println("reset:", args[1])
		return 0
	default:
		fmt.Fprintln(os.Stderr, "unknown jobs subcommand:", args[0])
		return 2
	}
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for nn := n / unit; nn >= unit; nn /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return strings.TrimSpace(s[:n]) + "…"
}
