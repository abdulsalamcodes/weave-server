package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/abdulsalamcodes/weave-server/internal/model"
	"github.com/abdulsalamcodes/weave-server/internal/repository"
	"github.com/abdulsalamcodes/weave-server/internal/service"
)

// executeTool dispatches a tool call from the agent loop.
// It is the single entry point — name maps to a typed method below.
func (h *ChatHandler) executeTool(ctx context.Context, name, argsJSON string, userID uuid.UUID) (interface{}, error) {
	switch name {
	case "get_wallet_balance":
		return h.toolGetWalletBalance(ctx, userID)
	case "get_linked_banks":
		return h.toolGetLinkedBanks(ctx, userID)
	case "get_transfer_history":
		return h.toolGetTransferHistory(ctx, userID, argsJSON)
	case "get_transfer_status":
		return h.toolGetTransferStatus(ctx, argsJSON)
	case "lookup_account":
		return h.toolLookupAccount(ctx, argsJSON)
	case "initiate_transfer":
		return h.toolInitiateTransfer(ctx, userID, argsJSON)
	case "confirm_transfer":
		return h.toolConfirmTransfer(ctx, userID)
	case "cancel_transfer":
		return h.toolCancelAction(ctx, userID)
	case "get_wallet_account":
		return h.toolGetWalletAccount(ctx, userID)
	case "refresh_bank_balance":
		return h.toolRefreshBankBalance(ctx, userID, argsJSON)
	case "update_bank_priority":
		return h.toolUpdateBankPriority(ctx, userID, argsJSON)
	case "initiate_unlink":
		return h.toolInitiateUnlink(ctx, userID, argsJSON)
	case "confirm_unlink":
		return h.toolConfirmUnlink(ctx, userID)
	case "get_wallet_history":
		return h.toolGetWalletHistory(ctx, userID, argsJSON)
	case "fund_wallet":
		return h.toolFundWallet(ctx, userID, argsJSON)
	default:
		return nil, fmt.Errorf("unknown tool: %s", name)
	}
}

// --- Tool implementations ---

func (h *ChatHandler) toolGetWalletBalance(ctx context.Context, userID uuid.UUID) (interface{}, error) {
	wallet, err := h.walletService.GetBalance(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("could not fetch wallet balance")
	}
	return map[string]interface{}{
		"available_ngn": wallet.LedgerBalance.NGN(),
		"total_ngn":     wallet.Balance.NGN(),
		"currency":      "NGN",
	}, nil
}

func (h *ChatHandler) toolGetLinkedBanks(ctx context.Context, userID uuid.UUID) (interface{}, error) {
	banks, err := h.bankRepo.GetByUserID(ctx, userID, 20, 0)
	if err != nil {
		return nil, fmt.Errorf("could not fetch linked banks")
	}
	type bankSummary struct {
		ID            string  `json:"id"`
		Name          string  `json:"name"`
		AccountNumber string  `json:"account_number"`
		BalanceNGN    float64 `json:"balance_ngn"`
		Priority      int     `json:"priority"`
	}
	result := make([]bankSummary, len(banks))
	for i, b := range banks {
		result[i] = bankSummary{
			ID:            b.ID.String(),
			Name:          b.BankName,
			AccountNumber: b.AccountNumber,
			BalanceNGN:    b.LastBalance.NGN(),
			Priority:      b.Priority,
		}
	}
	return map[string]interface{}{"banks": result, "count": len(banks)}, nil
}

func (h *ChatHandler) toolGetTransferHistory(ctx context.Context, userID uuid.UUID, argsJSON string) (interface{}, error) {
	var args struct {
		Limit  int    `json:"limit"`
		Status string `json:"status"`
	}
	_ = json.Unmarshal([]byte(argsJSON), &args)
	if args.Limit <= 0 || args.Limit > 20 {
		args.Limit = 5
	}

	filter := repository.TransactionFilter{
		Limit: args.Limit,
		Types: []model.TransactionType{model.TxnTypeDebitLeg, model.TxnTypePayoutLeg},
	}
	if args.Status != "" {
		filter.Statuses = []model.TransactionStatus{model.TransactionStatus(args.Status)}
	}

	txns, err := h.txnRepo.ListByUserID(ctx, userID, filter)
	if err != nil {
		return nil, fmt.Errorf("could not fetch transfer history")
	}

	type row struct {
		Ref           string  `json:"ref"`
		AmountNGN     float64 `json:"amount_ngn"`
		Recipient     string  `json:"recipient"`
		RecipientName string  `json:"recipient_name"`
		Status        string  `json:"status"`
		FailureReason string  `json:"failure_reason,omitempty"`
		Date          string  `json:"date"`
	}
	rows := make([]row, len(txns))
	for i, t := range txns {
		rows[i] = row{
			Ref:           t.OurRef,
			AmountNGN:     t.Amount.NGN(),
			Recipient:     t.RecipientAccount,
			RecipientName: t.RecipientName,
			Status:        string(t.Status),
			FailureReason: t.FailureReason,
			Date:          t.CreatedAt.Format("2006-01-02 15:04"),
		}
	}
	return map[string]interface{}{"transfers": rows, "count": len(rows)}, nil
}

