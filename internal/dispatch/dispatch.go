package dispatch

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/3n3a/mediforge/internal/archive"
	"github.com/3n3a/mediforge/internal/cache"
	"github.com/3n3a/mediforge/internal/client"
	"github.com/3n3a/mediforge/internal/config"
	"github.com/3n3a/mediforge/internal/jellyfin"
	"github.com/3n3a/mediforge/internal/probe"
	"github.com/3n3a/mediforge/internal/scan"
)

type Options struct {
	DryRun       bool
	Force        bool
	MaxRetries   int
	FFprobeBin   string
	Libraries    []config.Library
	Jellyfin     *jellyfin.Client // nil when disabled
}

type Runner struct {
	cfg    *config.Master
	cache  *cache.Cache
	client *client.Client
	log    *slog.Logger
}

type runSummary struct {
	scanned, skippedCached, skippedPerm, encoded, failed int
}

func NewRunner(cfg *config.Master, c *cache.Cache, cl *client.Client, log *slog.Logger) *Runner {
	return &Runner{cfg: cfg, cache: c, client: cl, log: log}
}

// Run executes dispatch across the selected libraries. Returns the exit code.
func (r *Runner) Run(ctx context.Context, opts Options) (int, error) {
	// 1. Startup: hit worker /health to fail fast.
	if !opts.DryRun {
		hctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		if _, err := r.client.Health(hctx); err != nil {
			return 2, fmt.Errorf("worker health check: %w", err)
		}
	}

	// 2. Lockfile — second invocation exits 0 silently.
	lockPath := r.cfg.DBPath + ".lock"
	release, gotLock, err := Lock(lockPath)
	if err != nil {
		return 2, err
	}
	if !gotLock {
		r.log.Info("another dispatch run is active; exiting", slog.String("lock", lockPath))
		return 0, nil
	}
	defer release()

	// 3. Reclaim stale 'active' jobs (crashed prior run).
	if n, err := r.cache.SweepStaleActive(); err != nil {
		r.log.Warn("sweep stale jobs failed", slog.String("err", err.Error()))
	} else if n > 0 {
		r.log.Info("reclaimed stale active jobs", slog.Int64("n", n))
	}

	start := time.Now()
	var totals runSummary
	anyFailed := false

	for _, lib := range opts.Libraries {
		summary, err := r.runLibrary(ctx, lib, opts)
		if err != nil {
			r.log.Error("library run aborted", slog.String("library", lib.Name), slog.String("err", err.Error()))
			anyFailed = true
		}
		totals.scanned += summary.scanned
		totals.skippedCached += summary.skippedCached
		totals.skippedPerm += summary.skippedPerm
		totals.encoded += summary.encoded
		totals.failed += summary.failed

		if !opts.DryRun && opts.Jellyfin != nil && summary.encoded > 0 {
			jctx, cancel := context.WithTimeout(ctx, 15*time.Second)
			if err := opts.Jellyfin.Refresh(jctx); err != nil {
				r.log.Warn("jellyfin refresh failed", slog.String("library", lib.Name), slog.String("err", err.Error()))
			} else {
				r.log.Info("jellyfin refresh triggered", slog.String("library", lib.Name))
			}
			cancel()
		}
	}

	r.log.Info("run summary",
		slog.Int("scanned", totals.scanned),
		slog.Int("skipped_cached", totals.skippedCached),
		slog.Int("skipped_perm_failed", totals.skippedPerm),
		slog.Int("encoded", totals.encoded),
		slog.Int("failed", totals.failed),
		slog.Duration("elapsed", time.Since(start)),
	)

	if anyFailed || totals.failed > 0 {
		return 1, nil
	}
	return 0, nil
}

func (r *Runner) runLibrary(ctx context.Context, lib config.Library, opts Options) (runSummary, error) {
	var s runSummary
	libStart := time.Now()

	files, err := scan.Walk(lib.Root, r.cfg.MediaExtensions)
	if err != nil {
		return s, fmt.Errorf("walk %s: %w", lib.Root, err)
	}
	r.log.Info("library scan", slog.String("library", lib.Name), slog.Int("files", len(files)))

	for _, file := range files {
		if ctx.Err() != nil {
			return s, ctx.Err()
		}
		s.scanned++
		r.processFile(ctx, lib, file, opts, &s)
	}

	r.log.Info("library summary",
		slog.String("library", lib.Name),
		slog.Int("scanned", s.scanned),
		slog.Int("skipped_cached", s.skippedCached),
		slog.Int("skipped_perm_failed", s.skippedPerm),
		slog.Int("encoded", s.encoded),
		slog.Int("failed", s.failed),
		slog.Duration("elapsed", time.Since(libStart)),
	)
	return s, nil
}

