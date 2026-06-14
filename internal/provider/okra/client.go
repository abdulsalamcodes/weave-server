package okra

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const baseURL = "https://api.okra.ng/v2"

type Client struct {
	clientID   string
	secret     string
	httpClient *http.Client
}

func NewClient(clientID, secret string) *Client {
	return &Client{
		clientID:   clientID,
		secret:     secret,
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

// --- Connect Widget ---

type ConnectRequest struct {
	CustomerID string `json:"customer_id,omitempty"`
	FirstName  string `json:"first_name,omitempty"`
	LastName   string `json:"last_name,omitempty"`
	Phone      string `json:"phone,omitempty"`
	CallbackURL string `json:"callback_url,omitempty"`
}

type ConnectResponse struct {
	Status  string `json:"status"`
	Message string `json:"message"`
	Data    struct {
		ConnectURL string `json:"connect_url"`
		Reference  string `json:"reference"`
		Token      string `json:"token"`
	} `json:"data"`
}

func (c *Client) GenerateConnectURL(ctx context.Context, req *ConnectRequest) (*ConnectResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", baseURL+"/accounts/connect", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+c.secret)
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

// --- Balance ---

type BalanceResponse struct {
	Status  string `json:"status"`
	Message string `json:"message"`
	Data    struct {
		Balance      float64 `json:"balance"`
		AccountName  string  `json:"account_name"`
		AccountNumber string `json:"account_number"`
		BankName     string  `json:"bank_name"`
		BankCode     string  `json:"bank_code"`
	} `json:"data"`
}

func (c *Client) GetBalance(ctx context.Context, accountID string) (*BalanceResponse, error) {
	url := fmt.Sprintf("%s/accounts/%s/balance", baseURL, accountID)
	httpReq, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+c.secret)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("http call: %w", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)

	var result BalanceResponse
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("decode: %w (body: %s)", err, string(raw))
	}

	return &result, nil
}

// --- Initiate Payment ---

type PaymentRequest struct {
	Amount          float64 `json:"amount"`
	AccountID       string  `json:"account_id"`
	RecipientAccount string `json:"recipient_account"`
	RecipientBank   string  `json:"recipient_bank"`
	Narration       string  `json:"narration,omitempty"`
	Reference       string  `json:"reference"`
}

type PaymentResponse struct {
	Status  string `json:"status"`
	Message string `json:"message"`
	Data    struct {
		Reference string `json:"reference"`
		Status    string `json:"status"`
	} `json:"data"`
}

func (c *Client) InitiatePayment(ctx context.Context, req *PaymentRequest) (*PaymentResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", baseURL+"/payments/initiate", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+c.secret)
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("http call: %w", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)

	var result PaymentResponse
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("decode: %w (body: %s)", err, string(raw))
	}

	return &result, nil
}

// --- Webhook ---

type WebhookPayload struct {
	Event   string `json:"event"`
	Data    json.RawMessage `json:"data"`
	Account struct {
		ID            string `json:"id"`
		AccountNumber string `json:"account_number"`
		AccountName   string `json:"account_name"`
		BankName      string `json:"bank_name"`
		BankCode      string `json:"bank_code"`
		Balance       float64 `json:"balance"`
		AccessToken   string  `json:"access_token"`
	} `json:"account,omitempty"`
	Reference string `json:"reference"`
}

var supportedEvents = map[string]bool{
	"account.connected":  true,
	"account.updated":    true,
	"payment.success":    true,
	"payment.failed":     true,
}

func IsSupportedEvent(event string) bool {
	return supportedEvents[event]
}

// VerifyWebhook verifies Okra webhook HMAC-SHA256 signature.
// Okra signs webhooks with HMAC-SHA256 using the client secret.
// The signature is in the "X-Okra-Signature" header as a hex-encoded string.
func (c *Client) VerifyWebhook(signature string, body []byte) bool {
	mac := hmac.New(sha256.New, []byte(c.secret))
	mac.Write(body)
	expected := hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(signature), []byte(expected))
}
