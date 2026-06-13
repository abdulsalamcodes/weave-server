package paystack

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

const (
	baseURL = "https://api.paystack.co"
)

type Client struct {
	secretKey  string
	publicKey  string
	httpClient *http.Client
}

func NewClient(secretKey, publicKey string) *Client {
	return &Client{
		secretKey:  secretKey,
		publicKey:  publicKey,
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

// --- Customer ---

type CreateCustomerRequest struct {
	Email string  `json:"email"`
	Phone string  `json:"phone"`
	Name  string  `json:"first_name"`
}

type CreateCustomerResponse struct {
	Status  bool   `json:"status"`
	Message string `json:"message"`
	Data    struct {
		ID           int    `json:"id"`
		CustomerCode string `json:"customer_code"`
		Email        string `json:"email"`
	} `json:"data"`
}

func (c *Client) CreateCustomer(ctx context.Context, req *CreateCustomerRequest) (*CreateCustomerResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", baseURL+"/customer", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+c.secretKey)
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("http call: %w", err)
	}
	defer resp.Body.Close()

	var result CreateCustomerResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	if !result.Status {
		return nil, fmt.Errorf("paystack error: %s", result.Message)
	}

	return &result, nil
}

// --- Dedicated Virtual Account ---

type AssignDVAResponse struct {
	Status  bool   `json:"status"`
	Message string `json:"message"`
	Data    struct {
		AccountNumber string `json:"account_number"`
		AccountName   string `json:"account_name"`
		Bank          struct {
			Name string `json:"name"`
			Slug string `json:"slug"`
		} `json:"bank"`
		Provider string `json:"provider"`
	} `json:"data"`
}

func (c *Client) AssignDVA(ctx context.Context, customerCode string, preferredBank string) (*AssignDVAResponse, error) {
	req := map[string]interface{}{
		"customer":       customerCode,
		"preferred_bank": preferredBank,
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", baseURL+"/dedicated_account", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+c.secretKey)
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("http call: %w", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)

	var result AssignDVAResponse
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("decode response: %w (body: %s)", err, string(raw))
	}

	if !result.Status {
		return nil, fmt.Errorf("paystack error: %s (body: %s)", result.Message, string(raw))
	}

	return &result, nil
}

// --- Transfer ---

type TransferRequest struct {
	Amount    int    `json:"amount"`    // kobo
	Recipient string `json:"recipient"` // transfer recipient code
	Reference string `json:"reference"`
	Reason    string `json:"reason,omitempty"`
}

type TransferResponse struct {
	Status  bool   `json:"status"`
	Message string `json:"message"`
	Data    struct {
		ID          int    `json:"id"`
		Reference   string `json:"reference"`
		Amount      int    `json:"amount"`
		Status      string `json:"status"`
		RecipientID int    `json:"recipient"`
	} `json:"data"`
}

func (c *Client) InitiateTransfer(ctx context.Context, req *TransferRequest) (*TransferResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", baseURL+"/transfer", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+c.secretKey)
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("http call: %w", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)

	var result TransferResponse
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("decode response: %w (body: %s)", err, string(raw))
	}

	if !result.Status {
		return nil, fmt.Errorf("paystack error: %s (body: %s)", result.Message, string(raw))
	}

	return &result, nil
}

// --- Transfer Recipient ---

type CreateRecipientRequest struct {
	Type           string `json:"type"`
	Name           string `json:"name"`
	AccountNumber  string `json:"account_number"`
	BankCode       string `json:"bank_code"`
	Currency       string `json:"currency"`
}

type CreateRecipientResponse struct {
	Status  bool   `json:"status"`
	Message string `json:"message"`
	Data    struct {
		RecipientCode string `json:"recipient_code"`
		Active        bool   `json:"active"`
	} `json:"data"`
}

func (c *Client) CreateTransferRecipient(ctx context.Context, req *CreateRecipientRequest) (*CreateRecipientResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", baseURL+"/transferrecipient", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+c.secretKey)
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("http call: %w", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)

	var result CreateRecipientResponse
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("decode response: %w (body: %s)", err, string(raw))
	}

	if !result.Status {
		return nil, fmt.Errorf("paystack error: %s (body: %s)", result.Message, string(raw))
	}

	return &result, nil
}

// --- Webhook Verification ---

func (c *Client) VerifyWebhook(signature string, body []byte) bool {
	mac := hmac.New(sha256.New, []byte(c.secretKey))
	mac.Write(body)
	expected := hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(signature), []byte(expected))
}

// --- Balance ---

type BalanceResponse struct {
	Status  bool   `json:"status"`
	Message string `json:"message"`
	Data    []struct {
		Currency string `json:"currency"`
		Balance  int    `json:"balance"`
	} `json:"data"`
}

func (c *Client) GetBalance(ctx context.Context) (*BalanceResponse, error) {
	httpReq, err := http.NewRequestWithContext(ctx, "GET", baseURL+"/balance", nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+c.secretKey)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("http call: %w", err)
	}
	defer resp.Body.Close()

	var result BalanceResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return &result, nil
}

// --- Bank List ---

type Bank struct {
	Name      string `json:"name"`
	Slug      string `json:"slug"`
	Code      string `json:"code"`
	LongCode  string `json:"longcode"`
	Active    bool   `json:"active"`
}

func (c *Client) ListBanks(ctx context.Context) ([]Bank, error) {
	httpReq, err := http.NewRequestWithContext(ctx, "GET", baseURL+"/bank?country=nigeria", nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+c.secretKey)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("http call: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		Status  bool   `json:"status"`
		Message string `json:"message"`
		Data    []Bank `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return result.Data, nil
}

// --- Resolve Account ---

type ResolveAccountResponse struct {
	Status  bool   `json:"status"`
	Message string `json:"message"`
	Data    struct {
		AccountNumber string `json:"account_number"`
		AccountName   string `json:"account_name"`
		BankID        int    `json:"bank_id"`
	} `json:"data"`
}

func (c *Client) ResolveAccount(ctx context.Context, accountNumber, bankCode string) (*ResolveAccountResponse, error) {
	url := fmt.Sprintf("%s/bank/resolve?account_number=%s&bank_code=%s", baseURL, accountNumber, bankCode)
	httpReq, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+c.secretKey)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("http call: %w", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)

	var result ResolveAccountResponse
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("decode response: %w (body: %s)", err, string(raw))
	}

	if !result.Status {
		return nil, fmt.Errorf("paystack resolve error: %s (body: %s)", result.Message, string(raw))
	}

	return &result, nil
}
