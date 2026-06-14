package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/abdulsalamcodes/weave-server/internal/middleware"
	"github.com/abdulsalamcodes/weave-server/internal/model"
	"github.com/abdulsalamcodes/weave-server/internal/repository"
	"github.com/abdulsalamcodes/weave-server/internal/service"
)

func newTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
}

type testUserRepo struct {
	mu     sync.Mutex
	users  map[uuid.UUID]*model.User
	phones map[string]*model.User
}

func newTestUserRepo() *testUserRepo {
	return &testUserRepo{users: make(map[uuid.UUID]*model.User), phones: make(map[string]*model.User)}
}

func (r *testUserRepo) Create(_ context.Context, input model.CreateUserInput) (*model.User, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	u := &model.User{ID: uuid.New(), Phone: input.Phone, FullName: input.FullName, PINHash: input.PIN}
	r.users[u.ID] = u
	r.phones[u.Phone] = u
	return u, nil
}

func (r *testUserRepo) GetByID(_ context.Context, id uuid.UUID) (*model.User, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	u, ok := r.users[id]
	if !ok {
		return nil, nil
	}
	return u, nil
}

func (r *testUserRepo) GetByPhone(_ context.Context, phone string) (*model.User, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	u, ok := r.phones[phone]
	if !ok {
		return nil, nil
	}
	return u, nil
}

func (r *testUserRepo) RecordPINAttempt(_ context.Context, _ uuid.UUID, _ bool, _ string) error { return nil }

func (r *testUserRepo) RecentFailedPINAttempts(_ context.Context, _ uuid.UUID, _ time.Duration) (int, error) {
	return 0, nil
}

func (r *testUserRepo) RecentFailedPINAttemptsByIP(_ context.Context, _ string, _ time.Duration) (int, error) {
	return 0, nil
}

func (r *testUserRepo) UpdatePIN(_ context.Context, _ uuid.UUID, _ string) error { return nil }

func (r *testUserRepo) UpdateKYC(_ context.Context, _ uuid.UUID, _ model.KYCLevel, _, _ string) error {
	return nil
}

var _ repository.UserRepository = (*testUserRepo)(nil)

type testWalletRepo struct {
	mu      sync.Mutex
	wallets map[uuid.UUID]*model.Wallet
}

func newTestWalletRepo() *testWalletRepo {
	return &testWalletRepo{wallets: make(map[uuid.UUID]*model.Wallet)}
}

func (r *testWalletRepo) Create(_ context.Context, w *model.Wallet) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	w.ID = uuid.New()
	r.wallets[w.UserID] = w
	return nil
}

func (r *testWalletRepo) GetByUserID(_ context.Context, userID uuid.UUID) (*model.Wallet, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	w, ok := r.wallets[userID]
	if !ok {
		return nil, nil
	}
	return w, nil
}

func (r *testWalletRepo) GetByID(_ context.Context, id uuid.UUID) (*model.Wallet, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, w := range r.wallets {
		if w.ID == id {
			return w, nil
		}
	}
	return nil, nil
}

func (r *testWalletRepo) Credit(_ context.Context, walletID uuid.UUID, amount model.Amount) error  { return nil }
func (r *testWalletRepo) Debit(_ context.Context, walletID uuid.UUID, amount model.Amount) error    { return nil }
func (r *testWalletRepo) Hold(_ context.Context, walletID uuid.UUID, amount model.Amount) error     { return nil }
func (r *testWalletRepo) ReleaseHold(_ context.Context, walletID uuid.UUID, amount model.Amount) error { return nil }
func (r *testWalletRepo) RecordTransaction(_ context.Context, _ *model.WalletTransaction) error  { return nil }
func (r *testWalletRepo) CreateWalletAccount(_ context.Context, _ *model.WalletAccount) error     { return nil }
func (r *testWalletRepo) GetWalletAccountByUserID(_ context.Context, _ uuid.UUID) (*model.WalletAccount, error) {
	return nil, nil
}
func (r *testWalletRepo) GetWalletAccountByNumber(_ context.Context, _ string) (*model.WalletAccount, error) {
	return nil, nil
}
func (r *testWalletRepo) RecordDeposit(_ context.Context, _ *model.WalletDeposit) error { return nil }

var _ repository.WalletRepository = (*testWalletRepo)(nil)

type testTxnRepo struct {
	mu   sync.Mutex
	txns map[uuid.UUID]*model.Transaction
}

func newTestTxnRepo() *testTxnRepo {
	return &testTxnRepo{txns: make(map[uuid.UUID]*model.Transaction)}
}

func (r *testTxnRepo) Create(_ context.Context, input model.CreateTransactionInput) (*model.Transaction, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	t := &model.Transaction{
		ID:               uuid.New(),
		UserID:           input.UserID,
		ParentID:         input.ParentID,
		Type:             input.Type,
		Amount:           input.Amount,
		Fee:              input.Fee,
		Currency:         input.Currency,
		Status:           model.TxnStatusPending,
		SourceProvider:   input.SourceProvider,
		RecipientAccount: input.RecipientAccount,
		RecipientBankCode: input.RecipientBankCode,
		RecipientName:    input.RecipientName,
		OurRef:           input.OurRef,
		IdempotencyKey:   input.IdempotencyKey,
	}
	r.txns[t.ID] = t
	return t, nil
}

func (r *testTxnRepo) GetByID(_ context.Context, id uuid.UUID) (*model.Transaction, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	t, ok := r.txns[id]
	if !ok {
		return nil, nil
	}
	return t, nil
}

