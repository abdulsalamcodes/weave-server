package handler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/abdulsalamcodes/weave-server/internal/model"
	"github.com/abdulsalamcodes/weave-server/internal/provider/llm"
	"github.com/abdulsalamcodes/weave-server/internal/provider/paystack"
)

// --- Agent system prompt ---

// buildAgentPrompt constructs the system prompt injected at the start of every
// agent loop. It carries the always-resident L1 user state (wallet, banks,
// pending action, virtual account) so the agent never has to call a tool just
// to know the user's current situation.
func (h *ChatHandler) buildAgentPrompt(ctx context.Context, userID uuid.UUID) string {
	var (
		wg      sync.WaitGroup
		wallet  struct{ avail, total float64; ok bool }
		banks   []model.BankAccount
		acct    struct{ number, bank, name string; ok bool }
	)

	wg.Add(3)
	go func() {
		defer wg.Done()
		if w, err := h.walletService.GetBalance(ctx, userID); err == nil {
			wallet = struct{ avail, total float64; ok bool }{w.LedgerBalance.NGN(), w.Balance.NGN(), true}
		}
	}()
	go func() {
		defer wg.Done()
		b, _ := h.bankRepo.GetByUserID(ctx, userID, 10, 0)
		banks = b
	}()
	go func() {
		defer wg.Done()
		if a, err := h.walletService.GetWalletAccount(ctx, userID); err == nil && a != nil {
			acct = struct{ number, bank, name string; ok bool }{a.AccountNumber, a.BankName, a.AccountName, true}
		}
	}()
	wg.Wait()

	var state strings.Builder

	if wallet.ok {
		state.WriteString(fmt.Sprintf("WALLET: ₦%.2f available (₦%.2f total)\n", wallet.avail, wallet.total))
	}

	if len(banks) > 0 {
		state.WriteString(fmt.Sprintf("LINKED BANKS (%d):\n", len(banks)))
		for _, b := range banks {
			status := "active+verified"
			if !b.IsActive {
				status = "inactive"
			} else if !b.IsVerified {
				status = "active,not-verified"
			}
			state.WriteString(fmt.Sprintf("  • %s [%s] ₦%s priority=%d status=%s\n",
				b.BankName, b.AccountNumber, formatAmount(b.LastBalance), b.Priority, status))
		}
	} else {
		state.WriteString("LINKED BANKS: none\n")
	}

	if action, ok := h.loadPending(ctx, userID); ok {
		switch action.Kind {
		case kindTransfer:
			t := action.Transfer
			state.WriteString(fmt.Sprintf(
				"\nPENDING TRANSFER (awaiting user confirmation):\n"+
					"  Amount: ₦%.2f  To: %s  Bank: %s  Name: %s\n",
				float64(t.Amount)/100, t.RecipientAccount, t.RecipientBank, t.RecipientName,
			))
		case kindUnlink:
			u := action.Unlink
			state.WriteString(fmt.Sprintf(
				"\nPENDING UNLINK (awaiting user confirmation): %s (%s)\n",
				u.BankName, u.AccountNumber,
			))
		}
	}

	if acct.ok {
		state.WriteString(fmt.Sprintf("VIRTUAL ACCOUNT: %s at %s (name: %s)\n", acct.number, acct.bank, acct.name))
	} else {
		state.WriteString("VIRTUAL ACCOUNT: not yet created\n")
	}

	return `You are Weave, an intelligent Nigerian banking assistant. You help users manage their money through natural conversation.

TOOLS: Use the provided tools to fetch real data and perform actions. Never fabricate balances, transfer references, or account names.

IMPORTANT: CURRENT USER STATE (at the bottom of this prompt) reflects the live database right now. Always trust it over anything said earlier in the conversation. If a bank shows status=active+verified, it IS usable — do not contradict this based on past errors in chat history.

MONEY MOVEMENT RULES — follow these strictly:
1. When a user wants to send money, call initiate_transfer to build the plan.
2. Present the plan clearly and ask for explicit confirmation before proceeding.
3. Only call confirm_transfer after the user says yes, proceed, confirm, or similar.
4. If the user says no, cancel, or abort — call cancel_transfer.
5. For unlink requests, call initiate_unlink first, then confirm_unlink after user confirmation.

MULTI-STEP REASONING: You can call multiple tools in sequence. For example:
- "send to the same person I sent to last week" → call get_transfer_history first, extract the recipient, then initiate_transfer.
- "do I have enough to send 10000?" → call get_wallet_balance and get_linked_banks, reason about the total, then answer.
- "look up 0123456789 at GTBank then send them 5000" → call lookup_account first, then initiate_transfer with the resolved name.

FORMATTING:
- Amounts: ₦5,000.00 format with comma separators.
- Always include the transfer reference (WVF-xxx) when a transfer completes.
- Keep responses concise — this is a mobile chat UI.
- Use emoji sparingly but meaningfully (✅ for success, ❌ for failure, ⚠️ for warnings).

CURRENT USER STATE:
` + state.String()
}

