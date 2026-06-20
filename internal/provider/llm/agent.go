package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// AgentMessage is the richer message type used inside the agent loop.
// It supports tool_calls (assistant) and tool results (tool role),
// which the simpler Message type cannot express.
type AgentMessage struct {
	Role       string     `json:"role"`
	Content    *string    `json:"content"`            // pointer so we can send null for tool-calling turns
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"` // set on role:"tool" messages
}

type agentChatRequest struct {
	Model       string         `json:"model"`
	Messages    []AgentMessage `json:"messages"`
	Tools       []Tool         `json:"tools,omitempty"`
	Temperature float64        `json:"temperature,omitempty"`
	MaxTokens   int            `json:"max_tokens,omitempty"`
}

// RunAgent runs the ReAct agent loop until the LLM produces a final text
// response or the iteration cap is reached.
//
// execute is called for each tool the LLM invokes. It receives the tool name
// and raw JSON arguments, and returns a result that gets JSON-encoded and sent
// back to the LLM as a tool result message. Returning an error sends the error
// message as the tool result so the LLM can recover gracefully.
func (c *Client) RunAgent(
	ctx context.Context,
	systemPrompt string,
	history []Message,
	userMessage string,
	tools []Tool,
	execute func(ctx context.Context, name, argsJSON string) (interface{}, error),
) (string, error) {
	const maxIterations = 8

	// Seed the thread: system → history → new user turn.
	msgs := make([]AgentMessage, 0, len(history)+3)
	msgs = append(msgs, textMsg("system", systemPrompt))
	for _, h := range history {
		c := h.Content
		msgs = append(msgs, AgentMessage{Role: h.Role, Content: &c})
	}
	msgs = append(msgs, textMsg("user", userMessage))

	for i := 0; i < maxIterations; i++ {
		resp, err := c.agentCall(ctx, msgs, tools)
		if err != nil {
			return "", fmt.Errorf("agent iteration %d: %w", i, err)
		}

		if len(resp.Choices) == 0 {
			return "", fmt.Errorf("empty choices at iteration %d", i)
		}
		choice := resp.Choices[0]

		// Append the assistant's turn to the thread.
		assistantMsg := AgentMessage{
			Role:      "assistant",
			Content:   &choice.Message.Content,
			ToolCalls: choice.Message.ToolCalls,
		}
		msgs = append(msgs, assistantMsg)

		// No tool calls — the LLM is done.
		if len(choice.Message.ToolCalls) == 0 {
			return choice.Message.Content, nil
		}

		// Execute every tool call the LLM requested, in order.
		for _, tc := range choice.Message.ToolCalls {
			result, execErr := execute(ctx, tc.Function.Name, tc.Function.Arguments)

			var toolContent string
			if execErr != nil {
				errPayload, _ := json.Marshal(map[string]string{"error": execErr.Error()})
				toolContent = string(errPayload)
			} else {
				encoded, _ := json.Marshal(result)
				toolContent = string(encoded)
			}

			msgs = append(msgs, AgentMessage{
				Role:       "tool",
				Content:    &toolContent,
				ToolCallID: tc.ID,
			})
		}
	}

	return "I wasn't able to complete your request after several attempts. Please try again.", nil
}

// agentCall sends the messages and tools to the LLM and returns the raw response.
func (c *Client) agentCall(ctx context.Context, msgs []AgentMessage, tools []Tool) (*ChatResponse, error) {
	req := agentChatRequest{
		Model:       c.model,
		Messages:    msgs,
		Tools:       tools,
		Temperature: 0.2,
		MaxTokens:   600,
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("http: %w", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("llm error (status %d): %s", resp.StatusCode, string(raw))
	}

	var chatResp ChatResponse
	if err := json.Unmarshal(raw, &chatResp); err != nil {
		return nil, fmt.Errorf("unmarshal: %w", err)
	}
	return &chatResp, nil
}

func textMsg(role, content string) AgentMessage {
	c := content
	return AgentMessage{Role: role, Content: &c}
}