func (h *ChatHandler) toolGetTransferStatus(ctx context.Context, argsJSON string) (interface{}, error) {
	var args struct {
		Reference string `json:"reference"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil || args.Reference == "" {
		return nil, fmt.Errorf("reference is required")
	}

	txn, err := h.txnRepo.GetByOurRef(ctx, args.Reference)
	if err != nil || txn == nil {
		return nil, fmt.Errorf("no transfer found with reference %q", args.Reference)
	}
	return map[string]interface{}{
		"ref":            txn.OurRef,
		"amount_ngn":     txn.Amount.NGN(),
		"recipient":      txn.RecipientAccount,
		"recipient_name": txn.RecipientName,
		"status":         string(txn.Status),
		"failure_reason": txn.FailureReason,
		"date":           txn.CreatedAt.Format("2006-01-02 15:04"),
	}, nil
}

func (h *ChatHandler) toolLookupAccount(ctx context.Context, argsJSON string) (interface{}, error) {
	if h.paystack == nil {
		return nil, fmt.Errorf("account lookup is not available right now")
	}
	var args struct {
		AccountNumber string `json:"account_number"`
		Bank          string `json:"bank"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return nil, fmt.Errorf("invalid arguments")
	}
	if args.AccountNumber == "" || args.Bank == "" {
		return nil, fmt.Errorf("account_number and bank are required")
	}

	banks, err := h.paystack.ListBanks(ctx)
	if err != nil {
		return nil, fmt.Errorf("could not fetch bank list")
	}
	code := resolveBankCode(banks, args.Bank)
	if code == "" {
		return nil, fmt.Errorf("bank %q not recognised — try the full name e.g. GTBank, Access Bank", args.Bank)
	}

	res, err := h.paystack.ResolveAccount(ctx, args.AccountNumber, code)
	if err != nil {
		return nil, fmt.Errorf("account %s at %s could not be resolved", args.AccountNumber, args.Bank)
	}
	return map[string]interface{}{
		"account_number": res.Data.AccountNumber,
		"account_name":   res.Data.AccountName,
		"bank":           args.Bank,
		"bank_code":      code,
	}, nil
}

