package handler

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/abdulsalamcodes/weave-server/internal/model"
	"github.com/abdulsalamcodes/weave-server/internal/provider/llm"
	"github.com/abdulsalamcodes/weave-server/internal/repository"
	"github.com/abdulsalamcodes/weave-server/internal/service"
)

// --- Send Money ---

func (h *ChatHandler) handleSendMoney(w http.ResponseWriter, r *http.Request, userID uuid.UUID, parsed *llm.ParsedIntent) {
	amount := model.NewAmount(int64(parsed.Amount))
	if amount.IsZero() {
		respondJSON(w, http.StatusOK, intentResponse{
			Response: "How much would you like to send? For example: \"send 5000 naira to 0123456789\"",
			Intent:   parsed.Intent,
		})
		return
	}

	if parsed.RecipientAccount == "" {
		respondJSON(w, http.StatusOK, intentResponse{
			Response: fmt.Sprintf("Got it — ₦%s. What account number should I send to?", formatAmount(amount)),
			Intent:   parsed.Intent,
		})
		return
	}

	// Resolve the recipient's name once and reuse the bank list for both lookup and storage.
	recipientName := parsed.RecipientName
	if recipientName == "" && h.paystack != nil && parsed.RecipientBank != "" {
		if banks, err := h.paystack.ListBanks(r.Context()); err == nil {
			if code := resolveBankCode(banks, parsed.RecipientBank); code != "" {
				if res, err := h.paystack.ResolveAccount(r.Context(), parsed.RecipientAccount, code); err == nil {
					recipientName = res.Data.AccountName
				}
			}
		}
	}

	action := pendingAction{
		Kind: kindTransfer,
		Transfer: &pendingTransfer{
			Amount:           amount.Kobo(),
			RecipientAccount: parsed.RecipientAccount,
			RecipientBank:    parsed.RecipientBank,
			RecipientName:    recipientName,
			IdempotencyKey:   userID.String() + ":" + hashMessage(parsed.Raw),
		},
	}
	h.storePending(r.Context(), userID, action)

	var msg strings.Builder
	msg.WriteString("Here's the transfer plan:\n\n")
	msg.WriteString(fmt.Sprintf("  Amount:  ₦%s\n", formatAmount(amount)))
	msg.WriteString(fmt.Sprintf("  To:      %s", parsed.RecipientAccount))
	if parsed.RecipientBank != "" {
		msg.WriteString(fmt.Sprintf(" (%s)", parsed.RecipientBank))
	}
	msg.WriteString("\n")
	if recipientName != "" {
		msg.WriteString(fmt.Sprintf("  Name:    %s\n", recipientName))
	}
	msg.WriteString("\nReply \"yes\" to confirm or \"cancel\" to abort.")

	respondJSON(w, http.StatusOK, intentResponse{
		Response: msg.String(),
		Intent:   parsed.Intent,
		Data: map[string]interface{}{
			"amount":            amount.NGN(),
			"recipient_account": parsed.RecipientAccount,
			"recipient_bank":    parsed.RecipientBank,
			"recipient_name":    recipientName,
			"awaiting_confirm":  true,
		},
	})
}

// --- Confirm / Cancel ---

func (h *ChatHandler) handleConfirm(w http.ResponseWriter, r *http.Request, userID uuid.UUID) {
	action, ok := h.loadPending(r.Context(), userID)
	if !ok {
		respondJSON(w, http.StatusOK, intentResponse{
			Response: "No pending action to confirm. It may have expired (10 min limit). Start a new transfer by saying \"send 5000 to 0123456789\".",
			Intent:   llm.IntentConfirmTx,
		})
		return
	}

	switch action.Kind {
	case kindTransfer:
		h.executeTransfer(w, r, userID, action.Transfer)
	case kindUnlink:
		h.executeUnlink(w, r, userID, action.Unlink)
	}
}

