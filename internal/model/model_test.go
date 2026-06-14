package model

import (
	"testing"
)

func TestAmount_Kobo(t *testing.T) {
	if NewAmount(1).Kobo() != 100 {
		t.Errorf("expected 100 kobo for ₦1, got %d", NewAmount(1).Kobo())
	}
	if Amount(0).Kobo() != 0 {
		t.Errorf("expected 0 kobo for 0, got %d", Amount(0).Kobo())
	}
}

func TestAmount_NGN(t *testing.T) {
	if Amount(10000).NGN() != 100.00 {
		t.Errorf("expected 100.00 NGN for 10000 kobo, got %f", Amount(10000).NGN())
	}
	if Amount(1).NGN() != 0.01 {
		t.Errorf("expected 0.01 NGN for 1 kobo, got %f", Amount(1).NGN())
	}
}

func TestAmount_Arithmetic(t *testing.T) {
	a := Amount(50000)
	b := Amount(20000)

	if sum := a.Add(b); sum != 70000 {
		t.Errorf("50000 + 20000 = %d, expected 70000", sum)
	}
	if diff := a.Sub(b); diff != 30000 {
		t.Errorf("50000 - 20000 = %d, expected 30000", diff)
	}
	if mul := a.Mul(2); mul != 100000 {
		t.Errorf("50000 * 2 = %d, expected 100000", mul)
	}
}

func TestAmount_Comparisons(t *testing.T) {
	if Amount(0).IsZero() != true {
		t.Error("0 should be zero")
	}
	if Amount(100).IsZero() != false {
		t.Error("100 should not be zero")
	}
	if Amount(-100).IsNegative() != true {
		t.Error("-100 should be negative")
	}
	if Amount(100).IsNegative() != false {
		t.Error("100 should not be negative")
	}
	if Amount(500).CanCover(Amount(300)) != true {
		t.Error("500 should cover 300")
	}
	if Amount(200).CanCover(Amount(300)) != false {
		t.Error("200 should not cover 300")
	}
}

func TestNewAmount(t *testing.T) {
	a := NewAmount(5)
	if a != 500 {
		t.Errorf("NewAmount(5) should be 500 kobo, got %d", a)
	}
}

func TestDebitPlan(t *testing.T) {
	plan := &DebitPlan{
		Legs: []DebitLeg{
			{Source: "wallet", Amount: 30000, Fee: 0, BankName: "Weave Wallet"},
			{Source: "bank-uuid", Amount: 20000, Fee: 3500, BankName: "GTBank"},
		},
		Total: 50000,
		Fees:  3500,
	}

	if plan.Total != 50000 {
		t.Errorf("expected total 50000, got %d", plan.Total)
	}
	if len(plan.Legs) != 2 {
		t.Errorf("expected 2 legs, got %d", len(plan.Legs))
	}
	if plan.Fees != 3500 {
		t.Errorf("expected fees 3500, got %d", plan.Fees)
	}
}

func TestTransactionStatus(t *testing.T) {
	tests := []struct {
		status TransactionStatus
		want   string
	}{
		{TxnStatusPending, "PENDING"},
		{TxnStatusProcessing, "PROCESSING"},
		{TxnStatusCompleted, "COMPLETED"},
		{TxnStatusFailed, "FAILED"},
		{TxnStatusRefunded, "REFUNDED"},
	}
	for _, tt := range tests {
		if string(tt.status) != tt.want {
			t.Errorf("expected %s, got %s", tt.want, string(tt.status))
		}
	}
}

func TestTransactionType(t *testing.T) {
	tests := []struct {
		typ  TransactionType
		want string
	}{
		{TxnTypeDebitLeg, "debit_leg"},
		{TxnTypePayoutLeg, "payout_leg"},
		{TxnTypeDeposit, "deposit"},
		{TxnTypeFee, "fee"},
		{TxnTypeRefund, "refund"},
		{TxnTypeHold, "hold"},
		{TxnTypeRelease, "release"},
	}
	for _, tt := range tests {
		if string(tt.typ) != tt.want {
			t.Errorf("expected %s, got %s", tt.want, string(tt.typ))
		}
	}
}

func TestKYCLevel(t *testing.T) {
	if KYCLevelBasic != 1 {
		t.Errorf("expected KYCLevelBasic = 1, got %d", KYCLevelBasic)
	}
	if KYCLevelVerified != 2 {
		t.Errorf("expected KYCLevelVerified = 2, got %d", KYCLevelVerified)
	}
	if KYCLevelEnhanced != 3 {
		t.Errorf("expected KYCLevelEnhanced = 3, got %d", KYCLevelEnhanced)
	}
}