func (r *testTxnRepo) GetByOurRef(_ context.Context, ref string) (*model.Transaction, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, t := range r.txns {
		if t.OurRef == ref {
			return t, nil
		}
	}
	return nil, nil
}

func (r *testTxnRepo) GetByParentID(_ context.Context, parentID uuid.UUID) ([]model.Transaction, error) {
	return nil, nil
}

func (r *testTxnRepo) UpdateStatus(_ context.Context, id uuid.UUID, status model.TransactionStatus, reason string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if t, ok := r.txns[id]; ok {
		t.Status = status
		t.FailureReason = reason
	}
	return nil
}

func (r *testTxnRepo) UpdateProviderRef(_ context.Context, id uuid.UUID, providerRef string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if t, ok := r.txns[id]; ok {
		t.ProviderRef = providerRef
	}
	return nil
}

func (r *testTxnRepo) GetByIdempotencyKey(_ context.Context, key string) (*model.Transaction, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, t := range r.txns {
		if t.IdempotencyKey == key {
			return t, nil
		}
	}
	return nil, nil
}

func (r *testTxnRepo) ListByUserID(_ context.Context, userID uuid.UUID, filter repository.TransactionFilter) ([]model.Transaction, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var result []model.Transaction
	for _, t := range r.txns {
		if t.UserID != userID {
			continue
		}
		result = append(result, *t)
	}
	if filter.Offset > 0 && filter.Offset < len(result) {
		result = result[filter.Offset:]
	}
	if filter.Limit > 0 && filter.Limit < len(result) {
		result = result[:filter.Limit]
	}
	return result, nil
}

func (r *testTxnRepo) CountByUserID(_ context.Context, userID uuid.UUID, _ repository.TransactionFilter) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	count := 0
	for _, t := range r.txns {
		if t.UserID == userID {
			count++
		}
	}
	return count, nil
}

var _ repository.TransactionRepository = (*testTxnRepo)(nil)

type testBankRepo struct {
	mu       sync.Mutex
	accounts []model.BankAccount
}

func newTestBankRepo() *testBankRepo {
	return &testBankRepo{}
}

func (r *testBankRepo) Create(_ context.Context, _ *model.BankAccount) error { return nil }
func (r *testBankRepo) GetByUserID(_ context.Context, _ uuid.UUID, _, _ int) ([]model.BankAccount, error) {
	return nil, nil
}
func (r *testBankRepo) GetByID(_ context.Context, _ uuid.UUID) (*model.BankAccount, error) { return nil, nil }
func (r *testBankRepo) UpdatePriority(_ context.Context, _ uuid.UUID, _ int) error { return nil }
func (r *testBankRepo) UpdateBalance(_ context.Context, _ uuid.UUID, _ model.Amount) error { return nil }
func (r *testBankRepo) Delete(_ context.Context, _ uuid.UUID) error { return nil }

var _ repository.BankAccountRepository = (*testBankRepo)(nil)

func TestTransferHandler_Integration(t *testing.T) {
	userRepo := newTestUserRepo()
	walletRepo := newTestWalletRepo()
	txnRepo := newTestTxnRepo()
	bankRepo := newTestBankRepo()

	user, _ := userRepo.Create(context.Background(), model.CreateUserInput{
		Phone:    "+2349000000001",
		FullName: "Integration Test",
		PIN:      "hash",
	})
	wallet := &model.Wallet{UserID: user.ID, Balance: 10_000_000, LedgerBalance: 10_000_000, Currency: "NGN"}
	walletRepo.Create(context.Background(), wallet)

	walletSvc := service.NewWalletService(walletRepo, userRepo, nil, newTestLogger())
	engine := service.NewSourcingEngine(walletSvc, bankRepo, newTestLogger())
	payoutSvc := service.NewPayoutService(nil, newTestLogger())
	transferSvc := service.NewTransferService(txnRepo, walletRepo, bankRepo, walletSvc, engine, payoutSvc, nil, nil, nil, newTestLogger())

	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := context.WithValue(r.Context(), middleware.UserIDKey, user.ID)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	})
	handler := NewTransferHandler(transferSvc, txnRepo, newTestLogger())
	handler.RegisterRoutes(r)

	server := httptest.NewServer(r)
	defer server.Close()

	t.Run("successful transfer", func(t *testing.T) {
		body := `{"amount":50000,"recipient_account":"0123456789","recipient_bank":"058","recipient_name":"Jane Doe"}`
		resp, err := http.Post(server.URL+"/transfers", "application/json", bytes.NewReader([]byte(body)))
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		defer resp.Body.Close()

		respBody, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("expected 201 Created, got %d, body: %s", resp.StatusCode, string(respBody))
		}

		var result map[string]interface{}
		if err := json.Unmarshal(respBody, &result); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if result["status"] != "COMPLETED" {
			t.Errorf("expected COMPLETED, got %v", result["status"])
		}
		if result["transaction_id"] == "" {
			t.Error("expected transaction_id")
		}
	})

	t.Run("insufficient funds returns 400", func(t *testing.T) {
		body := `{"amount":99999999,"recipient_account":"0123456789","recipient_bank":"058","recipient_name":"Jane Doe"}`
		resp, err := http.Post(server.URL+"/transfers", "application/json", bytes.NewReader([]byte(body)))
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusBadRequest {
			respBody, _ := io.ReadAll(resp.Body)
			t.Fatalf("expected 400, got %d, body: %s", resp.StatusCode, string(respBody))
		}
	})
}
