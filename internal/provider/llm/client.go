package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type Client struct {
	apiKey     string
	model      string
	baseURL    string
	httpClient *http.Client
}

func NewClient(apiKey, model, baseURL string) *Client {
	if model == "" {
		model = "llama3.2"
	}
	if baseURL == "" {
		baseURL = "http://localhost:11434/v1"
	}
	return &Client{
		apiKey:     apiKey,
		model:      model,
		baseURL:    strings.TrimRight(baseURL, "/"),
		httpClient: &http.Client{Timeout: 60 * time.Second},
	}
}

// --- Request/Response types ---

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ChatRequest struct {
	Model       string      `json:"model"`
	Messages    []Message   `json:"messages"`
	Tools       []Tool      `json:"tools,omitempty"`
	ToolChoice  interface{} `json:"tool_choice,omitempty"`
	Temperature float64     `json:"temperature,omitempty"`
	MaxTokens   int         `json:"max_tokens,omitempty"`
}

type Tool struct {
	Type     string       `json:"type"`
	Function ToolFunction `json:"function"`
}

type ToolFunction struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	Parameters  interface{} `json:"parameters"`
}

type ChatResponse struct {
	Choices []Choice `json:"choices"`
}

type Choice struct {
	Message      ResponseMessage `json:"message"`
	FinishReason string          `json:"finish_reason"`
}

type ResponseMessage struct {
	Role      string     `json:"role"`
	Content   string     `json:"content"`
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
}

type ToolCall struct {
	ID       string           `json:"id"`
	Type     string           `json:"type"`
	Function ToolCallFunction `json:"function"`
}

type ToolCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// --- Parsed Intent ---

type Intent string

const (
	IntentSendMoney      Intent = "send_money"
	IntentConfirmTx      Intent = "confirm_transfer"
	IntentCancelTx       Intent = "cancel_transfer"
	IntentCheckBal       Intent = "check_balance"
	IntentLinkBank       Intent = "link_bank"
	IntentListBanks      Intent = "list_banks"
	IntentUnlinkBank     Intent = "unlink_bank"
	IntentSetPriority    Intent = "set_priority"
	IntentRefreshBalance Intent = "refresh_balance"
	IntentFundWallet     Intent = "fund_wallet"
	IntentWalletHistory  Intent = "wallet_history"
	IntentTxHistory      Intent = "transfer_history"
	IntentTxStatus       Intent = "transfer_status"
	IntentLookupAccount  Intent = "lookup_account"
	IntentHelp           Intent = "help"
	IntentUnknown        Intent = "unknown"
)

type ParsedIntent struct {
	Intent           Intent  `json:"intent"`
	Amount           float64 `json:"amount,omitempty"`
	Currency         string  `json:"currency,omitempty"`
	RecipientAccount string  `json:"recipient_account,omitempty"`
	RecipientBank    string  `json:"recipient_bank,omitempty"`
	RecipientName    string  `json:"recipient_name,omitempty"`
	Reference        string  `json:"reference,omitempty"`        // transfer ref for status/retry
	BankIdentifier   string  `json:"bank_identifier,omitempty"`  // bank name/code for unlink/priority/refresh
	Priority         int     `json:"priority,omitempty"`         // 1-5 for set_priority
	Confidence       float64 `json:"confidence"`
	Raw              string  `json:"raw"`
}

// --- Tool definition ---