func (h *ChatHandler) executeTransfer(w http.ResponseWriter, r *http.Request, userID uuid.UUID, t *pendingTransfer) {
	amount := model.Amount(t.Amount)
	result, err := h.transferService.InitiateTransfer(r.Context(), userID, service.TransferRequest{
		Amount:           amount,
		RecipientAccount: t.RecipientAccount,
		RecipientBank:    t.RecipientBank,
		RecipientName:    t.RecipientName,
		IdempotencyKey:   t.IdempotencyKey,
	})
	if err != nil {
		var response string
		switch {
		case errors.Is(err, service.ErrInsufficientFunds):
			response = "You don't have enough funds to complete this transfer. Fund your wallet or link a bank account with sufficient balance."
		default:
			response = "The transfer couldn't be completed: " + err.Error()
		}
		respondJSON(w, http.StatusOK, intentResponse{Response: response, Intent: llm.IntentConfirmTx})
		return
	}

	// Delete the pending key only after a successful transfer.
	h.clearPending(r.Context(), userID)

	var msg strings.Builder
	msg.WriteString("✅ Done!\n\n")
	totalFees := model.Amount(0)
	if result.DebitPlan != nil {
		for _, leg := range result.DebitPlan.Legs {
			totalFees += leg.Fee
			feeStr := ""
			if leg.Fee > 0 {
				feeStr = fmt.Sprintf(" + ₦%s fee", formatAmount(leg.Fee))
			}
			msg.WriteString(fmt.Sprintf("  %s  -₦%s%s\n", leg.BankName, formatAmount(leg.Amount), feeStr))
		}
	}
	recipient := t.RecipientAccount
	if t.RecipientName != "" {
		recipient = fmt.Sprintf("%s (%s)", t.RecipientName, t.RecipientAccount)
	}
	msg.WriteString(fmt.Sprintf("\nSent ₦%s to %s", formatAmount(amount), recipient))
	if totalFees > 0 {
		msg.WriteString(fmt.Sprintf(" · fees ₦%s", formatAmount(totalFees)))
	}
	msg.WriteString(fmt.Sprintf("\nRef: %s", result.OurRef))

	respondJSON(w, http.StatusOK, intentResponse{
		Response: msg.String(),
		Intent:   llm.IntentConfirmTx,
		Data:     map[string]interface{}{"transaction_id": result.TransactionID, "our_ref": result.OurRef},
	})
}

func (h *ChatHandler) executeUnlink(w http.ResponseWriter, r *http.Request, userID uuid.UUID, u *pendingUnlink) {
	bankID, err := uuid.Parse(u.BankID)
	if err != nil {
		respondJSON(w, http.StatusOK, intentResponse{Response: "Something went wrong. Please try again.", Intent: llm.IntentUnlinkBank})
		return
	}

	if err := h.bankRepo.Delete(r.Context(), bankID); err != nil {
		respondJSON(w, http.StatusOK, intentResponse{Response: "Failed to unlink the bank. Please try again.", Intent: llm.IntentUnlinkBank})
		return
	}

	h.clearPending(r.Context(), userID)

	respondJSON(w, http.StatusOK, intentResponse{
		Response: fmt.Sprintf("✅ %s (%s) has been unlinked.", u.BankName, u.AccountNumber),
		Intent:   llm.IntentUnlinkBank,
	})
}

func (h *ChatHandler) handleCancel(w http.ResponseWriter, r *http.Request, userID uuid.UUID) {
	h.clearPending(r.Context(), userID)
	respondJSON(w, http.StatusOK, intentResponse{
		Response: "Cancelled. Let me know if you'd like to do something else.",
		Intent:   llm.IntentCancelTx,
	})
}

// --- Check Balance ---

func (h *ChatHandler) handleCheckBalance(w http.ResponseWriter, r *http.Request, userID uuid.UUID) {
	wallet, err := h.walletService.GetBalance(r.Context(), userID)
	if err != nil {
		respondJSON(w, http.StatusOK, intentResponse{Response: "Sorry, I couldn't fetch your balance right now.", Intent: llm.IntentCheckBal})
		return
	}

	var msg strings.Builder
	msg.WriteString(fmt.Sprintf("💰 Wallet: ₦%s", formatAmount(wallet.Balance)))
	if wallet.Balance != wallet.LedgerBalance {
		msg.WriteString(fmt.Sprintf(" (₦%s available)", formatAmount(wallet.LedgerBalance)))
	}

	banks, _ := h.bankRepo.GetByUserID(r.Context(), userID, 10, 0)
	if len(banks) > 0 {
		msg.WriteString(fmt.Sprintf("\n\n🏦 Linked banks (%d):", len(banks)))
		for _, b := range banks {
			msg.WriteString(fmt.Sprintf("\n  • %s — ₦%s", b.BankName, formatAmount(b.LastBalance)))
		}
	} else {
		msg.WriteString("\n\nNo linked banks yet. Tap Banks to add one.")
	}

	respondJSON(w, http.StatusOK, intentResponse{
		Response: msg.String(),
		Intent:   llm.IntentCheckBal,
		Data:     map[string]interface{}{"balance": wallet.Balance.NGN(), "ledger_balance": wallet.LedgerBalance.NGN()},
	})
}

