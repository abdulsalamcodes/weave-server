package service

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/abdulsalamcodes/weave-server/internal/model"
)

func TestTransferService_InitiateTransfer_WalletOnly(t *testing.T) {
	walletRepo := newMockWalletRepo()
	txnRepo := newMockTxnRepo()
	bankRepo := newMockBankRepo()
	userRepo := newMockUserRepo()
	walletSvc := NewWalletService(walletRepo, userRepo, nil, newTestLogger())
	engine := NewSourcingEngine(walletSvc, bankRepo, newTestLogger())
	payoutSvc := NewPayoutService(nil, newTestLogger())
	svc := NewTransferService(txnRepo, walletRepo, walletSvc, engine, payoutSvc, nil, newTestLogger())

	user, _ := userRepo.Create(context.Background(), model.CreateUserInput{Phone: "+2349010000000", FullName: "Transfer Test", PIN: "hash"})
	wallet := &model.Wallet{UserID: user.ID, Balance: 100000, LedgerBalance: 100000, Currency: "NGN"}
	walletRepo.Create(context.Background(), wallet)

	t.Run("successful transfer from wallet only", func(t *testing.T) {
		result, err := svc.InitiateTransfer(context.Background(), user.ID, TransferRequest{
			Amount:           50000,
			RecipientAccount: "0123456789",
			RecipientBank:    "058",
			RecipientName:    "Jane Doe",
			IdempotencyKey:   "idem-1",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Status != "COMPLETED" {
			t.Errorf("expected COMPLETED, got %s", result.Status)
		}
		if result.OurRef == "" {
			t.Error("expected our_ref")
		}
		if result.DebitPlan == nil {
			t.Fatal("expected debit plan")
		}
		if len(result.DebitPlan.Legs) != 1 {
			t.Fatalf("expected 1 leg, got %d", len(result.DebitPlan.Legs))
		}
		if result.DebitPlan.Legs[0].Amount != 50000 {
			t.Errorf("expected leg amount 50000, got %d", result.DebitPlan.Legs[0].Amount)
		}

		// Verify wallet was debited (hold then debit: LedgerBalance = initial - hold - debit)
		w, _ := walletRepo.GetByUserID(context.Background(), user.ID)
		if w.Balance != 50000 {
			t.Errorf("expected wallet balance 50000, got %d", w.Balance)
		}
		if w.LedgerBalance != 0 {
			t.Errorf("expected wallet ledger 0 (held then debited), got %d", w.LedgerBalance)
		}

		// Verify transaction records
		parent, _ := txnRepo.GetByID(context.Background(), result.TransactionID)
		if parent == nil {
			t.Fatal("expected parent transaction")
		}
		if parent.Status != model.TxnStatusCompleted {
			t.Errorf("expected parent completed, got %s", parent.Status)
		}
		children, _ := txnRepo.GetByParentID(context.Background(), parent.ID)
		if len(children) != 2 {
			t.Fatalf("expected 2 child transactions (debit leg + payout), got %d", len(children))
		}
	})

	t.Run("idempotency returns existing result", func(t *testing.T) {
		result, err := svc.InitiateTransfer(context.Background(), user.ID, TransferRequest{
			Amount:           50000,
			RecipientAccount: "0123456789",
			RecipientBank:    "058",
			RecipientName:    "Jane Doe",
			IdempotencyKey:   "idem-1",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// Balance should not have changed (idempotent)
		w, _ := walletRepo.GetByUserID(context.Background(), user.ID)
		if w.Balance != 50000 {
			t.Errorf("balance should still be 50000 after idempotent request, got %d", w.Balance)
		}
		_ = result
	})

	t.Run("duplicate key returns existing", func(t *testing.T) {
		result, err := svc.InitiateTransfer(context.Background(), user.ID, TransferRequest{
			Amount:           1000,
			RecipientAccount: "0123456789",
			RecipientBank:    "058",
			RecipientName:    "Jane Doe",
			IdempotencyKey:   "idem-1",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// Idempotent response has no DebitPlan (returned from cache)
		if result.OurRef == "" {
			t.Error("expected our_ref")
		}
	})
}

func TestTransferService_InitiateTransfer_InsufficientFunds(t *testing.T) {
	walletRepo := newMockWalletRepo()
	txnRepo := newMockTxnRepo()
	bankRepo := newMockBankRepo()
	userRepo := newMockUserRepo()
	walletSvc := NewWalletService(walletRepo, userRepo, nil, newTestLogger())
	engine := NewSourcingEngine(walletSvc, bankRepo, newTestLogger())
	payoutSvc := NewPayoutService(nil, newTestLogger())
	svc := NewTransferService(txnRepo, walletRepo, walletSvc, engine, payoutSvc, nil, newTestLogger())

	user, _ := userRepo.Create(context.Background(), model.CreateUserInput{Phone: "+2349020000000", FullName: "Poor User", PIN: "hash"})
	wallet := &model.Wallet{UserID: user.ID, Balance: 1000, LedgerBalance: 1000, Currency: "NGN"}
	walletRepo.Create(context.Background(), wallet)

	_, err := svc.InitiateTransfer(context.Background(), user.ID, TransferRequest{
		Amount:           50000,
		RecipientAccount: "0123456789",
		RecipientBank:    "058",
		RecipientName:    "Jane Doe",
	})
	if err == nil {
		t.Fatal("expected error for insufficient funds")
	}
}

func TestTransferService_GetTransfer(t *testing.T) {
	txnRepo := newMockTxnRepo()
	svc := NewTransferService(txnRepo, newMockWalletRepo(), nil, nil, nil, nil, newTestLogger())

	parent, _ := txnRepo.Create(context.Background(), model.CreateTransactionInput{
		Amount:   10000,
		Currency: "NGN",
		OurRef:   "WVF-test-1",
	})

	t.Run("by id", func(t *testing.T) {
		txn, err := svc.GetTransfer(context.Background(), parent.ID)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if txn.Amount != 10000 {
			t.Errorf("expected amount 10000, got %d", txn.Amount)
		}
	})

	t.Run("by ref", func(t *testing.T) {
		txn, err := svc.GetTransferByRef(context.Background(), "WVF-test-1")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if txn.ID != parent.ID {
			t.Errorf("expected txn ID %s, got %s", parent.ID, txn.ID)
		}
	})

	t.Run("not found by id", func(t *testing.T) {
		_, err := svc.GetTransfer(context.Background(), uuid.New())
		if err != ErrTransferNotFound {
			t.Errorf("expected ErrTransferNotFound, got %v", err)
		}
	})

	t.Run("not found by ref", func(t *testing.T) {
		_, err := svc.GetTransferByRef(context.Background(), "nonexistent")
		if err != ErrTransferNotFound {
			t.Errorf("expected ErrTransferNotFound, got %v", err)
		}
	})
}
