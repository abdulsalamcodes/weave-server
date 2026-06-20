package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/abdulsalamcodes/weave-server/internal/model"
	"github.com/abdulsalamcodes/weave-server/internal/provider/mono"
	"github.com/abdulsalamcodes/weave-server/internal/provider/paystack"
	"github.com/abdulsalamcodes/weave-server/internal/repository"
)

var (
	ErrWalletNotFound      = errors.New("wallet not found")
	ErrInsufficientFunds   = errors.New("insufficient funds")
	ErrNoWalletAccount     = errors.New("no wallet account found")
	ErrAmountTooSmall      = errors.New("amount below minimum")
	ErrAmountTooLarge      = errors.New("amount above maximum")
	ErrDailyLimitExceeded  = errors.New("daily top-up limit exceeded")
	ErrBankNotActive       = errors.New("bank account not active or verified")
	ErrMonoUnavailable     = errors.New("mono client not configured")
	ErrDirectDebitFailed   = errors.New("direct debit initiation failed")
	ErrForbidden           = errors.New("resource not owned by user")
)

const (
	fundFromBankMinKobo   model.Amount = 10_000      // ₦100
	fundFromBankMaxKobo   model.Amount = 50_000_000  // ₦500,000 per request
	fundFromBankDailyKobo model.Amount = 100_000_000 // ₦1,000,000 daily limit
)

type WalletService struct {
	walletRepo      repository.WalletRepository
	userRepo        repository.UserRepository
	bankRepo        repository.BankAccountRepository
	bankFundRepo    repository.BankFundRepository
	auditRepo       *repository.AuditLogRepo
	monoClient      *mono.Client
	paystack        *paystack.Client
	paystackEnabled bool
	paystackBank    string
	logger          *slog.Logger
}