// --- Transfer History ---

func (h *ChatHandler) handleTransferHistory(w http.ResponseWriter, r *http.Request, userID uuid.UUID) {
	filter := repository.TransactionFilter{
		Limit: 5,
		Types: []model.TransactionType{model.TxnTypeDebitLeg, model.TxnTypePayoutLeg},
	}
	txns, err := h.txnRepo.ListByUserID(r.Context(), userID, filter)
	if err != nil || len(txns) == 0 {
		respondJSON(w, http.StatusOK, intentResponse{Response: "You have no recent transfers.", Intent: llm.IntentTxHistory})
		return
	}

	var msg strings.Builder
	var needsAttention []string

	msg.WriteString(fmt.Sprintf("Last %d transfer(s):\n", len(txns)))
	for _, t := range txns {
		icon := statusIcon(t.Status)
		msg.WriteString(fmt.Sprintf("\n%s ₦%s → %s  %s  %s",
			icon, formatAmount(t.Amount), t.RecipientAccount,
			t.OurRef, t.CreatedAt.Format("Jan 2 15:04"),
		))
		if t.Status == model.TxnStatusFailed && t.FailureReason != "" {
			needsAttention = append(needsAttention, fmt.Sprintf("  ⚠️  %s: %s", t.OurRef, t.FailureReason))
		}
	}

	if len(needsAttention) > 0 {
		msg.WriteString("\n\nNeeds attention:")
		for _, n := range needsAttention {
			msg.WriteString("\n" + n)
		}
	}

	summaries := make([]txnSummary, len(txns))
	for i, t := range txns {
		summaries[i] = toSummary(t)
	}

	respondJSON(w, http.StatusOK, intentResponse{Response: msg.String(), Intent: llm.IntentTxHistory, Data: summaries})
}

// --- Wallet History ---

func (h *ChatHandler) handleWalletHistory(w http.ResponseWriter, r *http.Request, userID uuid.UUID) {
	filter := repository.TransactionFilter{
		Limit: 5,
		Types: []model.TransactionType{model.TxnTypeDeposit},
	}
	txns, err := h.txnRepo.ListByUserID(r.Context(), userID, filter)
	if err != nil || len(txns) == 0 {
		respondJSON(w, http.StatusOK, intentResponse{Response: "No wallet deposits found.", Intent: llm.IntentWalletHistory})
		return
	}

	var msg strings.Builder
	msg.WriteString("Recent wallet deposits:\n")
	for _, t := range txns {
		msg.WriteString(fmt.Sprintf("\n• ₦%s · %s", formatAmount(t.Amount), t.CreatedAt.Format("Jan 2, 15:04")))
	}

	summaries := make([]txnSummary, len(txns))
	for i, t := range txns {
		summaries[i] = toSummary(t)
	}

	respondJSON(w, http.StatusOK, intentResponse{Response: msg.String(), Intent: llm.IntentWalletHistory, Data: summaries})
}

// --- Transfer Status ---

