package httpclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const DefaultTimeout = 30 * time.Second

func NewClient() *http.Client {
	return &http.Client{Timeout: DefaultTimeout}
}

// Get sends a GET request and unmarshals the JSON response into dst.
func Get(ctx context.Context, client *http.Client, url string, headers map[string]string, dst any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return do(client, req, dst)
}

// Post sends a POST request with a JSON body and unmarshals the response into dst.
func Post(ctx context.Context, client *http.Client, url string, headers map[string]string, body, dst any) error {
	var bodyReader *bytes.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal body: %w", err)
		}
		bodyReader = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bodyReader)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return do(client, req, dst)
}

func do(client *http.Client, req *http.Request, dst any) error {
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("http call: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read body: %w", err)
	}

	if err := json.Unmarshal(raw, dst); err != nil {
		return fmt.Errorf("decode: %w (body: %s)", err, string(raw))
	}

	return nil
}
