package handler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/abdulsalamcodes/weave-server/internal/model"
	"github.com/abdulsalamcodes/weave-server/internal/provider/llm"
	"github.com/abdulsalamcodes/weave-server/internal/provider/paystack"
)

// --- Keyword shortcut ---

// keywordIntent maps unambiguous single-word/phrase inputs directly to an intent,
// bypassing the LLM entirely. Returns nil to signal "fall through to LLM".
func keywordIntent(msg string) *llm.ParsedIntent {
	var intent llm.Intent
	switch strings.ToLower(strings.TrimSpace(msg)) {
	case "help", "?", "commands", "what can you do", "what can i do here":
		intent = llm.IntentHelp
	case "balance", "my balance", "check balance", "wallet", "wallet balance":
		intent = llm.IntentCheckBal
	case "banks", "my banks", "linked banks", "accounts", "my accounts":
		intent = llm.IntentListBanks
	case "history", "transfers", "recent transfers", "my transfers":
		intent = llm.IntentTxHistory
	case "yes", "confirm", "ok", "okay", "proceed", "go ahead", "do it", "sure", "yep", "yh", "yeah", "alright":
		intent = llm.IntentConfirmTx
	case "no", "cancel", "stop", "abort", "nevermind", "nope", "don't":
		intent = llm.IntentCancelTx
	default:
		return nil
	}
	return &llm.ParsedIntent{Intent: intent, Raw: msg, Confidence: 1.0}
}

// --- Formatting helpers ---

func formatAmount(a model.Amount) string {
	return fmt.Sprintf("%.2f", a.NGN())
}

func hashMessage(msg string) string {
	h := sha256.Sum256([]byte(msg))
	return hex.EncodeToString(h[:16])
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
	if h.rdb == nil {
		return nil
	}
	raw, err := h.rdb.Get(ctx, historyKey(userID)).Bytes()
	if err != nil {
		return nil
	}
	var msgs []llm.Message
	_ = json.Unmarshal(raw, &msgs)
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

// --- L1 system context ---

// buildSystemContext assembles always-resident user state injected into every LLM call.
// All three data sources are fetched in parallel to minimise latency.
func (h *ChatHandler) buildSystemContext(ctx context.Context, userID uuid.UUID) string {
	type walletRes struct {
		balance       model.Amount
		ledgerBalance model.Amount
		ok            bool
	}
	type banksRes struct {
		banks []model.BankAccount
	}
	type acctRes struct {
		number  string
		bank    string
		name    string
		ok      bool
	}

	var (
		wg      sync.WaitGroup
		walletR walletRes
		banksR  banksRes
		acctR   acctRes
	)

	wg.Add(3)

	go func() {
		defer wg.Done()
		if w, err := h.walletService.GetBalance(ctx, userID); err == nil {
			walletR = walletRes{balance: w.Balance, ledgerBalance: w.LedgerBalance, ok: true}
		}
	}()

	go func() {
		defer wg.Done()
		if b, err := h.bankRepo.GetByUserID(ctx, userID, 10, 0); err == nil {
			banksR = banksRes{banks: b}
		}
	}()

	go func() {
		defer wg.Done()
		if a, err := h.walletService.GetWalletAccount(ctx, userID); err == nil && a != nil {
			acctR = acctRes{number: a.AccountNumber, bank: a.BankName, name: a.AccountName, ok: true}
		}
	}()

	wg.Wait()

	var sb strings.Builder

	if walletR.ok {
		sb.WriteString(fmt.Sprintf("WALLET: ₦%s available (₦%s total)\n",
			formatAmount(walletR.ledgerBalance), formatAmount(walletR.balance)))
	}

	if len(banksR.banks) > 0 {
		sb.WriteString(fmt.Sprintf("LINKED BANKS (%d):\n", len(banksR.banks)))
		for _, b := range banksR.banks {
			sb.WriteString(fmt.Sprintf("  • %s [%s] ₦%s priority=%d\n",
				b.BankName, b.AccountNumber, formatAmount(b.LastBalance), b.Priority))
		}
	} else {
		sb.WriteString("LINKED BANKS: none\n")
	}

	if action, ok := h.loadPending(ctx, userID); ok {
		switch action.Kind {
		case kindTransfer:
			t := action.Transfer
			sb.WriteString(fmt.Sprintf(
				"\nPENDING TRANSFER (awaiting confirmation):\n  Amount: ₦%.2f  To: %s  Bank: %s  Name: %s\n"+
					"  → Affirmative = confirm_transfer\n"+
					"  → Negative = cancel_transfer\n",
				float64(t.Amount)/100, t.RecipientAccount, t.RecipientBank, t.RecipientName,
			))
		case kindUnlink:
			u := action.Unlink
			sb.WriteString(fmt.Sprintf(
				"\nPENDING UNLINK (awaiting confirmation): %s (%s)\n"+
					"  → Affirmative = confirm_transfer\n"+
					"  → Negative = cancel_transfer\n",
				u.BankName, u.AccountNumber,
			))
		}
	}

	if acctR.ok {
		sb.WriteString(fmt.Sprintf("VIRTUAL ACCOUNT: %s at %s (name: %s)\n",
			acctR.number, acctR.bank, acctR.name))
	} else {
		sb.WriteString("VIRTUAL ACCOUNT: not yet created\n")
	}

	return sb.String()
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

// --- capturingResponseWriter ---

// capturingResponseWriter wraps http.ResponseWriter to capture the response body
// so the chat handler can save the assistant's reply to conversation history.
type capturingResponseWriter struct {
	http.ResponseWriter
	body       []byte
	statusCode int
}

func (c *capturingResponseWriter) WriteHeader(code int) {
	c.statusCode = code
	c.ResponseWriter.WriteHeader(code)
}

func (c *capturingResponseWriter) Write(b []byte) (int, error) {
	c.body = append(c.body, b...)
	return c.ResponseWriter.Write(b)
}