func (h *ChatHandler) handleTransferStatus(w http.ResponseWriter, r *http.Request, userID uuid.UUID, parsed *llm.ParsedIntent) {
	if parsed.Reference == "" {
		respondJSON(w, http.StatusOK, intentResponse{
			Response: "Which transfer? Provide the reference (e.g. WVF-xxx) or say \"show my recent transfers\" to find it.",
			Intent:   llm.IntentTxStatus,
		})
		return
	}

	txn, err := h.txnRepo.GetByOurRef(r.Context(), parsed.Reference)
	if err != nil || txn == nil {
		respondJSON(w, http.StatusOK, intentResponse{
			Response: fmt.Sprintf("No transfer found with reference \"%s\".", parsed.Reference),
			Intent:   llm.IntentTxStatus,
		})
		return
	}

	var msg strings.Builder
	msg.WriteString(fmt.Sprintf("Transfer %s:\n\n", txn.OurRef))
	msg.WriteString(fmt.Sprintf("  Amount:  ₦%s\n", formatAmount(txn.Amount)))
	msg.WriteString(fmt.Sprintf("  To:      %s\n", txn.RecipientAccount))
	msg.WriteString(fmt.Sprintf("  Status:  %s\n", txn.Status))
	msg.WriteString(fmt.Sprintf("  Date:    %s", txn.CreatedAt.Format("Jan 2, 2006 15:04")))
	if txn.FailureReason != "" {
		msg.WriteString(fmt.Sprintf("\n  ⚠️  %s", txn.FailureReason))
	}

	respondJSON(w, http.StatusOK, intentResponse{Response: msg.String(), Intent: llm.IntentTxStatus, Data: toSummary(*txn)})
}

// --- Link Bank ---

func (h *ChatHandler) handleLinkBank(w http.ResponseWriter) {
	respondJSON(w, http.StatusOK, intentResponse{
		Response: "To link a bank account, tap the Banks tab at the bottom then tap \"Link Bank\". You'll go through the Mono secure flow to connect your account.",
		Intent:   llm.IntentLinkBank,
	})
}

// --- List Banks ---

func (h *ChatHandler) handleListBanks(w http.ResponseWriter, r *http.Request, userID uuid.UUID) {
	banks, err := h.bankRepo.GetByUserID(r.Context(), userID, 20, 0)
	if err != nil || len(banks) == 0 {
		respondJSON(w, http.StatusOK, intentResponse{
			Response: "You don't have any linked bank accounts yet. Tap the Banks tab to link one.",
			Intent:   llm.IntentListBanks,
		})
		return
	}

	var msg strings.Builder
	msg.WriteString(fmt.Sprintf("You have %d linked bank account(s):\n", len(banks)))
	for i, b := range banks {
		msg.WriteString(fmt.Sprintf("\n%d. %s · %s\n   Balance: ₦%s · Priority: %d",
			i+1, b.BankName, b.AccountNumber, formatAmount(b.LastBalance), b.Priority,
		))
	}
	msg.WriteString("\n\nSay \"refresh [bank name] balance\" or \"set [bank] as priority 1\" to manage them.")

	respondJSON(w, http.StatusOK, intentResponse{Response: msg.String(), Intent: llm.IntentListBanks, Data: banks})
}

// --- Unlink Bank (with confirmation step) ---

func (h *ChatHandler) handleUnlinkBank(w http.ResponseWriter, r *http.Request, userID uuid.UUID, parsed *llm.ParsedIntent) {
	if parsed.BankIdentifier == "" {
		respondJSON(w, http.StatusOK, intentResponse{
			Response: "Which bank would you like to unlink? Say \"unlink GTBank\" or \"show my banks\" to see your linked accounts.",
			Intent:   llm.IntentUnlinkBank,
		})
		return
	}

	bank := h.findBankByIdentifier(r.Context(), userID, parsed.BankIdentifier)
	if bank == nil {
		respondJSON(w, http.StatusOK, intentResponse{
			Response: fmt.Sprintf("I couldn't find a linked bank matching \"%s\". Say \"show my banks\" to see your accounts.", parsed.BankIdentifier),
			Intent:   llm.IntentUnlinkBank,
		})
		return
	}

	action := pendingAction{
		Kind: kindUnlink,
		Unlink: &pendingUnlink{
			BankID:        bank.ID.String(),
			BankName:      bank.BankName,
			AccountNumber: bank.AccountNumber,
		},
	}
	h.storePending(r.Context(), userID, action)

	respondJSON(w, http.StatusOK, intentResponse{
		Response: fmt.Sprintf("Are you sure you want to unlink %s (%s)?\n\nReply \"yes\" to confirm or \"cancel\" to keep it.",
			bank.BankName, bank.AccountNumber),
		Intent: llm.IntentUnlinkBank,
	})
}

