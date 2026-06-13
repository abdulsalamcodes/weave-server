package mono

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const baseURL = "https://api.mono.co/v1"

type Client struct {
	secretKey  string
	httpClient *http.Client
}

func NewClient(secretKey string) *Client {
	return &Client{
		secretKey:  secretKey,
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

// --- Connect ---

type ConnectResponse struct {
	Status  string `json:"status"`
	Message string `json:"message"`
	Data    struct {
		ID         string `json:"id"`
		Reference  string `json:"reference"`
		ConnectURL string `json:"connect_url"`
		Monokit    string `json:"monokit"`
	} `json:"data"`
}

func (c *Client) GenerateConnectURL(ctx context.Context, customerID, customerName, customerEmail string) (*ConnectResponse, error) {
	req := map[string]interface{}{
		"customer_id":      customerID,
		"customer_name":    customerName,
		"customer_email":   customerEmail,
		"redirect_url":     "",
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", baseURL+"/account/connect", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("mono-sec-key", c.secretKey)
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("http call: %w", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)

	var result ConnectResponse
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("decode: %w (body: %s)", err, string(raw))
	}

	return &result, nil
}

// --- Sync Account (get account data after connect) ---

type AccountResponse struct {
	Status  string `json:"status"`
	Message string `json:"message"`
	Data    struct {
		ID            string  `json:"id"`
		AccountNumber string  `json:"accountNumber"`
		AccountName   string  `json:"accountName"`
		BankName      string  `json:"bankName"`
		BankCode      string  `json:"bankCode"`
		Balance       float64 `json:"balance"`
		Currency      string  `json:"currency"`
	} `json:"data"`
}

func (c *Client) SyncAccount(ctx context.Context, accountID string) (*AccountResponse, error) {
	httpReq, err := http.NewRequestWithContext(ctx, "POST", baseURL+"/accounts/"+accountID+"/sync", nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("mono-sec-key", c.secretKey)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("http call: %w", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)

	var result AccountResponse
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("decode: %w (body: %s)", err, string(raw))
	}

	return &result, nil
}

// --- Balance ---

type BalanceResponse struct {
	Status  string `json:"status"`
	Message string `json:"message"`
	Data    struct {
		Balance  float64 `json:"balance"`
		Currency string  `json:"currency"`
	} `json:"data"`
}

func (c *Client) GetBalance(ctx context.Context, accountID string) (*BalanceResponse, error) {
	httpReq, err := http.NewRequestWithContext(ctx, "GET", baseURL+"/accounts/"+accountID+"/balance", nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("mono-sec-key", c.secretKey)

	resp, err := c.httpClient.Do(httpReq)
	return parseResponse[BalanceResponse](resp, err)
}

func parseResponse[T any](resp *http.Response, err error) (*T, error) {
	if err != nil {
		return nil, fmt.Errorf("http call: %w", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)

	var result T
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("decode: %w (body: %s)", err, string(raw))
	}

	return &result, nil
}
