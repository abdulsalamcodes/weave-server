package service

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/abdulsalamcodes/weave-server/internal/model"
)

func TestWalletService_GetBalance(t *testing.T) {
	walletRepo := newMockWalletRepo()
	userRepo := newMockUserRepo()
	svc := NewWalletService(walletRepo, userRepo, nil, "", nil, nil, nil, nil, newTestLogger())

	user, _ := userRepo.Create(context.Background(), model.CreateUserInput{Phone: "+2348010000000", FullName: "Test", PIN: "hash"})
	wallet := &model.Wallet{UserID: user.ID, Balance: 50000, LedgerBalance: 40000, Currency: "NGN"}
	walletRepo.Create(context.Background(), wallet)

	t.Run("found", func(t *testing.T) {
		w, err := svc.GetBalance(context.Background(), user.ID)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if w.Balance != 50000 {
			t.Errorf("expected balance 50000, got %d", w.Balance)
		}
		if w.LedgerBalance != 40000 {
			t.Errorf("expected ledger balance 40000, got %d", w.LedgerBalance)
		}
	})

	t.Run("not found", func(t *testing.T) {
		_, err := svc.GetBalance(context.Background(), uuid.New())
		if err != ErrWalletNotFound {
			t.Errorf("expected ErrWalletNotFound, got %v", err)
		}
	})
}

func TestWalletService_CreditDebit(t *testing.T) {
	walletRepo := newMockWalletRepo()
	userRepo := newMockUserRepo()
	svc := NewWalletService(walletRepo, userRepo, nil, "", nil, nil, nil, nil, newTestLogger())

	user, _ := userRepo.Create(context.Background(), model.CreateUserInput{Phone: "+2348020000000", FullName: "Test", PIN: "hash"})
	wallet := &model.Wallet{UserID: user.ID, Balance: 0, LedgerBalance: 0, Currency: "NGN"}
	walletRepo.Create(context.Background(), wallet)

	t.Run("credit", func(t *testing.T) {
		if err := svc.CreditWallet(context.Background(), wallet.ID, 10000, "ref-1", "test credit"); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		w, _ := walletRepo.GetByID(context.Background(), wallet.ID)
		if w.Balance != 10000 {
			t.Errorf("expected balance 10000, got %d", w.Balance)
		}
	})

	t.Run("debit success", func(t *testing.T) {
		if err := svc.DebitWallet(context.Background(), wallet.ID, 4000, "ref-2", "test debit"); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		w, _ := walletRepo.GetByID(context.Background(), wallet.ID)
		if w.Balance != 6000 {
			t.Errorf("expected balance 6000, got %d", w.Balance)
		}
	})

	t.Run("debit insufficient", func(t *testing.T) {
		err := svc.DebitWallet(context.Background(), wallet.ID, 10000, "ref-3", "overdraft")
		if err == nil {
			t.Fatal("expected error for insufficient balance")
		}
	})

	t.Run("credit zero amount", func(t *testing.T) {
		err := svc.CreditWallet(context.Background(), wallet.ID, 0, "ref-4", "zero")
		if err == nil {
			t.Fatal("expected error for zero amount")
		}
	})

	t.Run("debit negative amount", func(t *testing.T) {
		err := svc.DebitWallet(context.Background(), wallet.ID, -100, "ref-5", "negative")
		if err == nil {
			t.Fatal("expected error for negative amount")
		}
	})
}

func TestWalletService_HoldRelease(t *testing.T) {
	walletRepo := newMockWalletRepo()
	userRepo := newMockUserRepo()
	svc := NewWalletService(walletRepo, userRepo, nil, "", nil, nil, nil, nil, newTestLogger())

	user, _ := userRepo.Create(context.Background(), model.CreateUserInput{Phone: "+2348030000000", FullName: "Test", PIN: "hash"})
	wallet := &model.Wallet{UserID: user.ID, Balance: 50000, LedgerBalance: 50000, Currency: "NGN"}
	walletRepo.Create(context.Background(), wallet)

	t.Run("hold success", func(t *testing.T) {
		if err := svc.Hold(context.Background(), wallet.ID, 10000, "hold-1"); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		w, _ := walletRepo.GetByID(context.Background(), wallet.ID)
		if w.Balance != 50000 {
			t.Errorf("balance should be unchanged: got %d", w.Balance)
		}
		if w.LedgerBalance != 40000 {
			t.Errorf("ledger balance should be 40000, got %d", w.LedgerBalance)
		}
	})

	t.Run("hold insufficient", func(t *testing.T) {
		err := svc.Hold(context.Background(), wallet.ID, 50000, "hold-2")
		if err == nil {
			t.Fatal("expected error for insufficient available balance")
		}
	})

	t.Run("release", func(t *testing.T) {
		if err := svc.ReleaseHold(context.Background(), wallet.ID, 10000, "hold-1"); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		w, _ := walletRepo.GetByID(context.Background(), wallet.ID)
		if w.LedgerBalance != 50000 {
			t.Errorf("ledger balance should be back to 50000, got %d", w.LedgerBalance)
		}
	})
}

