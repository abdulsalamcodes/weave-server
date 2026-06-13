package service

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log/slog"
	"sync"

	"github.com/abdulsalamcodes/weave-server/internal/model"
	"github.com/abdulsalamcodes/weave-server/internal/provider/paystack"
)

type PayoutService struct {
	paystack       *paystack.Client
	recipientCache map[string]string // hash(account+bank) → recipient_code
	mu             sync.RWMutex
	logger         *slog.Logger
}

func NewPayoutService(paystackClient *paystack.Client, logger *slog.Logger) *PayoutService {
	return &PayoutService{
		paystack:       paystackClient,
		recipientCache: make(map[string]string),
		logger:         logger,
	}
}

func (s *PayoutService) createOrGetRecipient(ctx context.Context, accountNumber, bankCode, name string) (string, error) {
	cacheKey := s.recipientKey(accountNumber, bankCode)

	s.mu.RLock()
	if code, ok := s.recipientCache[cacheKey]; ok {
		s.mu.RUnlock()
		return code, nil
	}
	s.mu.RUnlock()

	resp, err := s.paystack.CreateTransferRecipient(ctx, &paystack.CreateRecipientRequest{
		Type:          "nuban",
		Name:          name,
		AccountNumber: accountNumber,
		BankCode:      bankCode,
		Currency:      "NGN",
	})
	if err != nil {
		return "", fmt.Errorf("create recipient: %w", err)
	}

	s.mu.Lock()
	s.recipientCache[cacheKey] = resp.Data.RecipientCode
	s.mu.Unlock()

	s.logger.Info("paystack recipient created",
		"account", accountNumber,
		"bank_code", bankCode,
		"recipient_code", resp.Data.RecipientCode,
	)

	return resp.Data.RecipientCode, nil
}

func (s *PayoutService) SendPayout(ctx context.Context, ourRef string, amount model.Amount, recipientAccount, recipientBank, recipientName string) (string, error) {
	recipientCode, err := s.createOrGetRecipient(ctx, recipientAccount, recipientBank, recipientName)
	if err != nil {
		return "", fmt.Errorf("get recipient: %w", err)
	}

	resp, err := s.paystack.InitiateTransfer(ctx, &paystack.TransferRequest{
		Amount:    int(amount.Kobo()),
		Recipient: recipientCode,
		Reference: ourRef,
		Reason:    "Weave transfer",
	})
	if err != nil {
		return "", fmt.Errorf("initiate transfer: %w", err)
	}

	s.logger.Info("payout initiated",
		"our_ref", ourRef,
		"amount", amount,
		"recipient", recipientAccount,
		"paystack_ref", resp.Data.Reference,
		"paystack_status", resp.Data.Status,
	)

	return resp.Data.Reference, nil
}

func (s *PayoutService) recipientKey(account, bank string) string {
	h := sha256.Sum256([]byte(account + ":" + bank))
	return fmt.Sprintf("%x", h[:16])
}

func (s *PayoutService) IsEnabled() bool {
	return s.paystack != nil
}
