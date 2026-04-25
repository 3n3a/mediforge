package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/3n3a/mediforge/internal/cache"
	"github.com/3n3a/mediforge/internal/client"
	"github.com/3n3a/mediforge/internal/config"
	"github.com/3n3a/mediforge/internal/dispatch"
	"github.com/3n3a/mediforge/internal/jellyfin"
	"github.com/3n3a/mediforge/internal/logx"
	"github.com/3n3a/mediforge/internal/probe"
	"github.com/spf13/cobra"
)

var version = "dev"

func main() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

var rootCmd = &cobra.Command{
	Use:   "mediforge",
	Short: "H.264 High + AAC MP4 media encoder",
}

// ============ dispatch ============

var dispatchCmd = &cobra.Command{
	Use:   "dispatch [library...]",
	Short: "Walk libraries, encode files not in target format",
	RunE:  runDispatch,
}

func init() {
	dispatchCmd.Flags().Bool("dry-run", false, "probe and log; do not upload or mutate files")
	dispatchCmd.Flags().Bool("force", false, "ignore cache and failed_permanent flag")
	dispatchCmd.Flags().Int("max-retries", 0, "override MAX_RETRIES for this run (0 = use env)")
	dispatchCmd.Flags().String("path-prefix", "", "only process files under this path")
	dispatchCmd.Flags().String("log-level", "", "override LOG_LEVEL (debug|info|warn|error)")
	dispatchCmd.Flags().String("db", "", "override MEDIFORGE_DB")
	rootCmd.AddCommand(dispatchCmd, probeCmd, versionCmd)
}

func runDispatch(cmd *cobra.Command, args []string) error {
	dryRun, _ := cmd.Flags().GetBool("dry-run")
	force, _ := cmd.Flags().GetBool("force")
	maxRetries, _ := cmd.Flags().GetInt("max-retries")
	pathPrefix, _ := cmd.Flags().GetString("path-prefix")
	logLevel, _ := cmd.Flags().GetString("log-level")
	dbOverride, _ := cmd.Flags().GetString("db")

	if dbOverride != "" {
		_ = os.Setenv("MEDIFORGE_DB", dbOverride)
	}

	cfg, err := config.LoadMaster()
	if err != nil {
		fmt.Fprintf(os.Stderr, "config: %v\n", err)
		return err
	}
	if logLevel != "" {
		cfg.LogLevel = logLevel
	}
	log := logx.New(cfg.LogLevel, cfg.LogFormat)
	slog.SetDefault(log)

	libs, err := cfg.FilterLibraries(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "library selection: %v\n", err)
		return err
	}
	for _, lib := range libs {
		if st, err := os.Stat(lib.Root); err != nil || !st.IsDir() {
			fmt.Fprintf(os.Stderr, "library %q: root %s is not a directory\n", lib.Name, lib.Root)
			return err
		}
	}

	if err := os.MkdirAll(filepath.Dir(cfg.DBPath), 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "mkdir db: %v\n", err)
		return err
	}
	c, err := cache.Open(cfg.DBPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "open cache: %v\n", err)
		return err
	}
	defer c.Close()

	cli := client.New(cfg.WorkerURL, cfg.WorkerToken, cfg.UploadTimeout, cfg.EncodeTimeout)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	opts := dispatch.Options{
		DryRun:     dryRun,
		Force:      force,
		MaxRetries: cfg.MaxRetries,
		FFmpegBin:  cfg.FFmpegBin,
		FFprobeBin: cfg.FFprobeBin,
		Libraries:  libs,
		PathPrefix: pathPrefix,
	}
	if maxRetries > 0 {
		opts.MaxRetries = maxRetries
	}
	if cfg.JellyfinEnabled {
		opts.Jellyfin = jellyfin.New(cfg.JellyfinURL, cfg.JellyfinAPIKey)
	}

	runner := dispatch.NewRunner(cfg, c, cli, log)
	code, err := runner.Run(ctx, opts)
	if err != nil {
		log.Error("dispatch failed", slog.String("err", err.Error()))
		return err
	}
	if code != 0 {
		return fmt.Errorf("dispatch exited with code %d", code)
	}
	return nil
}

// ============ probe ============

var probeCmd = &cobra.Command{
	Use:   "probe <file>",
	Short: "Probe a single file and print JSON",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		file := args[0]
		st, err := os.Stat(file)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%v\n", err)
			return err
		}
		if st.IsDir() {
			fmt.Fprintf(os.Stderr, "expected a file\n")
			return fmt.Errorf("is a directory")
		}

		ffprobeBin := "ffprobe"
		if cfg, err := config.LoadMaster(); err == nil {
			ffprobeBin = cfg.FFprobeBin
		}

		pctx, cancel := context.WithTimeout(cmd.Context(), 60*time.Second)
		defer cancel()
		res, err := probe.Run(pctx, ffprobeBin, file)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%v\n", err)
			return err
		}

		// Opportunistically update cache if configured.
		if cfg, err := config.LoadMaster(); err == nil {
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
		return nil
	},
}