func TestWalletService_IssueWalletAccount_NoPaystack(t *testing.T) {
	walletRepo := newMockWalletRepo()
	userRepo := newMockUserRepo()
	svc := NewWalletService(walletRepo, userRepo, nil, "", nil, nil, nil, nil, newTestLogger())

	user, _ := userRepo.Create(context.Background(), model.CreateUserInput{Phone: "+2348040000000", FullName: "Test", PIN: "hash"})

	_, err := svc.IssueWalletAccount(context.Background(), user.ID)
	if err == nil {
		t.Fatal("expected error when paystack is not configured")
	}
}

func TestWalletService_ProcessDepositWebhook(t *testing.T) {
	walletRepo := newMockWalletRepo()
	userRepo := newMockUserRepo()
	svc := NewWalletService(walletRepo, userRepo, nil, "", nil, nil, nil, nil, newTestLogger())

	user, _ := userRepo.Create(context.Background(), model.CreateUserInput{Phone: "+2348050000000", FullName: "Test", PIN: "hash"})
	wallet := &model.Wallet{UserID: user.ID, Balance: 0, LedgerBalance: 0, Currency: "NGN"}
	walletRepo.Create(context.Background(), wallet)
	walletRepo.CreateWalletAccount(context.Background(), &model.WalletAccount{
		UserID:        user.ID,
		AccountNumber: "1234567890",
		AccountName:   "Test User",
		BankName:      "Wema Bank",
		IsActive:      true,
	})

	t.Run("successful deposit", func(t *testing.T) {
		err := svc.ProcessDepositWebhook(context.Background(), "1234567890", 50000, 500, "paystack", "ps_ref_1")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		w, _ := walletRepo.GetByUserID(context.Background(), user.ID)
		if w.Balance != 49500 {
			t.Errorf("expected balance 49500 (50000-500), got %d", w.Balance)
		}
		if w.LedgerBalance != 49500 {
			t.Errorf("expected ledger 49500, got %d", w.LedgerBalance)
		}
	})

	t.Run("unknown account", func(t *testing.T) {
		err := svc.ProcessDepositWebhook(context.Background(), "0000000000", 1000, 0, "paystack", "ps_ref_2")
		if err == nil {
			t.Fatal("expected error for unknown account")
		}
	})
}

func TestSourcingEngine_BuildDebitPlan_FromWallet(t *testing.T) {
	walletRepo := newMockWalletRepo()
	bankRepo := newMockBankRepo()
	userRepo := newMockUserRepo()
	walletSvc := NewWalletService(walletRepo, userRepo, nil, "", nil, nil, nil, nil, newTestLogger())
	engine := NewSourcingEngine(walletSvc, bankRepo, nil, nil, newTestLogger())

	user, _ := userRepo.Create(context.Background(), model.CreateUserInput{Phone: "+2348060000000", FullName: "Test", PIN: "hash"})
	wallet := &model.Wallet{UserID: user.ID, Balance: 50000, LedgerBalance: 50000, Currency: "NGN"}
	walletRepo.Create(context.Background(), wallet)

	t.Run("wallet covers full amount", func(t *testing.T) {
		plan, err := engine.BuildDebitPlan(context.Background(), user.ID, 30000)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if plan.Total != 30000 {
			t.Errorf("expected total 30000, got %d", plan.Total)
		}
		if len(plan.Legs) != 1 {
			t.Fatalf("expected 1 leg, got %d", len(plan.Legs))
		}
		if plan.Legs[0].Source != "wallet" {
			t.Errorf("expected source 'wallet', got %q", plan.Legs[0].Source)
		}
		if plan.Legs[0].Amount != 30000 {
			t.Errorf("expected leg amount 30000, got %d", plan.Legs[0].Amount)
		}
	})

	t.Run("wallet partially covers, bank completes", func(t *testing.T) {
		bankRepo.Create(context.Background(), &model.BankAccount{
			UserID:     user.ID,
			BankName:   "GTBank",
			LastBalance: 50000,
			MinBalance: 10000,
			IsActive:   true,
			IsVerified: true,
			Priority:   1,
		})

		plan, err := engine.BuildDebitPlan(context.Background(), user.ID, 70000)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if plan.Total != 70000 {
			t.Errorf("expected total 70000, got %d", plan.Total)
		}
		if len(plan.Legs) != 2 {
			t.Fatalf("expected 2 legs, got %d", len(plan.Legs))
		}
		if plan.Legs[0].Source != "wallet" || plan.Legs[0].Amount != 50000 {
			t.Errorf("expected first leg wallet amount 50000, got %s %d", plan.Legs[0].Source, plan.Legs[0].Amount)
		}
		if plan.Legs[1].Amount != 20000 {
			t.Errorf("expected bank leg amount 20000, got %d", plan.Legs[1].Amount)
		}
		if plan.Fees <= 0 {
			t.Error("expected fees > 0 since bank leg has NIP fee")
		}
	})

	t.Run("insufficient across all sources", func(t *testing.T) {
		_, err := engine.BuildDebitPlan(context.Background(), user.ID, 200000)
		if err == nil {
			t.Fatal("expected error for insufficient funds")
		}
	})

	t.Run("no wallet", func(t *testing.T) {
		_, err := engine.BuildDebitPlan(context.Background(), uuid.New(), 1000)
		if err == nil {
			t.Fatal("expected error when user has no wallet or bank accounts")
		}
	})
}