func (r *Runner) processFile(ctx context.Context, lib config.Library, file string, opts Options, s *runSummary) {
	logger := r.log.With(slog.String("library", lib.Name), slog.String("file", trimRoot(file, lib.Root)))

	st, err := os.Stat(file)
	if err != nil {
		logger.Error("stat", slog.String("action", "error"), slog.String("err", err.Error()))
		s.failed++
		return
	}
	if st.IsDir() {
		return
	}

	// ---------------- probe cache ----------------
	entry, hit, err := r.cache.GetProbe(file)
	if err != nil {
		logger.Error("probe cache get", slog.String("err", err.Error()))
		s.failed++
		return
	}
	needProbe := !hit || entry.Size != st.Size() || entry.MtimeUnix != st.ModTime().Unix() || opts.Force
	if needProbe {
		pctx, cancel := context.WithTimeout(ctx, 60*time.Second)
		res, perr := probe.Run(pctx, opts.FFprobeBin, file)
		cancel()
		if perr != nil {
			logger.Error("probe failed", slog.String("action", "error"), slog.String("err", perr.Error()))
			s.failed++
			return
		}
		entry = cache.ProbeEntry{
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
		}
		if err := r.cache.PutProbe(entry); err != nil {
			logger.Warn("probe cache put", slog.String("err", err.Error()))
		}
	}

	if entry.IsTarget {
		reason := "cache_hit"
		if needProbe {
			reason = "target"
		}
		logger.Info("skip", slog.String("action", "skip"), slog.String("reason", reason))
		s.skippedCached++
		return
	}

	// ---------------- permanent-failure gate ----------------
	job, hasJob, err := r.cache.GetJob(file)
	if err != nil {
		logger.Error("jobs get", slog.String("err", err.Error()))
		s.failed++
		return
	}
	if hasJob && job.Status == cache.StatusPermanent && !opts.Force {
		logger.Info("skip",
			slog.String("action", "skip"),
			slog.String("reason", "permanent_failure"),
			slog.Int("attempts", job.Attempts),
			slog.String("last_error", job.LastError),
		)
		s.skippedPerm++
		return
	}

	reason := entry.Container
	if entry.VideoCodec != "h264" {
		reason = "video_codec=" + entry.VideoCodec
	} else if entry.VideoProfile != "high" {
		reason = "video_profile=" + entry.VideoProfile
	} else if entry.AudioCodec != "aac" {
		reason = "audio_codec=" + entry.AudioCodec
	} else {
		reason = "container=" + entry.Container
	}

	if opts.DryRun {
		logger.Info("would encode", slog.String("action", "would_encode"), slog.String("reason", reason))
		return
	}

	// ---------------- attempt ----------------
	jobAfter, err := r.cache.StartAttempt(file, lib.Name)
	if err != nil {
		logger.Error("jobs start attempt", slog.String("err", err.Error()))
		s.failed++
		return
	}
	attempt := jobAfter.Attempts
	attemptStart := time.Now()

	tmp := archive.TmpPathFor(file)
	// Best-effort clean if an old tmp lingers.
	_ = os.Remove(tmp)

	encStart := time.Now()
	res, err := r.client.Encode(ctx, file, tmp)
	encDuration := time.Since(encStart)
	if err != nil {
		_ = os.Remove(tmp)
		code := "network"
		if werr := (*client.WorkerError)(nil); errors.As(err, &werr) {
			code = werr.Code
		} else if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			code = "timeout"
		}
		if ferr := r.cache.FailAttempt(file, code, err.Error(), opts.MaxRetries); ferr != nil {
			logger.Warn("jobs fail attempt", slog.String("err", ferr.Error()))
		}
		willRetry := attempt < opts.MaxRetries
		logger.Warn("encode error",
			slog.String("action", "error"),
			slog.String("reason", code),
			slog.String("msg", err.Error()),
			slog.Int("attempt", attempt),
			slog.Bool("will_retry", willRetry),
		)
		s.failed++
		return
	}

	finalPath, err := archive.SafeReplace(file, tmp)
	if err != nil {
		_ = os.Remove(tmp)
		if ferr := r.cache.FailAttempt(file, "archive_failed", err.Error(), opts.MaxRetries); ferr != nil {
			logger.Warn("jobs fail attempt", slog.String("err", ferr.Error()))
		}
		logger.Error("archive failed", slog.String("action", "error"), slog.String("err", err.Error()))
		s.failed++
		return
	}

	if err := r.cache.CompleteAttempt(file, res.BytesIn, res.BytesOut); err != nil {
		logger.Warn("jobs complete attempt", slog.String("err", err.Error()))
	}
	// Invalidate probe for the (possibly replaced) path so the next scan reprobes.
	_ = r.cache.DeleteProbe(file)
	if finalPath != file {
		_ = r.cache.DeleteProbe(finalPath)
	}

	logger.Info("encoded",
		slog.String("action", "encode"),
		slog.String("reason", reason),
		slog.Int64("bytes_in", res.BytesIn),
		slog.Int64("bytes_out", res.BytesOut),
		slog.Duration("duration", encDuration),
		slog.Duration("total", time.Since(attemptStart)),
		slog.Int("attempt", attempt),
		slog.String("final", trimRoot(finalPath, lib.Root)),
	)
	s.encoded++
}

func trimRoot(path, root string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return path
	}
	if strings.HasPrefix(rel, "..") {
		return path
	}
	return rel
}