// --- Pending action store ---

func (h *ChatHandler) storePending(ctx context.Context, userID uuid.UUID, action pendingAction) {
	if h.rdb == nil {
		return
	}
	data, _ := json.Marshal(action)
	h.rdb.Set(ctx, pendingKey(userID), data, 10*time.Minute)
}

func (h *ChatHandler) loadPending(ctx context.Context, userID uuid.UUID) (pendingAction, bool) {
	if h.rdb == nil {
		return pendingAction{}, false
	}
	raw, err := h.rdb.Get(ctx, pendingKey(userID)).Bytes()
	if err != nil {
		return pendingAction{}, false
	}
	var action pendingAction
	if err := json.Unmarshal(raw, &action); err != nil {
		return pendingAction{}, false
	}
	return action, true
}

func (h *ChatHandler) clearPending(ctx context.Context, userID uuid.UUID) {
	if h.rdb != nil {
		h.rdb.Del(ctx, pendingKey(userID))
	}
}

// --- Conversation history ---

func (h *ChatHandler) loadHistory(ctx context.Context, userID uuid.UUID) []llm.Message {
	// Try Redis first (warm cache).
	if h.rdb != nil {
		raw, err := h.rdb.Get(ctx, historyKey(userID)).Bytes()
		if err == nil {
			var msgs []llm.Message
			if json.Unmarshal(raw, &msgs) == nil && len(msgs) > 0 {
				return msgs
			}
		}
	}

	// Redis miss — rebuild from DB.
	if h.chatRepo == nil {
		return nil
	}
	rows, err := h.chatRepo.RecentAsLLMMessages(ctx, userID, maxHistoryMessages)
	if err != nil || len(rows) == 0 {
		return nil
	}
	msgs := make([]llm.Message, 0, len(rows))
	for _, r := range rows {
		msgs = append(msgs, llm.Message{Role: r.Role, Content: r.Content})
	}
	// Re-warm Redis so next request hits cache.
	h.saveHistory(ctx, userID, msgs)
	return msgs
}

func (h *ChatHandler) saveHistory(ctx context.Context, userID uuid.UUID, msgs []llm.Message) {
	if h.rdb == nil {
		return
	}
	if len(msgs) > maxHistoryMessages {
		msgs = msgs[len(msgs)-maxHistoryMessages:]
	}
	data, _ := json.Marshal(msgs)
	h.rdb.Set(ctx, historyKey(userID), data, 2*time.Hour)
}

// --- Bank lookup helpers ---

func (h *ChatHandler) findBankByIdentifier(ctx context.Context, userID uuid.UUID, identifier string) *model.BankAccount {
	banks, err := h.bankRepo.GetByUserID(ctx, userID, 20, 0)
	if err != nil {
		return nil
	}
	lower := strings.ToLower(identifier)
	for i, b := range banks {
		if strings.Contains(strings.ToLower(b.BankName), lower) ||
			strings.Contains(b.AccountNumber, identifier) {
			return &banks[i]
		}
	}
	return nil
}

func resolveBankCode(banks []paystack.Bank, name string) string {
	lower := strings.ToLower(name)
	for _, b := range banks {
		if strings.Contains(strings.ToLower(b.Name), lower) ||
			strings.Contains(strings.ToLower(b.Slug), lower) {
			return b.Code
		}
	}
	return ""
}

// --- Formatting helpers ---

func formatAmount(a model.Amount) string {
	return fmt.Sprintf("%.2f", a.NGN())
}

func hashMessage(msg string) string {
	h := sha256.Sum256([]byte(msg))
	return hex.EncodeToString(h[:16])
}

// normalizeToMap round-trips v through JSON to produce a plain
// map[string]interface{} suitable for generic formatters.
func normalizeToMap(v interface{}) map[string]interface{} {
	b, _ := json.Marshal(v)
	var m map[string]interface{}
	_ = json.Unmarshal(b, &m)
	return m
}
