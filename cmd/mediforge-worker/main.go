package main

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/3n3a/mediforge/internal/config"
	"github.com/3n3a/mediforge/internal/encode"
	"github.com/3n3a/mediforge/internal/httpapi"
	"github.com/3n3a/mediforge/internal/logx"
)

var version = "dev"

func main() {
	cfg, err := config.LoadWorker()
	if err != nil {
		fmt.Fprintln(os.Stderr, "config:", err)
		os.Exit(2)
	}
	log := logx.New(cfg.LogLevel, cfg.LogFormat)
	slog.SetDefault(log)

	if err := os.MkdirAll(cfg.WorkDir, 0o755); err != nil {
		log.Error("create work dir", slog.String("err", err.Error()))
		os.Exit(2)
	}

	srv := &server{cfg: cfg, log: log}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", srv.handleHealth)
	mux.Handle("POST /encode", httpapi.BearerAuth(cfg.WorkerToken, http.HandlerFunc(srv.handleEncode)))

	httpSrv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		log.Info("worker listening", slog.String("addr", cfg.ListenAddr), slog.String("encoder", cfg.Encoder), slog.String("work_dir", cfg.WorkDir))
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("listen", slog.String("err", err.Error()))
			os.Exit(1)
		}
	}()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()
	log.Info("shutdown requested")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := httpSrv.Shutdown(shutdownCtx); err != nil {
		log.Warn("shutdown", slog.String("err", err.Error()))
	}
}

type server struct {
	cfg  *config.Worker
	log  *slog.Logger
	busy atomic.Bool
	mu   sync.Mutex
}

func (s *server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(httpapi.HealthResponse{
		OK:      true,
		Busy:    s.busy.Load(),
		Version: version,
	})
}

func (s *server) handleEncode(w http.ResponseWriter, r *http.Request) {
	// try-acquire: non-blocking lock via atomic+mutex
	if !s.mu.TryLock() {
		w.Header().Set("Retry-After", "30")
		writeError(w, http.StatusServiceUnavailable, "worker_busy", "worker is currently encoding", "")
		return
	}
	defer s.mu.Unlock()
	s.busy.Store(true)
	defer s.busy.Store(false)

	filename := r.Header.Get(httpapi.HeaderFilename)
	if filename == "" {
		filename = "input"
	}
	filename = filepath.Base(filename) // defense against path traversal
	ext := filepath.Ext(filename)
	if ext == "" {
		ext = ".bin"
	}

	cl := r.ContentLength
	if cl < 0 {
		writeError(w, http.StatusBadRequest, "missing_content_length", "Content-Length header required", "")
		return
	}

	id := randID()
	inPath := filepath.Join(s.cfg.WorkDir, "in-"+id+ext)
	outPath := filepath.Join(s.cfg.WorkDir, "out-"+id+".mp4")
	defer os.Remove(inPath)
	defer os.Remove(outPath)

	logger := s.log.With(slog.String("filename", filename), slog.String("job", id))

	// ---- receive ----
	inFile, err := os.Create(inPath)
	if err != nil {
		logger.Error("create input", slog.String("err", err.Error()))
		writeError(w, http.StatusInternalServerError, "io_error", "create input file failed", err.Error())
		return
	}
	received, err := io.Copy(inFile, r.Body)
	cerr := inFile.Close()
	if err != nil {
		logger.Warn("upload failed", slog.String("err", err.Error()))
		writeError(w, http.StatusBadRequest, "upload_failed", "read body failed", err.Error())
		return
	}
	if cerr != nil {
		logger.Warn("close input", slog.String("err", cerr.Error()))
	}
	if cl > 0 && received != cl {
		writeError(w, http.StatusBadRequest, "short_upload", fmt.Sprintf("expected %d bytes, got %d", cl, received), "")
		return
	}

	// ---- encode ----
	start := time.Now()
	logger.Info("encoding", slog.Int64("bytes_in", received), slog.String("encoder_pref", s.cfg.Encoder))
	res, err := encode.Encode(r.Context(), encode.Options{
		FFmpegBin: s.cfg.FFmpegBin,
		Encoder:   s.cfg.Encoder,
		Input:     inPath,
		Output:    outPath,
	})
	if err != nil {
		logger.Warn("encode failed", slog.String("err", err.Error()), slog.String("encoder", res.EncoderUsed))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(httpapi.ErrorResponse{
			Error:      err.Error(),
			Code:       "ffmpeg_failed",
			FFmpegExit: parseExit(err.Error()),
			StderrTail: res.StderrTail,
		})
		return
	}

	outStat, err := os.Stat(outPath)
	if err != nil {
		logger.Error("stat output", slog.String("err", err.Error()))
		writeError(w, http.StatusInternalServerError, "io_error", "stat output failed", err.Error())
		return
	}

	out, err := os.Open(outPath)
	if err != nil {
		logger.Error("open output", slog.String("err", err.Error()))
		writeError(w, http.StatusInternalServerError, "io_error", "open output failed", err.Error())
		return
	}
	defer out.Close()

	w.Header().Set("Content-Type", httpapi.ContentTypeMP4)
	w.Header().Set("Content-Length", strconv.FormatInt(outStat.Size(), 10))
	w.WriteHeader(http.StatusOK)
	sent, err := io.Copy(w, out)
	duration := time.Since(start)
	if err != nil {
		logger.Warn("stream output", slog.String("err", err.Error()))
		return
	}
	logger.Info("encoded",
		slog.String("encoder", res.EncoderUsed),
		slog.Int64("bytes_in", received),
		slog.Int64("bytes_out", sent),
		slog.Duration("duration", duration),
	)
}

func writeError(w http.ResponseWriter, status int, code, msg, detail string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(httpapi.ErrorResponse{Error: msg, Code: code, Detail: detail})
}

func randID() string {
	var b [10]byte
	_, _ = rand.Read(b[:])
	return strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b[:]))
}

// parseExit pulls the integer exit code out of "ffmpeg exit N: ..." for
// inclusion in the JSON error body. Returns -1 if absent.
func parseExit(msg string) int {
	const prefix = "ffmpeg exit "
	i := strings.Index(msg, prefix)
	if i < 0 {
		return -1
	}
	rest := msg[i+len(prefix):]
	end := strings.IndexAny(rest, ": ")
	if end < 0 {
		end = len(rest)
	}
	n, err := strconv.Atoi(rest[:end])
	if err != nil {
		return -1
	}
	return n
}
