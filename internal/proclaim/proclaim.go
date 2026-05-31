package proclaim

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

// Client controls a Proclaim instance via its local HTTP API (port 52195).
type Client struct {
	host     string
	password string
	token    string
	mu       sync.Mutex
	client   *http.Client
}

// NewClient creates a Proclaim API client.
func NewClient(host, password string) *Client {
	return &Client{
		host:     host,
		password: password,
		client:   &http.Client{Timeout: 3 * time.Second},
	}
}

// authenticate obtains an auth token from Proclaim.
func (c *Client) authenticate() error {
	body, _ := json.Marshal(map[string]string{"Password": c.password})
	resp, err := c.client.Post(
		fmt.Sprintf("http://%s:52195/appCommand/authenticate", c.host),
		"application/json",
		bytes.NewReader(body),
	)
	if err != nil {
		return fmt.Errorf("proclaim auth: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("proclaim auth read: %w", err)
	}

	// Strip UTF-8 BOM if present
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))

	var result struct {
		ProclaimAuthToken string `json:"proclaimAuthToken"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return fmt.Errorf("proclaim auth parse: %w", err)
	}
	if result.ProclaimAuthToken == "" {
		return fmt.Errorf("proclaim auth: empty token")
	}

	c.token = result.ProclaimAuthToken
	return nil
}

// Command sends an app command to Proclaim. Auto-authenticates on first call
// and retries once on auth failure.
func (c *Client) Command(name string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	for attempt := 0; attempt < 2; attempt++ {
		if c.token == "" {
			if err := c.authenticate(); err != nil {
				return err
			}
		}

		url := fmt.Sprintf("http://%s:52195/appCommand/perform?appCommandName=%s", c.host, name)
		req, _ := http.NewRequest("GET", url, nil)
		req.Header.Set("ProclaimAuthToken", c.token)

		resp, err := c.client.Do(req)
		if err != nil {
			c.token = "" // force re-auth on next try
			if attempt == 0 {
				continue
			}
			return fmt.Errorf("proclaim command %s: %w", name, err)
		}
		resp.Body.Close()

		if resp.StatusCode == 401 || resp.StatusCode == 403 {
			c.token = "" // token expired, re-auth
			continue
		}
		return nil
	}
	return fmt.Errorf("proclaim command %s: auth failed", name)
}

// NextSlide advances to the next slide.
func (c *Client) NextSlide() error {
	return c.Command("NextSlide")
}

// PreviousSlide goes to the previous slide.
func (c *Client) PreviousSlide() error {
	return c.Command("PreviousSlide")
}