// --- Set Priority ---

func (h *ChatHandler) handleSetPriority(w http.ResponseWriter, r *http.Request, userID uuid.UUID, parsed *llm.ParsedIntent) {
	if parsed.BankIdentifier == "" {
		respondJSON(w, http.StatusOK, intentResponse{
			Response: "Which bank should I update? Say something like \"set GTBank as priority 1\".",
			Intent:   llm.IntentSetPriority,
		})
		return
	}

	priority := parsed.Priority
	if priority < 1 || priority > 5 {
		priority = 1
	}

	bank := h.findBankByIdentifier(r.Context(), userID, parsed.BankIdentifier)
	if bank == nil {
		respondJSON(w, http.StatusOK, intentResponse{
			Response: fmt.Sprintf("I couldn't find a linked bank matching \"%s\".", parsed.BankIdentifier),
			Intent:   llm.IntentSetPriority,
		})
		return
	}

	if err := h.bankRepo.UpdatePriority(r.Context(), bank.ID, priority); err != nil {
		respondJSON(w, http.StatusOK, intentResponse{Response: "Failed to update priority. Please try again.", Intent: llm.IntentSetPriority})
		return
	}

	ordinals := map[int]string{1: "first", 2: "second", 3: "third", 4: "fourth", 5: "last"}
	respondJSON(w, http.StatusOK, intentResponse{
		Response: fmt.Sprintf("✅ %s is now priority %d — it will be used %s when sourcing funds.",
			bank.BankName, priority, ordinals[priority]),
		Intent: llm.IntentSetPriority,
	})
}

// --- Refresh Balance ---

func (h *ChatHandler) handleRefreshBalance(w http.ResponseWriter, r *http.Request, userID uuid.UUID, parsed *llm.ParsedIntent) {
	if h.mono == nil {
		respondJSON(w, http.StatusOK, intentResponse{
			Response: "Balance refresh is not available right now. Please try again later.",
			Intent:   llm.IntentRefreshBalance,
		})
		return
	}

	banks, _ := h.bankRepo.GetByUserID(r.Context(), userID, 20, 0)
	if len(banks) == 0 {
		respondJSON(w, http.StatusOK, intentResponse{Response: "You have no linked banks to refresh.", Intent: llm.IntentRefreshBalance})
		return
	}

	targets := banks
	if parsed.BankIdentifier != "" {
		b := h.findBankByIdentifier(r.Context(), userID, parsed.BankIdentifier)
		if b != nil {
			targets = []model.BankAccount{*b}
		}
	}

	var msg strings.Builder
	msg.WriteString("Balance refresh:\n")
	for _, b := range targets {
		res, err := h.mono.GetBalance(r.Context(), b.ProviderToken)
		if err != nil {
			msg.WriteString(fmt.Sprintf("\n  • %s — couldn't refresh (%s)", b.BankName, err.Error()))
			continue
		}
		newBalance := model.Amount(int64(res.Data.Balance))
		_ = h.bankRepo.UpdateBalance(r.Context(), b.ID, newBalance)
		msg.WriteString(fmt.Sprintf("\n  • %s — ₦%s ✅", b.BankName, formatAmount(newBalance)))
	}

	respondJSON(w, http.StatusOK, intentResponse{Response: msg.String(), Intent: llm.IntentRefreshBalance})
}

// --- Fund Wallet ---

func (h *ChatHandler) handleFundWallet(w http.ResponseWriter, r *http.Request, userID uuid.UUID) {
	account, err := h.walletService.GetWalletAccount(r.Context(), userID)
	if err != nil || account == nil {
		respondJSON(w, http.StatusOK, intentResponse{
			Response: "You don't have a virtual account yet. Go to the Accounts tab and tap \"Create Account\" to get a dedicated account number you can fund from any bank.",
			Intent:   llm.IntentFundWallet,
		})
		return
	}

	respondJSON(w, http.StatusOK, intentResponse{
		Response: fmt.Sprintf(
			"Transfer money to your Weave wallet:\n\n"+
				"  Bank:    %s\n"+
				"  Account: %s\n"+
				"  Name:    %s\n\n"+
				"Your wallet balance will update automatically once received.",
			account.BankName, account.AccountNumber, account.AccountName,
		),
		Intent: llm.IntentFundWallet,
		Data:   account,
	})
}

