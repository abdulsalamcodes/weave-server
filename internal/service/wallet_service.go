package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"

	"github.com/abdulsalamcodes/weave-server/internal/model"
	"github.com/abdulsalamcodes/weave-server/internal/provider/paystack"
	"github.com/abdulsalamcodes/weave-server/internal/repository"
)

var (
	ErrWalletNotFound    = errors.New("wallet not found")
	ErrInsufficientFunds = errors.New("insufficient funds")
	ErrNoWalletAccount   = errors.New("no wallet account found")
)

type WalletService struct {
	walletRepo    *repository.WalletRepo
	userRepo      *repository.UserRepo
	paystack      *paystack.Client
	paystackEnabled bool
	logger        *slog.Logger
}

func NewWalletService(
	walletRepo *repository.WalletRepo,
	userRepo *repository.UserRepo,
	paystackClient *paystack.Client,
	logger *slog.Logger,
) *WalletService {
	return &WalletService{
		walletRepo:    walletRepo,
		userRepo:      userRepo,
		paystack:      paystackClient,
		paystackEnabled: paystackClient != nil,
		logger:        logger,
	}
}

func (s *WalletService) GetBalance(ctx context.Context, userID uuid.UUID) (*model.Wallet, error) {
	wallet, err := s.walletRepo.GetByUserID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("get wallet: %w", err)
	}
	if wallet == nil {
		return nil, ErrWalletNotFound
	}
	return wallet, nil
}

func (s *WalletService) CreditWallet(ctx context.Context, walletID uuid.UUID, amount model.Amount, reference, description string) error {
	if amount.IsZero() || amount.IsNegative() {
		return errors.New("invalid credit amount")
	}

	if err := s.walletRepo.Credit(ctx, walletID, amount); err != nil {
		return fmt.Errorf("credit wallet: %w", err)
	}

	wt := &model.WalletTransaction{
		WalletID:    walletID,
		Type:        "credit",
		Amount:      amount,
		Reference:   reference,
		Description: description,
		Status:      "COMPLETED",
	}
	if err := s.walletRepo.RecordTransaction(ctx, wt); err != nil {
		s.logger.Error("failed to record wallet transaction", "error", err, "reference", reference)
	}

	s.logger.Info("wallet credited",
		"wallet_id", walletID,
		"amount", amount,
		"reference", reference,
	)
	return nil
}

func (s *WalletService) DebitWallet(ctx context.Context, walletID uuid.UUID, amount model.Amount, reference, description string) error {
	if amount.IsZero() || amount.IsNegative() {
		return errors.New("invalid debit amount")
	}

	if err := s.walletRepo.Debit(ctx, walletID, amount); err != nil {
		return fmt.Errorf("debit wallet: %w", err)
	}

	wt := &model.WalletTransaction{
		WalletID:    walletID,
		Type:        "debit",
		Amount:      amount,
		Reference:   reference,
		Description: description,
		Status:      "COMPLETED",
	}
	if err := s.walletRepo.RecordTransaction(ctx, wt); err != nil {
		s.logger.Error("failed to record wallet transaction", "error", err, "reference", reference)
	}

	s.logger.Info("wallet debited",
		"wallet_id", walletID,
		"amount", amount,
		"reference", reference,
	)
	return nil
}

func (s *WalletService) Hold(ctx context.Context, walletID uuid.UUID, amount model.Amount, reference string) error {
	if err := s.walletRepo.Hold(ctx, walletID, amount); err != nil {
		return fmt.Errorf("hold wallet: %w", err)
	}

	wt := &model.WalletTransaction{
		WalletID:    walletID,
		Type:        "hold",
		Amount:      amount,
		Reference:   reference,
		Description: "hold for pending transfer",
		Status:      "COMPLETED",
	}
	if err := s.walletRepo.RecordTransaction(ctx, wt); err != nil {
		s.logger.Error("failed to record hold transaction", "error", err)
	}
	return nil
}

func (s *WalletService) ReleaseHold(ctx context.Context, walletID uuid.UUID, amount model.Amount, reference string) error {
	if err := s.walletRepo.ReleaseHold(ctx, walletID, amount); err != nil {
		return fmt.Errorf("release hold: %w", err)
	}

	wt := &model.WalletTransaction{
		WalletID:    walletID,
		Type:        "release",
		Amount:      amount,
		Reference:   reference + "_release",
		Description: "hold released",
		Status:      "COMPLETED",
	}
	if err := s.walletRepo.RecordTransaction(ctx, wt); err != nil {
		s.logger.Error("failed to record release transaction", "error", err)
	}
	return nil
}