var intentTool = Tool{
	Type: "function",
	Function: ToolFunction{
		Name:        "parse_transfer_intent",
		Description: "Parse a user's natural language request about money transfers, balance, bank accounts, or wallet funding",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"intent": map[string]interface{}{
					"type": "string",
					"enum": []string{
						"send_money", "confirm_transfer", "cancel_transfer",
						"check_balance", "transfer_history", "transfer_status",
						"link_bank", "list_banks", "unlink_bank", "set_priority", "refresh_balance",
						"fund_wallet", "wallet_history",
						"lookup_account", "help", "unknown",
					},
				},
				"amount": map[string]interface{}{
					"type":        "number",
					"description": "Amount in naira. Only for send_money.",
				},
				"currency": map[string]interface{}{
					"type":        "string",
					"description": "Currency code, default NGN.",
				},
				"recipient_account": map[string]interface{}{
					"type":        "string",
					"description": "10-digit Nigerian bank account number.",
				},
				"recipient_bank": map[string]interface{}{
					"type":        "string",
					"description": "Recipient bank name or code.",
				},
				"recipient_name": map[string]interface{}{
					"type":        "string",
					"description": "Recipient name if mentioned.",
				},
				"reference": map[string]interface{}{
					"type":        "string",
					"description": "Transfer reference or ID mentioned by user (e.g. WVF-xxx) for status/retry.",
				},
				"bank_identifier": map[string]interface{}{
					"type":        "string",
					"description": "Bank name or partial account number to identify which linked bank for unlink/priority/refresh.",
				},
				"priority": map[string]interface{}{
					"type":        "number",
					"description": "Priority level 1-5 (1=highest) for set_priority intent.",
				},
			},
			"required": []string{"intent"},
		},
	},
}

// ParseIntentWithContext parses intent using conversation history and optional state context.
func (c *Client) ParseIntentWithContext(ctx context.Context, history []Message, systemContext string) (*ParsedIntent, error) {
	system := `You are a financial assistant for Weave, a Nigerian money transfer app.

Your job is to parse what the user wants to do based on the FULL conversation — not just the latest message.
Use prior messages to understand context. For example:
- If the bot just showed a transfer plan and the user says "yes", "ok", "proceed", "go ahead", "do it", "sure", "yep", "alright", "confirm" — that is confirm_transfer.
- If the user says "no", "nope", "cancel", "stop", "don't", "abort", "nevermind" — that is cancel_transfer.
- Use the conversation to fill in missing fields. If the user said "send 5000 to 0123456789" and then just says "GTBank" — they're providing the bank for the same transfer.

Always extract numbers and account numbers precisely. Nigerian account numbers are exactly 10 digits.`

	if systemContext != "" {
		system += "\n\nCurrent state:\n" + systemContext
	}

	messages := append([]Message{{Role: "system", Content: system}}, history...)

	req := ChatRequest{
		Model:    c.model,
		Messages: messages,
		Tools:    []Tool{intentTool},
		ToolChoice: map[string]interface{}{
			"type":     "function",
			"function": map[string]string{"name": "parse_transfer_intent"},
		},
		Temperature: 0.1,
		MaxTokens:   300,
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("api call: %w", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("llm error (status %d): %s", resp.StatusCode, string(raw))
	}

	var chatResp ChatResponse
	if err := json.Unmarshal(raw, &chatResp); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}

	if len(chatResp.Choices) == 0 {
		return nil, fmt.Errorf("no choices in response")
	}

	choice := chatResp.Choices[0]
	rawMsg := strings.TrimSpace(choice.Message.Content)

	if len(choice.Message.ToolCalls) == 0 {
		return &ParsedIntent{Intent: IntentUnknown, Raw: rawMsg}, nil
	}

	tc := choice.Message.ToolCalls[0]
	if tc.Function.Name != "parse_transfer_intent" {
		return &ParsedIntent{Intent: IntentUnknown, Raw: rawMsg}, nil
	}

	var parsed ParsedIntent
	if err := json.Unmarshal([]byte(tc.Function.Arguments), &parsed); err != nil {
		return nil, fmt.Errorf("parse function arguments: %w", err)
	}

	if parsed.Raw == "" {
		parsed.Raw = rawMsg
	}
	if parsed.Confidence == 0 {
		parsed.Confidence = 0.9
	}
	if parsed.Currency == "" && parsed.Intent == IntentSendMoney {
		parsed.Currency = "NGN"
	}

	return &parsed, nil
}

// ParseIntent is a convenience wrapper with no history (single-turn).
func (c *Client) ParseIntent(ctx context.Context, message string) (*ParsedIntent, error) {
	return c.ParseIntentWithContext(ctx, []Message{{Role: "user", Content: message}}, "")
}
