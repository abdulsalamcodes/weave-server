package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const openAIBaseURL = "https://api.openai.com/v1"

type Client struct {
	apiKey  string
	model   string
	httpClient *http.Client
}

func NewClient(apiKey, model string) *Client {
	if model == "" {
		model = "gpt-4o-mini"
	}
	return &Client{
		apiKey:     apiKey,
		model:      model,
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

// --- Request/Response types ---

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ChatRequest struct {
	Model       string    `json:"model"`
	Messages    []Message `json:"messages"`
	Tools       []Tool    `json:"tools,omitempty"`
	ToolChoice  interface{} `json:"tool_choice,omitempty"` // "auto", "none", or specific tool
	Temperature float64   `json:"temperature,omitempty"`
	MaxTokens   int       `json:"max_tokens,omitempty"`
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
	Role         string        `json:"role"`
	Content      string        `json:"content"`
	ToolCalls    []ToolCall    `json:"tool_calls,omitempty"`
}

type ToolCall struct {
	ID       string            `json:"id"`
	Type     string            `json:"type"`
	Function ToolCallFunction  `json:"function"`
}

type ToolCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"` // JSON string
}

// --- Parsed Intent ---

type Intent string

const (
	IntentSendMoney  Intent = "send_money"
	IntentCheckBal   Intent = "check_balance"
	IntentLinkBank   Intent = "link_bank"
	IntentHelp       Intent = "help"
	IntentUnknown    Intent = "unknown"
)

type ParsedIntent struct {
	Intent           Intent  `json:"intent"`
	Amount           float64 `json:"amount,omitempty"`
	Currency         string  `json:"currency,omitempty"`
	RecipientAccount string  `json:"recipient_account,omitempty"`
	RecipientBank    string  `json:"recipient_bank,omitempty"`
	RecipientName    string  `json:"recipient_name,omitempty"`
	Confidence       float64 `json:"confidence"`
	Raw              string  `json:"raw"`
}

// --- Intent Parser ---

var intentTool = Tool{
	Type: "function",
	Function: ToolFunction{
		Name:        "parse_transfer_intent",
		Description: "Parse a user's natural language request to send money or check balance",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"intent": map[string]interface{}{
					"type": "string",
					"enum": []string{"send_money", "check_balance", "link_bank", "help", "unknown"},
				},
				"amount": map[string]interface{}{
					"type":        "number",
					"description": "Amount to send. Extract as a number. Only for send_money intent.",
				},
				"currency": map[string]interface{}{
					"type":        "string",
					"description": "Currency code (NGN, USD, etc.)",
				},
				"recipient_account": map[string]interface{}{
					"type":        "string",
					"description": "Recipient bank account number. 10 digits for Nigerian banks.",
				},
				"recipient_bank": map[string]interface{}{
					"type":        "string",
					"description": "Recipient bank name or code, if mentioned.",
				},
				"recipient_name": map[string]interface{}{
					"type":        "string",
					"description": "Recipient name if mentioned.",
				},
			},
			"required": []string{"intent"},
		},
	},
}

func (c *Client) ParseIntent(ctx context.Context, message string) (*ParsedIntent, error) {
	req := ChatRequest{
		Model: c.model,
		Messages: []Message{
			{
				Role:    "system",
				Content: "You are a financial assistant for Weave, a Nigerian money transfer app. Parse user requests into structured data. Be precise with amounts and account numbers.",
			},
			{
				Role:    "user",
				Content: message,
			},
		},
		Tools:       []Tool{intentTool},
		ToolChoice:  map[string]interface{}{
			"type": "function",
			"function": map[string]string{"name": "parse_transfer_intent"},
		},
		Temperature: 0.1,
		MaxTokens:   300,
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", openAIBaseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("api call: %w", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("openai error (status %d): %s", resp.StatusCode, string(raw))
	}

	var chatResp ChatResponse
	if err := json.Unmarshal(raw, &chatResp); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}

	if len(chatResp.Choices) == 0 {
		return nil, fmt.Errorf("no choices in response")
	}

	choice := chatResp.Choices[0]
	if len(choice.Message.ToolCalls) == 0 {
		// Fallback: return unknown with raw text
		return &ParsedIntent{
			Intent: IntentUnknown,
			Raw:    message,
		}, nil
	}

	tc := choice.Message.ToolCalls[0]
	if tc.Function.Name != "parse_transfer_intent" {
		return &ParsedIntent{Intent: IntentUnknown, Raw: message}, nil
	}

	var parsed ParsedIntent
	if err := json.Unmarshal([]byte(tc.Function.Arguments), &parsed); err != nil {
		return nil, fmt.Errorf("parse function arguments: %w", err)
	}

	parsed.Raw = message
	parsed.Confidence = 0.9

	return &parsed, nil
}