// ============ cache ============

var cacheCmd = &cobra.Command{
	Use:   "cache",
	Short: "Cache management commands",
}

var cacheStatsCmd = &cobra.Command{
	Use:   "stats",
	Short: "Show probe and jobs statistics",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.LoadMaster()
		if err != nil {
			fmt.Fprintf(os.Stderr, "config: %v\n", err)
			return err
		}
		c, err := cache.Open(cfg.DBPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "open cache: %v\n", err)
			return err
		}
		defer c.Close()

		s, err := c.Stats()
		if err != nil {
			fmt.Fprintf(os.Stderr, "%v\n", err)
			return err
		}
		fmt.Printf("probes:      %d total (%d in target format)\n", s.ProbeRows, s.ProbeTargets)
		fmt.Printf("jobs:        active=%d done=%d failed=%d failed_permanent=%d\n",
			s.JobsActive, s.JobsDone, s.JobsFailed, s.JobsPermanent)
		if st, err := os.Stat(cfg.DBPath); err == nil {
			fmt.Printf("db file:     %s (%s)\n", cfg.DBPath, humanBytes(st.Size()))
		}
		return nil
	},
}

var cacheEvictCmd = &cobra.Command{
	Use:   "evict <path>",
	Short: "Forget a path or directory (probe + jobs)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		path := args[0]
		cfg, err := config.LoadMaster()
		if err != nil {
			fmt.Fprintf(os.Stderr, "config: %v\n", err)
			return err
		}
		c, err := cache.Open(cfg.DBPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "open cache: %v\n", err)
			return err
		}
		defer c.Close()

		// If path is a directory, bulk-evict all rows under it.
		if st, err := os.Stat(path); err == nil && st.IsDir() {
			prefix := filepath.Clean(path) + string(os.PathSeparator)
			pn, err := c.DeleteProbePrefix(prefix)
			if err != nil {
				fmt.Fprintf(os.Stderr, "%v\n", err)
				return err
			}
			jn, err := c.DeleteJobPrefix(prefix)
			if err != nil {
				fmt.Fprintf(os.Stderr, "%v\n", err)
				return err
			}
			fmt.Printf("evicted %d probe(s) and %d job(s) under %s\n", pn, jn, path)
			return nil
		}

		// Single-file or non-existent path: try prefix match.
		pn, err := c.DeleteProbePrefix(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%v\n", err)
			return err
		}
		jn, err := c.DeleteJobPrefix(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%v\n", err)
			return err
		}
		if pn+jn == 0 {
			fmt.Printf("no cache entries found for %s\n", path)
			return fmt.Errorf("no cache entries")
		}
		fmt.Printf("evicted %d probe(s) and %d job(s) for %s\n", pn, jn, path)
		return nil
	},
}

func init() {
	cacheCmd.AddCommand(cacheStatsCmd, cacheEvictCmd)
}

// ============ jobs ============

var jobsCmd = &cobra.Command{
	Use:   "jobs",
	Short: "Job ledger management",
}

var jobsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List jobs with optional status filter",
	RunE: func(cmd *cobra.Command, args []string) error {
		statusFilter, _ := cmd.Flags().GetString("status")
		cfg, err := config.LoadMaster()
		if err != nil {
			fmt.Fprintf(os.Stderr, "config: %v\n", err)
			return err
		}
		c, err := cache.Open(cfg.DBPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "open cache: %v\n", err)
			return err
		}
		defer c.Close()

		jobs, err := c.ListJobs(statusFilter)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%v\n", err)
			return err
		}
		if len(jobs) == 0 {
			fmt.Println("(no jobs)")
			return nil
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
		return nil
	},
}

var jobsRetryCmd = &cobra.Command{
	Use:   "retry <path>",
	Short: "Clear failed_permanent flag and reset attempts",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		path := args[0]
		cfg, err := config.LoadMaster()
		if err != nil {
			fmt.Fprintf(os.Stderr, "config: %v\n", err)
			return err
		}
		c, err := cache.Open(cfg.DBPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "open cache: %v\n", err)
			return err
		}
		defer c.Close()

		ok, err := c.ResetJob(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%v\n", err)
			return err
		}
		if !ok {
			fmt.Fprintf(os.Stderr, "no job found for: %s\n", path)
			return fmt.Errorf("job not found")
		}
		fmt.Println("reset:", path)
		return nil
	},
}

func init() {
	jobsListCmd.Flags().String("status", "", "filter by status (active|done|failed|failed_permanent)")
	jobsCmd.AddCommand(jobsListCmd, jobsRetryCmd)
	rootCmd.AddCommand(jobsCmd, cacheCmd)
}

// ============ version ============

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print version",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("mediforge", version)
	},
}

// ============ helpers ============

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
	return s[:n] + "…"
}