func (s *WalletService) IssueWalletAccount(ctx context.Context, userID uuid.UUID) (*model.WalletAccount, error) {
	existing, err := s.walletRepo.GetWalletAccountByUserID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("check existing account: %w", err)
	}
	if existing != nil {
		return existing, nil
	}

	if !s.paystackEnabled {
		return nil, errors.New("paystack not configured: set PAYSTACK_SECRET_KEY")
	}

	user, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("get user: %w", err)
	}
	if user == nil {
		return nil, ErrUserNotFound
	}

	email := user.Email
	if email == "" {
		email = user.Phone + "@weave.ng"
	}

	// Create customer on Paystack
	customer, err := s.paystack.CreateCustomer(ctx, &paystack.CreateCustomerRequest{
		Email: email,
		Phone: user.Phone,
		Name:  user.FullName,
	})
	if err != nil {
		return nil, fmt.Errorf("create paystack customer: %w", err)
	}

	// Assign dedicated virtual account (Wema Bank)
	dva, err := s.paystack.AssignDVA(ctx, customer.Data.CustomerCode, "wema")
	if err != nil {
		return nil, fmt.Errorf("assign dva: %w", err)
	}

	wa := &model.WalletAccount{
		UserID:        userID,
		Provider:      "paystack",
		ProviderRef:   customer.Data.CustomerCode,
		AccountNumber: dva.Data.AccountNumber,
		AccountName:   dva.Data.AccountName,
		BankName:      dva.Data.Bank.Name,
		BankCode:      "",
		IsActive:      true,
		IsDefault:     true,
	}

	if err := s.walletRepo.CreateWalletAccount(ctx, wa); err != nil {
		return nil, fmt.Errorf("save wallet account: %w", err)
	}

	s.logger.Info("wallet account issued",
		"user_id", userID,
		"account_number", wa.AccountNumber,
		"bank", wa.BankName,
		"provider", wa.Provider,
	)

	return wa, nil
}

func (s *WalletService) ProcessDepositWebhook(ctx context.Context, accountNumber string, amount model.Amount, fee model.Amount, provider, providerRef string) error {
	wa, err := s.walletRepo.GetWalletAccountByNumber(ctx, accountNumber)
	if err != nil {
		return fmt.Errorf("lookup wallet account: %w", err)
	}
	if wa == nil {
		return fmt.Errorf("wallet account not found: %s", accountNumber)
	}

	deposit := &model.WalletDeposit{
		WalletAccountID: wa.ID,
		UserID:         wa.UserID,
		Amount:         amount,
		Fee:            fee,
		Provider:       provider,
		ProviderRef:    providerRef,
		Status:         "COMPLETED",
	}

	if err := s.walletRepo.RecordDeposit(ctx, deposit); err != nil {
		return fmt.Errorf("record deposit: %w", err)
	}

	wallet, err := s.walletRepo.GetByUserID(ctx, wa.UserID)
	if err != nil {
		return fmt.Errorf("get user wallet: %w", err)
	}
	if wallet == nil {
		return ErrWalletNotFound
	}

	netAmount := amount - fee
	if netAmount < 0 {
		netAmount = 0
	}

	if err := s.CreditWallet(ctx, wallet.ID, netAmount, providerRef, "deposit via "+provider); err != nil {
		return fmt.Errorf("credit wallet: %w", err)
	}

	s.logger.Info("deposit processed",
		"user_id", wa.UserID,
		"amount", amount,
		"fee", fee,
		"net", netAmount,
		"provider_ref", providerRef,
	)
	return nil
}

type SourcingEngine struct {
	walletService *WalletService
	bankRepo      *repository.BankAccountRepo
	logger        *slog.Logger
}

func NewSourcingEngine(
	walletService *WalletService,
	bankRepo *repository.BankAccountRepo,
	logger *slog.Logger,
) *SourcingEngine {
	return &SourcingEngine{
		walletService: walletService,
		bankRepo:      bankRepo,
		logger:        logger,
	}
}

func (e *SourcingEngine) BuildDebitPlan(ctx context.Context, userID uuid.UUID, amount model.Amount) (*model.DebitPlan, error) {
	plan := &model.DebitPlan{}

	// 1. Check wallet balance first (wallet is always highest priority)
	wallet, err := e.walletService.GetBalance(ctx, userID)
	if err != nil && err != ErrWalletNotFound {
		return nil, fmt.Errorf("get wallet: %w", err)
	}

	remaining := amount

	if wallet != nil && wallet.LedgerBalance > 0 {
		fromWallet := wallet.LedgerBalance
		if fromWallet > remaining {
			fromWallet = remaining
		}
		plan.Legs = append(plan.Legs, model.DebitLeg{
			Source:   "wallet",
			Amount:   fromWallet,
			Fee:      0,
			BankName: "Weave Wallet",
		})
		remaining -= fromWallet
	}

	if remaining <= 0 {
		plan.Total = amount
		return plan, nil
	}

	// 2. Check linked bank accounts (sorted by priority)
	accounts, err := e.bankRepo.GetByUserID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("get bank accounts: %w", err)
	}

	for _, acct := range accounts {
		if !acct.IsActive || !acct.IsVerified {
			continue
		}

		available := acct.LastBalance - acct.MinBalance
		if available <= 0 {
			continue
		}

		fromAcct := available
		if fromAcct > remaining {
			fromAcct = remaining
		}

		fee := model.Amount(35 * 100) // ₦35 NIP fee in kobo
		plan.Legs = append(plan.Legs, model.DebitLeg{
			Source:   acct.ID.String(),
			Amount:   fromAcct,
			Fee:      fee,
			BankName: acct.BankName,
		})

		remaining -= fromAcct
		plan.Fees += fee

		if remaining <= 0 {
			break
		}
	}

	if remaining > 0 {
		return nil, fmt.Errorf("insufficient funds: need %v more across all accounts", remaining.NGN())
	}

	plan.Total = amount
	return plan, nil
}