func (h *ChatHandler) toolInitiateTransfer(ctx context.Context, userID uuid.UUID, argsJSON string) (interface{}, error) {
	var args struct {
		Amount           float64 `json:"amount"`
		RecipientAccount string  `json:"recipient_account"`
		RecipientBank    string  `json:"recipient_bank"`
		RecipientName    string  `json:"recipient_name"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return nil, fmt.Errorf("invalid transfer arguments")
	}
	if args.Amount <= 0 {
		return nil, fmt.Errorf("amount must be greater than zero")
	}
	if args.RecipientAccount == "" || args.RecipientBank == "" {
		return nil, fmt.Errorf("recipient_account and recipient_bank are required")
	}

	amount := model.NewAmount(int64(args.Amount))

	// Auto-resolve recipient name via Paystack if not supplied.
	recipientName := args.RecipientName
	if recipientName == "" && h.paystack != nil {
		if banks, err := h.paystack.ListBanks(ctx); err == nil {
			if code := resolveBankCode(banks, args.RecipientBank); code != "" {
				if res, err := h.paystack.ResolveAccount(ctx, args.RecipientAccount, code); err == nil {
					recipientName = res.Data.AccountName
				}
			}
		}
	}

	action := pendingAction{
		Kind: kindTransfer,
		Transfer: &pendingTransfer{
			Amount:           amount.Kobo(),
			RecipientAccount: args.RecipientAccount,
			RecipientBank:    args.RecipientBank,
			RecipientName:    recipientName,
			IdempotencyKey:   userID.String() + ":" + hashMessage(argsJSON),
		},
	}
	h.storePending(ctx, userID, action)

	return map[string]interface{}{
		"status":            "pending_confirmation",
		"amount_ngn":        amount.NGN(),
		"recipient_account": args.RecipientAccount,
		"recipient_bank":    args.RecipientBank,
		"recipient_name":    recipientName,
		"instruction":       "Present this plan to the user and ask for confirmation. Only call confirm_transfer after they say yes.",
	}, nil
}

func (h *ChatHandler) toolConfirmTransfer(ctx context.Context, userID uuid.UUID) (interface{}, error) {
	action, ok := h.loadPending(ctx, userID)
	if !ok {
		return nil, fmt.Errorf("no pending transfer found — it may have expired")
	}
	if action.Kind != kindTransfer {
		return nil, fmt.Errorf("pending action is not a transfer (it is %q) — call the appropriate confirm tool", action.Kind)
	}

	t := action.Transfer
	amount := model.Amount(t.Amount)
	result, err := h.transferService.InitiateTransfer(ctx, userID, service.TransferRequest{
		Amount:           amount,
		RecipientAccount: t.RecipientAccount,
		RecipientBank:    t.RecipientBank,
		RecipientName:    t.RecipientName,
		IdempotencyKey:   t.IdempotencyKey,
	})
	if err != nil {
		if errors.Is(err, service.ErrInsufficientFunds) {
			return nil, fmt.Errorf("insufficient funds — the user needs to top up their wallet or link a bank with enough balance")
		}
		return nil, fmt.Errorf("transfer failed: %s", err.Error())
	}

	// Clear pending only after success.
	h.clearPending(ctx, userID)

	type legSummary struct {
		Source    string  `json:"source"`
		AmountNGN float64 `json:"amount_ngn"`
		FeeNGN    float64 `json:"fee_ngn"`
	}
	var legs []legSummary
	totalFeeNGN := 0.0
	if result.DebitPlan != nil {
		for _, leg := range result.DebitPlan.Legs {
			legs = append(legs, legSummary{
				Source:    leg.BankName,
				AmountNGN: leg.Amount.NGN(),
				FeeNGN:    leg.Fee.NGN(),
			})
			totalFeeNGN += leg.Fee.NGN()
		}
	}

	return map[string]interface{}{
		"status":          "success",
		"ref":             result.OurRef,
		"transaction_id":  result.TransactionID,
		"amount_ngn":      amount.NGN(),
		"recipient":       t.RecipientAccount,
		"recipient_name":  t.RecipientName,
		"total_fees_ngn":  totalFeeNGN,
		"debit_legs":      legs,
	}, nil
}

func (h *ChatHandler) toolCancelAction(ctx context.Context, userID uuid.UUID) (interface{}, error) {
	h.clearPending(ctx, userID)
	return map[string]string{"status": "cancelled"}, nil
}

func (h *ChatHandler) toolGetWalletAccount(ctx context.Context, userID uuid.UUID) (interface{}, error) {
	acct, err := h.walletService.GetWalletAccount(ctx, userID)
	if err != nil || acct == nil {
		return map[string]interface{}{
			"has_account": false,
			"message":     "No virtual account yet. User must go to the Accounts tab and tap Create Account.",
		}, nil
	}
	return map[string]interface{}{
		"has_account":    true,
		"account_number": acct.AccountNumber,
		"bank_name":      acct.BankName,
		"account_name":   acct.AccountName,
	}, nil
}

func (h *ChatHandler) toolRefreshBankBalance(ctx context.Context, userID uuid.UUID, argsJSON string) (interface{}, error) {
	if h.mono == nil {
		return nil, fmt.Errorf("balance refresh is not available right now")
	}
	var args struct {
		BankIdentifier string `json:"bank_identifier"`
	}
	_ = json.Unmarshal([]byte(argsJSON), &args)

	banks, _ := h.bankRepo.GetByUserID(ctx, userID, 20, 0)
	if len(banks) == 0 {
		return nil, fmt.Errorf("no linked banks found")
	}

	targets := banks
	if args.BankIdentifier != "" {
		b := h.findBankByIdentifier(ctx, userID, args.BankIdentifier)
		if b == nil {
			return nil, fmt.Errorf("no linked bank matching %q", args.BankIdentifier)
		}
		targets = []model.BankAccount{*b}
	}

	type refreshResult struct {
		Name       string  `json:"name"`
		BalanceNGN float64 `json:"balance_ngn"`
		Error      string  `json:"error,omitempty"`
	}
	results := make([]refreshResult, 0, len(targets))
	for _, b := range targets {
		res, err := h.mono.GetBalance(ctx, b.ProviderToken)
		if err != nil {
			results = append(results, refreshResult{Name: b.BankName, Error: err.Error()})
			continue
		}
		newBal := model.Amount(int64(res.Data.Balance))
		_ = h.bankRepo.UpdateBalance(ctx, b.ID, newBal)
		results = append(results, refreshResult{Name: b.BankName, BalanceNGN: newBal.NGN()})
	}
	return map[string]interface{}{"results": results}, nil
}

func (h *ChatHandler) toolUpdateBankPriority(ctx context.Context, userID uuid.UUID, argsJSON string) (interface{}, error) {
	var args struct {
		BankIdentifier string `json:"bank_identifier"`
		Priority       int    `json:"priority"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return nil, fmt.Errorf("invalid arguments")
	}
	if args.Priority < 1 || args.Priority > 5 {
		return nil, fmt.Errorf("priority must be between 1 and 5")
	}

	bank := h.findBankByIdentifier(ctx, userID, args.BankIdentifier)
	if bank == nil {
		return nil, fmt.Errorf("no linked bank matching %q", args.BankIdentifier)
	}
	if err := h.bankRepo.UpdatePriority(ctx, bank.ID, args.Priority); err != nil {
		return nil, fmt.Errorf("failed to update priority")
	}
	return map[string]interface{}{
		"bank":     bank.BankName,
		"priority": args.Priority,
		"status":   "updated",
	}, nil
}

