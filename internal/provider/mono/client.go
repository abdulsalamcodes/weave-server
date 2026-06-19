package mono

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const baseURL = "https://api.withmono.com/v2"

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
		MonoURL  string `json:"mono_url"`
		Customer string `json:"customer"` // Mono customer ID
		Meta     struct {
			Ref string `json:"ref"`
		} `json:"meta"`
	} `json:"data"`
}

func (c *Client) GenerateConnectURL(ctx context.Context, customerID, customerName, customerEmail, redirectURL string) (*ConnectResponse, error) {
	req := map[string]interface{}{
		"customer": map[string]string{
			"name":  customerName,
			"email": customerEmail,
		},
		"meta": map[string]string{
			"ref": customerID,
		},
		"scope":        "auth",
		"redirect_url": redirectURL,
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", baseURL+"/accounts/initiate", bytes.NewReader(body))
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

	if resp.StatusCode >= 400 {
		var apiErr struct {
			Message string `json:"message"`
			Error   string `json:"error"`
		}
		_ = json.Unmarshal(raw, &apiErr)
		msg := apiErr.Message
		if msg == "" {
			msg = apiErr.Error
		}
		if msg == "" {
			msg = string(raw)
		}
		return nil, fmt.Errorf("mono API error %d: %s", resp.StatusCode, msg)
	}

	var result ConnectResponse
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("decode: %w (body: %s)", err, string(raw))
	}

	return &result, nil
}

// --- Exchange Code ---

type ExchangeResponse struct {
	Status  string `json:"status"`
	Message string `json:"message"`
	Data    struct {
		ID string `json:"id"`
	} `json:"data"`
}

func (c *Client) ExchangeCode(ctx context.Context, code string) (*ExchangeResponse, error) {
	body, _ := json.Marshal(map[string]string{"code": code})
	httpReq, err := http.NewRequestWithContext(ctx, "POST", baseURL+"/accounts/auth", bytes.NewReader(body))
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
	if resp.StatusCode >= 400 {
		var apiErr struct{ Message string `json:"message"` }
		_ = json.Unmarshal(raw, &apiErr)
		return nil, fmt.Errorf("mono API error %d: %s", resp.StatusCode, apiErr.Message)
	}

	var result ExchangeResponse
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("decode: %w (body: %s)", err, string(raw))
	}
	return &result, nil
}

// --- Customer Accounts ---

type CustomerAccount struct {
	ID            string  `json:"id"`
	BVN           string  `json:"bvn"`
	AccountNumber string  `json:"account_number"`
	AuthMethod    string  `json:"auth_method"`
	Bank          string  `json:"bank"`
	AccountName   string  `json:"account_name"`
	Type          string  `json:"type"`
	Currency      string  `json:"currency"`
	Balance       float64 `json:"balance"`
	Status        string  `json:"status"`
}

type CustomerAccountsResponse struct {
	Status  string            `json:"status"`
	Message string            `json:"message"`
	Data    []CustomerAccount `json:"data"`
}

func (c *Client) GetCustomerAccounts(ctx context.Context, customerID string) (*CustomerAccountsResponse, error) {
	httpReq, err := http.NewRequestWithContext(ctx, "GET", baseURL+"/customers/"+customerID+"/accounts", nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("mono-sec-key", c.secretKey)
	httpReq.Header.Set("accept", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("http call: %w", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		var apiErr struct{ Message string `json:"message"` }
		_ = json.Unmarshal(raw, &apiErr)
		return nil, fmt.Errorf("mono API error %d: %s", resp.StatusCode, apiErr.Message)
	}

	var result CustomerAccountsResponse
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

// VerifyWebhook verifies Mono webhook HMAC-SHA512 signature.
// Mono signs webhooks with HMAC-SHA512 using the secret key.
// The signature is in the "mono-signature" header as a hex-encoded string.
func (c *Client) VerifyWebhook(signature string, body []byte) bool {
	mac := hmac.New(sha512.New, []byte(c.secretKey))
	mac.Write(body)
	expected := hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(signature), []byte(expected))
}

// --- Direct Debit ---

type DirectDebitRequest struct {
	Amount    float64 `json:"amount"`
	Narration string  `json:"narration"`
	Reference string  `json:"reference"`
}

type DirectDebitResponse struct {
	Status  string `json:"status"`
	Message string `json:"message"`
	Data    struct {
		ID              string `json:"id"`
		Amount          int    `json:"amount"`
		Status          string `json:"status"`
		Reference       string `json:"reference"`
		TransactionDate string `json:"transactionDate"`
	} `json:"data"`
}

func (c *Client) DirectDebit(ctx context.Context, accountID string, req DirectDebitRequest) (*DirectDebitResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", baseURL+"/accounts/"+accountID+"/direct-debit", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("mono-sec-key", c.secretKey)
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	return parseResponse[DirectDebitResponse](resp, err)
}
