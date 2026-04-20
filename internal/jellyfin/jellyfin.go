package jellyfin

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type Client struct {
	base   string
	apiKey string
	http   *http.Client
}

func New(baseURL, apiKey string) *Client {
	return &Client{
		base:   strings.TrimRight(baseURL, "/"),
		apiKey: apiKey,
		http:   &http.Client{Timeout: 15 * time.Second},
	}
}

// Refresh triggers a full library refresh on the Jellyfin server. This is
// the coarse-grained endpoint; Jellyfin scans all libraries. It's the safest
// call since we don't know Jellyfin's internal library IDs from the master.
func (c *Client) Refresh(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/Library/Refresh", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", fmt.Sprintf(`MediaBrowser Token="%s"`, c.apiKey))
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("jellyfin refresh: status %d", resp.StatusCode)
	}
	return nil
}
