package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/3n3a/mediforge/internal/httpapi"
)

type Client struct {
	base       string
	token      string
	http       *http.Client
	upload     time.Duration
	encode     time.Duration
	maxBusy    int
	busyBackof time.Duration
}

type EncodeResult struct {
	BytesIn  int64
	BytesOut int64
}

// RetryableError indicates the caller may retry later (e.g. worker busy).
var ErrWorkerBusy = errors.New("worker busy")

func New(baseURL, token string, uploadTimeout, encodeTimeout time.Duration) *Client {
	return &Client{
		base:       baseURL,
		token:      token,
		http:       &http.Client{Timeout: 0},
		upload:     uploadTimeout,
		encode:     encodeTimeout,
		maxBusy:    5,
		busyBackof: 30 * time.Second,
	}
}

// Health pings the worker. No auth required.
func (c *Client) Health(ctx context.Context) (httpapi.HealthResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/health", nil)
	if err != nil {
		return httpapi.HealthResponse{}, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return httpapi.HealthResponse{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return httpapi.HealthResponse{}, fmt.Errorf("health: status %d", resp.StatusCode)
	}
	var h httpapi.HealthResponse
	if err := json.NewDecoder(resp.Body).Decode(&h); err != nil {
		return httpapi.HealthResponse{}, err
	}
	return h, nil
}

// Encode uploads src to the worker, waits for completion, and streams the
// encoded MP4 to destTmp. Returns the bytes in/out on success.
//
// Retries on 503 (busy) up to maxBusy times and on transient network errors.
// A 500 from the worker is returned as a non-retryable error.
func (c *Client) Encode(ctx context.Context, src, destTmp string) (EncodeResult, error) {
	busyAttempts := 0
	netAttempts := 0
	for {
		result, err := c.encodeOnce(ctx, src, destTmp)
		if err == nil {
			return result, nil
		}
		if errors.Is(err, ErrWorkerBusy) {
			if busyAttempts >= c.maxBusy {
				return result, fmt.Errorf("worker busy after %d retries", busyAttempts)
			}
			busyAttempts++
			select {
			case <-ctx.Done():
				return result, ctx.Err()
			case <-time.After(c.busyBackof):
			}
			continue
		}
		if isTransientNet(err) && netAttempts < 2 {
			netAttempts++
			select {
			case <-ctx.Done():
				return result, ctx.Err()
			case <-time.After(5 * time.Second):
			}
			continue
		}
		return result, err
	}
}

func (c *Client) encodeOnce(parent context.Context, src, destTmp string) (EncodeResult, error) {
	f, err := os.Open(src)
	if err != nil {
		return EncodeResult{}, fmt.Errorf("open source: %w", err)
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return EncodeResult{}, fmt.Errorf("stat source: %w", err)
	}

	ctx, cancel := context.WithTimeout(parent, c.encode)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/encode", f)
	if err != nil {
		return EncodeResult{}, err
	}
	req.ContentLength = st.Size()
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", httpapi.ContentTypeBin)
	req.Header.Set(httpapi.HeaderFilename, filepath.Base(src))

	resp, err := c.http.Do(req)
	if err != nil {
		return EncodeResult{}, fmt.Errorf("encode request: %w", err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		// stream body into destTmp
		out, err := os.Create(destTmp)
		if err != nil {
			return EncodeResult{BytesIn: st.Size()}, fmt.Errorf("create temp: %w", err)
		}
		written, copyErr := io.Copy(out, resp.Body)
		if cerr := out.Close(); cerr != nil && copyErr == nil {
			copyErr = cerr
		}
		if copyErr != nil {
			_ = os.Remove(destTmp)
			return EncodeResult{BytesIn: st.Size()}, fmt.Errorf("download encoded: %w", copyErr)
		}
		if cl := resp.Header.Get("Content-Length"); cl != "" {
			if n, err := strconv.ParseInt(cl, 10, 64); err == nil && n != written {
				_ = os.Remove(destTmp)
				return EncodeResult{BytesIn: st.Size()}, fmt.Errorf("short download: got %d, expected %d", written, n)
			}
		}
		return EncodeResult{BytesIn: st.Size(), BytesOut: written}, nil

	case http.StatusServiceUnavailable:
		io.Copy(io.Discard, resp.Body)
		return EncodeResult{BytesIn: st.Size()}, ErrWorkerBusy

	default:
		body, _ := io.ReadAll(resp.Body)
		var er httpapi.ErrorResponse
		_ = json.Unmarshal(body, &er)
		code := er.Code
		if code == "" {
			code = fmt.Sprintf("http_%d", resp.StatusCode)
		}
		msg := er.Error
		if msg == "" {
			msg = string(body)
		}
		return EncodeResult{BytesIn: st.Size()}, &WorkerError{Status: resp.StatusCode, Code: code, Message: msg, Detail: er.Detail, StderrTail: er.StderrTail}
	}
}

type WorkerError struct {
	Status     int
	Code       string
	Message    string
	Detail     string
	StderrTail string
}

func (e *WorkerError) Error() string {
	if e.Detail != "" {
		return fmt.Sprintf("worker %d %s: %s (%s)", e.Status, e.Code, e.Message, e.Detail)
	}
	return fmt.Sprintf("worker %d %s: %s", e.Status, e.Code, e.Message)
}

func isTransientNet(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	msg := err.Error()
	return containsAny(msg, "connection reset", "broken pipe", "connection refused", "EOF", "i/o timeout")
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if indexOf(s, sub) >= 0 {
			return true
		}
	}
	return false
}

func indexOf(s, substr string) int {
	// minimal stdlib-free contains; we stay off strings import churn
	n, m := len(s), len(substr)
	if m == 0 {
		return 0
	}
	for i := 0; i+m <= n; i++ {
		if s[i:i+m] == substr {
			return i
		}
	}
	return -1
}