func NewWalletService(
	walletRepo repository.WalletRepository,
	userRepo repository.UserRepository,
	paystackClient *paystack.Client,
	paystackBank string,
	bankRepo repository.BankAccountRepository,
	bankFundRepo repository.BankFundRepository,
	auditRepo *repository.AuditLogRepo,
	monoClient *mono.Client,
	logger *slog.Logger,
) *WalletService {
	bank := paystackBank
	if bank == "" {
		bank = "test-bank"
	}
	return &WalletService{
		walletRepo:      walletRepo,
		userRepo:        userRepo,
		bankRepo:        bankRepo,
		bankFundRepo:    bankFundRepo,
		auditRepo:       auditRepo,
		monoClient:      monoClient,
		paystack:        paystackClient,
		paystackEnabled: paystackClient != nil,
		paystackBank:    bank,
		logger:          logger,
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

func (s *WalletService) GetWalletAccount(ctx context.Context, userID uuid.UUID) (*model.WalletAccount, error) {
	return s.walletRepo.GetWalletAccountByUserID(ctx, userID)
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

	firstName, lastName := splitName(user.FullName)
	customer, err := s.paystack.CreateCustomer(ctx, &paystack.CreateCustomerRequest{
		Email:     email,
		Phone:     user.Phone,
		FirstName: firstName,
		LastName:  lastName,
	})
	if err != nil {
		return nil, fmt.Errorf("create paystack customer: %w", err)
	}

	// Assign dedicated virtual account
	dva, err := s.paystack.AssignDVA(ctx, customer.Data.CustomerCode, s.paystackBank)
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

// FundFromBank initiates a Mono DirectPay session. Returns the pending request including
// a PaymentURL the user must visit to authorise the debit. Mono fires "payment.successful"
// on completion, which triggers CompleteFundFromBank to credit the wallet.
func (s *WalletService) FundFromBank(ctx context.Context, userID uuid.UUID, bankAccountID uuid.UUID, amount model.Amount) (*model.BankFundRequest, error) {
	if amount < fundFromBankMinKobo {
		return nil, ErrAmountTooSmall
	}
	if amount > fundFromBankMaxKobo {
		return nil, ErrAmountTooLarge
	}

	// Ownership + activity check.
	ba, err := s.bankRepo.GetByID(ctx, bankAccountID)
	if err != nil {
		return nil, fmt.Errorf("get bank account: %w", err)
	}
	if ba == nil || ba.UserID != userID {
		return nil, ErrForbidden
	}
	if !ba.IsActive || !ba.IsVerified {
		return nil, ErrBankNotActive
	}

	// Daily limit check.
	todaySum, err := s.bankFundRepo.SumCompletedToday(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("check daily limit: %w", err)
	}
	if todaySum+amount > fundFromBankDailyKobo {
		return nil, ErrDailyLimitExceeded
	}

	ref := fmt.Sprintf("WVF-FUND-%s", generateShortID())

	req := &model.BankFundRequest{
		UserID:        userID,
		BankAccountID: bankAccountID,
		Reference:     ref,
		Amount:        amount,
		Status:        model.BankFundStatusPending,
	}
	if err := s.bankFundRepo.Create(ctx, req); err != nil {
		return nil, fmt.Errorf("create fund request: %w", err)
	}

	uid := userID
	s.writeAudit(ctx, &uid, "fund_from_bank_initiated", "ok", map[string]any{
		"reference":    ref,
		"amount_ngn":   amount.NGN(),
		"bank_account": bankAccountID,
		"bank_name":    ba.BankName,
	})

	if s.monoClient == nil {
		_ = s.bankFundRepo.UpdateStatus(ctx, ref, model.BankFundStatusFailed, "", "mono client not configured")
		s.writeAudit(ctx, &uid, "fund_from_bank_failed", "failed", map[string]any{"reference": ref, "reason": "mono_unavailable"})
		return nil, ErrMonoUnavailable
	}

	user, err := s.userRepo.GetByID(ctx, userID)
	if err != nil || user == nil {
		_ = s.bankFundRepo.UpdateStatus(ctx, ref, model.BankFundStatusFailed, "", "user not found")
		return nil, fmt.Errorf("user not found")
	}
	email := user.Email
	if email == "" {
		email = user.Phone + "@weave.ng"
	}

	payReq := mono.PaymentInitiateRequest{
		Amount:      int64(amount), // already in kobo
		Type:        "onetime-debit",
		Method:      "account",
		Description: fmt.Sprintf("Weave wallet top-up — %s", ref),
		Reference:   ref,
		RedirectURL: "http://localhost:3000/app/accounts",
	}
	payReq.Customer.Name = user.FullName
	payReq.Customer.Email = email

	s.logger.Info("initiating mono payment", "reference", ref, "amount_kobo", int64(amount), "customer_email", email)
	payResp, err := s.monoClient.PaymentInitiate(ctx, payReq)
	if err != nil {
		s.logger.Error("mono payment initiate failed", "reference", ref, "error", err.Error())
		_ = s.bankFundRepo.UpdateStatus(ctx, ref, model.BankFundStatusFailed, "", err.Error())
		s.writeAudit(ctx, &uid, "fund_from_bank_failed", "failed", map[string]any{"reference": ref, "error": err.Error()})
		return nil, fmt.Errorf("%w: %s", ErrDirectDebitFailed, err.Error())
	}
	s.logger.Info("mono payment initiated", "reference", ref, "payment_id", payResp.Data.ID, "mono_url", payResp.Data.MonoURL)

	providerRef := payResp.Data.ID
	paymentURL := payResp.Data.MonoURL
	_ = s.bankFundRepo.UpdateStatus(ctx, ref, model.BankFundStatusPending, providerRef, "")
	req.ProviderRef = providerRef
	req.PaymentURL = paymentURL
	return req, nil
}

// CompleteFundFromBank credits the wallet when Mono fires payment.successful.
// It is idempotent: a completed reference is a no-op.
func (s *WalletService) CompleteFundFromBank(ctx context.Context, reference string, amount model.Amount) error {
	fundReq, err := s.bankFundRepo.GetByReference(ctx, reference)
	if err != nil {
		return fmt.Errorf("get fund request: %w", err)
	}
	if fundReq == nil {
		s.logger.Warn("fund_from_bank webhook for unknown reference", "reference", reference)
		return nil
	}
	if fundReq.Status == model.BankFundStatusCompleted {
		return nil // idempotent
	}

	wallet, err := s.walletRepo.GetByUserID(ctx, fundReq.UserID)
	if err != nil {
		return fmt.Errorf("get wallet: %w", err)
	}

	if err := s.CreditWallet(ctx, wallet.ID, amount, reference, "bank top-up via Mono"); err != nil {
		uid := fundReq.UserID
		s.writeAudit(ctx, &uid, "fund_from_bank_credit_failed", "failed", map[string]any{
			"reference": reference, "error": err.Error(),
		})
		return fmt.Errorf("credit wallet: %w", err)
	}

	if err := s.bankFundRepo.UpdateStatus(ctx, reference, model.BankFundStatusCompleted, "", ""); err != nil {
		s.logger.Error("failed to mark fund request completed", "reference", reference, "error", err)
	}

	uid := fundReq.UserID
	s.writeAudit(ctx, &uid, "fund_from_bank_completed", "ok", map[string]any{
		"reference":  reference,
		"amount_ngn": amount.NGN(),
	})
	return nil
}

func (s *WalletService) writeAudit(ctx context.Context, userID *uuid.UUID, action, status string, meta map[string]any) {
	if s.auditRepo == nil {
		return
	}
	_ = s.auditRepo.Write(ctx, &model.AuditLog{
		UserID:   userID,
		Action:   action,
		Status:   status,
		Metadata: meta,
	})
}

// WalletBalanceProvider is the interface SourcingEngine needs from WalletService.
type WalletBalanceProvider interface {
	GetBalance(ctx context.Context, userID uuid.UUID) (*model.Wallet, error)
}

var _ WalletBalanceProvider = (*WalletService)(nil)

type SourcingEngine struct {
	walletSvc  WalletBalanceProvider
	bankRepo   repository.BankAccountRepository
	auditRepo  *repository.AuditLogRepo
	monoClient *mono.Client
	logger     *slog.Logger
}

func NewSourcingEngine(
	walletSvc WalletBalanceProvider,
	bankRepo repository.BankAccountRepository,
	auditRepo *repository.AuditLogRepo,
	monoClient *mono.Client,
	logger *slog.Logger,
) *SourcingEngine {
	return &SourcingEngine{
		walletSvc:  walletSvc,
		bankRepo:   bankRepo,
		auditRepo:  auditRepo,
		monoClient: monoClient,
		logger:     logger,
	}
}

// refreshBankBalances fetches live Mono balances for all active+verified accounts
// in parallel with a 5s timeout per fetch. On failure it falls back to the stored
// balance and writes an audit warning. Returns a map of bankID → refreshed balance.
func (e *SourcingEngine) refreshBankBalances(ctx context.Context, userID uuid.UUID, accounts []model.BankAccount) map[uuid.UUID]model.Amount {
	result := make(map[uuid.UUID]model.Amount, len(accounts))
	if e.monoClient == nil {
		for _, a := range accounts {
			result[a.ID] = a.LastBalance
		}
		return result
	}

	var (
		mu sync.Mutex
		wg sync.WaitGroup
	)

	for _, acct := range accounts {
		if acct.Provider != "mono" || acct.ProviderToken == "" {
			result[acct.ID] = acct.LastBalance
			continue
		}

		wg.Add(1)
		go func(a model.BankAccount) {
			defer wg.Done()

			fetchCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()

			resp, err := e.monoClient.GetBalance(fetchCtx, a.ProviderToken)
			if err != nil {
				e.logger.Warn("mono balance refresh failed", "bank_id", a.ID, "bank_name", a.BankName, "error", err)
				if e.auditRepo != nil {
					uid := userID
					_ = e.auditRepo.Write(context.Background(), &model.AuditLog{
						UserID: &uid,
						Action: "balance_refresh_failed",
						Status: "failed",
						Metadata: map[string]any{
							"bank_id":   a.ID,
							"bank_name": a.BankName,
							"error":     err.Error(),
						},
					})
				}
				mu.Lock()
				result[a.ID] = a.LastBalance // fall back to stored value
				mu.Unlock()
				return
			}

			fresh := model.Amount(int64(resp.Data.Balance * 100))
			// Update DB in background; don't block the debit plan.
			go func() {
				_ = e.bankRepo.UpdateBalance(context.Background(), a.ID, fresh)
			}()

			mu.Lock()
			result[a.ID] = fresh
			mu.Unlock()
		}(acct)
	}

	wg.Wait()
	return result
}

func (e *SourcingEngine) BuildDebitPlan(ctx context.Context, userID uuid.UUID, amount model.Amount) (*model.DebitPlan, error) {
	plan := &model.DebitPlan{}

	// 1. Check wallet balance first (wallet is always highest priority)
	wallet, err := e.walletSvc.GetBalance(ctx, userID)
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

	// 2. Check linked bank accounts (sorted by priority) with live balances.
	accounts, err := e.bankRepo.GetByUserID(ctx, userID, 100, 0)
	if err != nil {
		return nil, fmt.Errorf("get bank accounts: %w", err)
	}

	// Refresh all balances from Mono in parallel before sourcing.
	liveBalances := e.refreshBankBalances(ctx, userID, accounts)

	for _, acct := range accounts {
		if !acct.IsActive || !acct.IsVerified {
			continue
		}

		balance := liveBalances[acct.ID]
		available := balance - acct.MinBalance
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
		return nil, fmt.Errorf("%w: need %v more across all accounts", ErrInsufficientFunds, remaining.NGN())
	}

	plan.Total = amount
	return plan, nil
}

func generateShortID() string {
	return uuid.New().String()[:8]
}

func splitName(full string) (first, last string) {
	parts := strings.Fields(full)
	if len(parts) == 0 {
		return "User", "Unknown"
	}
	if len(parts) == 1 {
		return parts[0], parts[0]
	}
	return parts[0], strings.Join(parts[1:], " ")
}