// --- Lookup Account ---

func (h *ChatHandler) handleLookupAccount(w http.ResponseWriter, r *http.Request, parsed *llm.ParsedIntent) {
	if h.paystack == nil {
		respondJSON(w, http.StatusOK, intentResponse{
			Response: "Account lookup is not available right now.",
			Intent:   llm.IntentLookupAccount,
		})
		return
	}

	if parsed.RecipientAccount == "" {
		respondJSON(w, http.StatusOK, intentResponse{
			Response: "Which account number would you like to look up? Say \"who is 0123456789 at GTBank?\"",
			Intent:   llm.IntentLookupAccount,
		})
		return
	}

	if parsed.RecipientBank == "" {
		respondJSON(w, http.StatusOK, intentResponse{
			Response: fmt.Sprintf("Which bank is account %s with?", parsed.RecipientAccount),
			Intent:   llm.IntentLookupAccount,
		})
		return
	}

	banks, err := h.paystack.ListBanks(r.Context())
	if err != nil {
		respondJSON(w, http.StatusOK, intentResponse{Response: "Couldn't fetch bank list right now. Please try again.", Intent: llm.IntentLookupAccount})
		return
	}

	bankCode := resolveBankCode(banks, parsed.RecipientBank)
	if bankCode == "" {
		respondJSON(w, http.StatusOK, intentResponse{
			Response: fmt.Sprintf("I don't recognise \"%s\" as a bank. Try the full name e.g. \"GTBank\", \"Access Bank\", \"Zenith Bank\".", parsed.RecipientBank),
			Intent:   llm.IntentLookupAccount,
		})
		return
	}

	result, err := h.paystack.ResolveAccount(r.Context(), parsed.RecipientAccount, bankCode)
	if err != nil {
		respondJSON(w, http.StatusOK, intentResponse{
			Response: fmt.Sprintf("Account %s at %s could not be resolved. Double-check the number and bank.", parsed.RecipientAccount, parsed.RecipientBank),
			Intent:   llm.IntentLookupAccount,
		})
		return
	}

	respondJSON(w, http.StatusOK, intentResponse{
		Response: fmt.Sprintf("✅ Found:\n\n  Name:    %s\n  Account: %s\n  Bank:    %s\n\nSay \"send [amount] to %s at %s\" to transfer to them.",
			result.Data.AccountName, result.Data.AccountNumber, parsed.RecipientBank,
			result.Data.AccountNumber, parsed.RecipientBank,
		),
		Intent: llm.IntentLookupAccount,
		Data:   result.Data,
	})
}

// --- Help ---

func (h *ChatHandler) handleHelp(w http.ResponseWriter) {
	respondJSON(w, http.StatusOK, intentResponse{
		Response: "Here's everything I can help you with:\n\n" +
			"💸 Send money — \"send 5000 to 0123456789 at GTBank\"\n" +
			"✅ Confirm/cancel — \"yes\" or \"cancel\" after a preview\n" +
			"💰 Check balance — \"what's my balance?\"\n" +
			"📋 Transfer history — \"show my recent transfers\"\n" +
			"🔎 Transfer status — \"what's the status of WVF-abc123?\"\n" +
			"🔍 Lookup account — \"who is 0123456789 at Zenith?\"\n" +
			"➕ Fund wallet — \"how do I fund my wallet?\"\n" +
			"📊 Wallet deposits — \"show my wallet history\"\n" +
			"🏦 Linked banks — \"show my bank accounts\"\n" +
			"🔗 Link a bank — \"link my GTBank account\"\n" +
			"🗑️ Unlink a bank — \"unlink my Access Bank\"\n" +
			"⭐ Set priority — \"make Zenith my priority 1 account\"\n" +
			"🔄 Refresh balance — \"refresh my GTBank balance\"",
		Intent: llm.IntentHelp,
	})
}

// --- Helpers ---

func statusIcon(s model.TransactionStatus) string {
	switch s {
	case model.TxnStatusFailed:
		return "❌"
	case model.TxnStatusPending:
		return "⏳"
	default:
		return "✅"
	}
}