func (h *ChatHandler) toolInitiateUnlink(ctx context.Context, userID uuid.UUID, argsJSON string) (interface{}, error) {
	var args struct {
		BankIdentifier string `json:"bank_identifier"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil || args.BankIdentifier == "" {
		return nil, fmt.Errorf("bank_identifier is required")
	}

	bank := h.findBankByIdentifier(ctx, userID, args.BankIdentifier)
	if bank == nil {
		return nil, fmt.Errorf("no linked bank matching %q", args.BankIdentifier)
	}

	action := pendingAction{
		Kind: kindUnlink,
		Unlink: &pendingUnlink{
			BankID:        bank.ID.String(),
			BankName:      bank.BankName,
			AccountNumber: bank.AccountNumber,
		},
	}
	h.storePending(ctx, userID, action)

	return map[string]interface{}{
		"status":         "pending_confirmation",
		"bank_name":      bank.BankName,
		"account_number": bank.AccountNumber,
		"instruction":    "Ask the user to confirm they want to unlink this bank. Call confirm_unlink only after they say yes.",
	}, nil
}

func (h *ChatHandler) toolConfirmUnlink(ctx context.Context, userID uuid.UUID) (interface{}, error) {
	action, ok := h.loadPending(ctx, userID)
	if !ok {
		return nil, fmt.Errorf("no pending unlink found")
	}
	if action.Kind != kindUnlink {
		return nil, fmt.Errorf("pending action is %q, not an unlink", action.Kind)
	}

	bankID, err := uuid.Parse(action.Unlink.BankID)
	if err != nil {
		return nil, fmt.Errorf("invalid bank reference")
	}
	if err := h.bankRepo.Delete(ctx, bankID); err != nil {
		return nil, fmt.Errorf("failed to unlink bank")
	}

	h.clearPending(ctx, userID)
	return map[string]interface{}{
		"status":    "unlinked",
		"bank_name": action.Unlink.BankName,
		"account":   action.Unlink.AccountNumber,
	}, nil
}

func (h *ChatHandler) toolGetWalletHistory(ctx context.Context, userID uuid.UUID, argsJSON string) (interface{}, error) {
	var args struct {
		Limit int `json:"limit"`
	}
	_ = json.Unmarshal([]byte(argsJSON), &args)
	if args.Limit <= 0 || args.Limit > 20 {
		args.Limit = 5
	}

	filter := repository.TransactionFilter{
		Limit: args.Limit,
		Types: []model.TransactionType{model.TxnTypeDeposit},
	}
	txns, err := h.txnRepo.ListByUserID(ctx, userID, filter)
	if err != nil {
		return nil, fmt.Errorf("could not fetch wallet history")
	}

	type row struct {
		AmountNGN float64 `json:"amount_ngn"`
		Date      string  `json:"date"`
	}
	rows := make([]row, len(txns))
	for i, t := range txns {
		rows[i] = row{AmountNGN: t.Amount.NGN(), Date: t.CreatedAt.Format("2006-01-02 15:04")}
	}
	return map[string]interface{}{"deposits": rows, "count": len(rows)}, nil
}

// directBalance is used by keyword shortcuts to return a formatted balance string
// without going through the full agent loop.
func (h *ChatHandler) directBalance(ctx context.Context, userID uuid.UUID) string {
	result, err := h.toolGetWalletBalance(ctx, userID)
	if err != nil {
		return "Sorry, I couldn't fetch your balance right now."
	}
	data := result.(map[string]interface{})
	avail := data["available_ngn"].(float64)
	total := data["total_ngn"].(float64)

	banks, _ := h.bankRepo.GetByUserID(ctx, userID, 10, 0)
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("💰 Wallet: ₦%.2f", avail))
	if avail != total {
		sb.WriteString(fmt.Sprintf(" (₦%.2f total)", total))
	}
	if len(banks) > 0 {
		sb.WriteString(fmt.Sprintf("\n\n🏦 Linked banks (%d):", len(banks)))
		for _, b := range banks {
			sb.WriteString(fmt.Sprintf("\n  • %s — ₦%s", b.BankName, formatAmount(b.LastBalance)))
		}
	} else {
		sb.WriteString("\n\nNo linked banks yet. Tap Banks to add one.")
	}
	return sb.String()
}

func (h *ChatHandler) toolFundWallet(ctx context.Context, userID uuid.UUID, argsJSON string) (interface{}, error) {
	var args struct {
		BankIdentifier string  `json:"bank_identifier"`
		AmountNGN      float64 `json:"amount_ngn"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return nil, fmt.Errorf("invalid fund_wallet arguments: %w", err)
	}
	if args.AmountNGN <= 0 {
		return nil, errors.New("amount must be positive")
	}
	if args.BankIdentifier == "" {
		return nil, errors.New("bank_identifier is required")
	}

	bank := h.findBankByIdentifier(ctx, userID, args.BankIdentifier)
	if bank == nil {
		return nil, fmt.Errorf("no linked bank found matching %q — check your linked banks with 'show my banks'", args.BankIdentifier)
	}

	amount := model.Amount(int64(args.AmountNGN * 100))
	req, err := h.walletService.FundFromBank(ctx, userID, bank.ID, amount)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrAmountTooSmall):
			return nil, fmt.Errorf("minimum top-up is ₦100")
		case errors.Is(err, service.ErrAmountTooLarge):
			return nil, fmt.Errorf("maximum single top-up is ₦500,000")
		case errors.Is(err, service.ErrDailyLimitExceeded):
			return nil, fmt.Errorf("you've reached your daily top-up limit of ₦1,000,000")
		case errors.Is(err, service.ErrBankNotActive):
			return nil, fmt.Errorf("%s is not currently active. Please unlink and re-link it from the Banks tab — no other steps needed", bank.BankName)
		case errors.Is(err, service.ErrMonoUnavailable):
			return nil, fmt.Errorf("bank top-up is temporarily unavailable — fund your wallet via bank transfer instead")
		case errors.Is(err, service.ErrDirectDebitFailed):
			return nil, fmt.Errorf("Mono rejected the direct debit: %s", err.Error())
		default:
			return nil, fmt.Errorf("top-up failed: %s", err.Error())
		}
	}

	result := map[string]interface{}{
		"reference":  req.Reference,
		"status":     req.Status,
		"amount_ngn": amount.NGN(),
		"bank":       bank.BankName,
	}
	if req.PaymentURL != "" {
		result["payment_url"] = req.PaymentURL
		result["message"] = fmt.Sprintf("₦%.2f top-up from %s initiated (ref: %s).\n\nTo authorize the debit, please visit this link:\n%s\n\nYour wallet will be credited automatically once you approve the payment.", amount.NGN(), bank.BankName, req.Reference, req.PaymentURL)
	} else {
		result["message"] = fmt.Sprintf("₦%.2f top-up from %s initiated (ref: %s). Your wallet will be credited once the debit is confirmed.", amount.NGN(), bank.BankName, req.Reference)
	}
	return result, nil
}

// Suppress unused import
var _ = time.RFC3339
