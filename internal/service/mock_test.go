package service

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/abdulsalamcodes/weave-server/internal/model"
	"github.com/abdulsalamcodes/weave-server/internal/repository"
)

var _ repository.UserRepository = (*mockUserRepo)(nil)

type mockUserRepo struct {
	mu          sync.Mutex
	users       map[uuid.UUID]*model.User
	byPhone     map[string]*model.User
	pinAttempts []pinAttempt
}

type pinAttempt struct {
	userID  uuid.UUID
	success bool
	time    time.Time
	ip      string
}

func newMockUserRepo() *mockUserRepo {
	return &mockUserRepo{
		users:   make(map[uuid.UUID]*model.User),
		byPhone: make(map[string]*model.User),
	}
}

func (m *mockUserRepo) Create(_ context.Context, input model.CreateUserInput) (*model.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	u := &model.User{
		ID:       uuid.New(),
		Phone:    input.Phone,
		FullName: input.FullName,
		PINHash:  input.PIN,
		KYCLevel: model.KYCLevelBasic,
	}
	m.users[u.ID] = u
	m.byPhone[u.Phone] = u
	return u, nil
}

func (m *mockUserRepo) GetByID(_ context.Context, id uuid.UUID) (*model.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.users[id]
	if !ok {
		return nil, nil
	}
	return u, nil
}

func (m *mockUserRepo) GetByPhone(_ context.Context, phone string) (*model.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.byPhone[phone]
	if !ok {
		return nil, nil
	}
	return u, nil
}

func (m *mockUserRepo) RecordPINAttempt(_ context.Context, userID uuid.UUID, success bool, ip string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pinAttempts = append(m.pinAttempts, pinAttempt{userID: userID, success: success, time: time.Now(), ip: ip})
	return nil
}

func (m *mockUserRepo) RecentFailedPINAttempts(_ context.Context, userID uuid.UUID, within time.Duration) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	count := 0
	cutoff := time.Now().Add(-within)
	for _, a := range m.pinAttempts {
		if a.userID == userID && !a.success && a.time.After(cutoff) {
			count++
		}
	}
	return count, nil
}

func (m *mockUserRepo) RecentFailedPINAttemptsByIP(_ context.Context, ip string, within time.Duration) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	count := 0
	cutoff := time.Now().Add(-within)
	for _, a := range m.pinAttempts {
		if a.ip == ip && !a.success && a.time.After(cutoff) {
			count++
		}
	}
	return count, nil
}

func (m *mockUserRepo) UpdatePIN(_ context.Context, userID uuid.UUID, pinHash string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if u, ok := m.users[userID]; ok {
		u.PINHash = pinHash
	}
	return nil
}

func (m *mockUserRepo) UpdateKYC(_ context.Context, _ uuid.UUID, _ model.KYCLevel, _, _ string) error {
	return nil
}

var _ repository.WalletRepository = (*mockWalletRepo)(nil)

type mockWalletRepo struct {
	mu       sync.Mutex
	wallets  map[uuid.UUID]*model.Wallet
	byUser   map[uuid.UUID]*model.Wallet
	txns     []model.WalletTransaction
	accounts []model.WalletAccount
	deposits []model.WalletDeposit
}

func newMockWalletRepo() *mockWalletRepo {
	return &mockWalletRepo{
		wallets: make(map[uuid.UUID]*model.Wallet),
		byUser:  make(map[uuid.UUID]*model.Wallet),
	}
}

func (m *mockWalletRepo) Create(_ context.Context, wallet *model.Wallet) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if wallet.ID == uuid.Nil {
		wallet.ID = uuid.New()
	}
	m.wallets[wallet.ID] = wallet
	m.byUser[wallet.UserID] = wallet
	return nil
}

func (m *mockWalletRepo) GetByUserID(_ context.Context, userID uuid.UUID) (*model.Wallet, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	w, ok := m.byUser[userID]
	if !ok {
		return nil, nil
	}
	return w, nil
}

func (m *mockWalletRepo) GetByID(_ context.Context, id uuid.UUID) (*model.Wallet, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	w, ok := m.wallets[id]
	if !ok {
		return nil, nil
	}
	return w, nil
}

func (m *mockWalletRepo) Credit(_ context.Context, walletID uuid.UUID, amount model.Amount) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if w, ok := m.wallets[walletID]; ok {
		w.Balance += amount
		w.LedgerBalance += amount
	}
	return nil
}

func (m *mockWalletRepo) Debit(_ context.Context, walletID uuid.UUID, amount model.Amount) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if w, ok := m.wallets[walletID]; ok {
		if w.Balance < amount || w.LedgerBalance < amount {
			return fmt.Errorf("insufficient balance")
		}
		w.Balance -= amount
		w.LedgerBalance -= amount
	}
	return nil
}

func (m *mockWalletRepo) Hold(_ context.Context, walletID uuid.UUID, amount model.Amount) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if w, ok := m.wallets[walletID]; ok {
		if w.LedgerBalance < amount {
			return fmt.Errorf("insufficient available balance")
		}
		w.LedgerBalance -= amount
	}
	return nil
}

func (m *mockWalletRepo) ReleaseHold(_ context.Context, walletID uuid.UUID, amount model.Amount) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if w, ok := m.wallets[walletID]; ok {
		w.LedgerBalance += amount
	}
	return nil
}

func (m *mockWalletRepo) RecordTransaction(_ context.Context, wt *model.WalletTransaction) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if wt.ID == uuid.Nil {
		wt.ID = uuid.New()
	}
	m.txns = append(m.txns, *wt)
	return nil
}

func (m *mockWalletRepo) CreateWalletAccount(_ context.Context, wa *model.WalletAccount) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if wa.ID == uuid.Nil {
		wa.ID = uuid.New()
	}
	m.accounts = append(m.accounts, *wa)
	return nil
}

func (m *mockWalletRepo) GetWalletAccountByUserID(_ context.Context, userID uuid.UUID) (*model.WalletAccount, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, a := range m.accounts {
		if a.UserID == userID && a.IsActive {
			return &a, nil
		}
	}
	return nil, nil
}

func (m *mockWalletRepo) GetWalletAccountByNumber(_ context.Context, number string) (*model.WalletAccount, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, a := range m.accounts {
		if a.AccountNumber == number && a.IsActive {
			return &a, nil
		}
	}
	return nil, nil
}

func (m *mockWalletRepo) RecordDeposit(_ context.Context, deposit *model.WalletDeposit) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if deposit.ID == uuid.Nil {
		deposit.ID = uuid.New()
	}
	m.deposits = append(m.deposits, *deposit)
	return nil
}

var _ repository.TransactionRepository = (*mockTxnRepo)(nil)

type mockTxnRepo struct {
	mu    sync.Mutex
	txns  map[uuid.UUID]*model.Transaction
	byRef map[string]*model.Transaction
	byKey map[string]*model.Transaction
}

func newMockTxnRepo() *mockTxnRepo {
	return &mockTxnRepo{
		txns:  make(map[uuid.UUID]*model.Transaction),
		byRef: make(map[string]*model.Transaction),
		byKey: make(map[string]*model.Transaction),
	}
}

func (m *mockTxnRepo) Create(_ context.Context, input model.CreateTransactionInput) (*model.Transaction, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t := &model.Transaction{
		ID:                uuid.New(),
		UserID:            input.UserID,
		ParentID:          input.ParentID,
		Type:              input.Type,
		Amount:            input.Amount,
		Fee:               input.Fee,
		Currency:          input.Currency,
		Status:            model.TxnStatusPending,
		SourceAccountID:   input.SourceAccountID,
		SourceProvider:    input.SourceProvider,
		RecipientAccount:  input.RecipientAccount,
		RecipientBankCode: input.RecipientBankCode,
		RecipientName:     input.RecipientName,
		OurRef:            input.OurRef,
		IdempotencyKey:    input.IdempotencyKey,
		CreatedAt:         time.Now(),
	}
	m.txns[t.ID] = t
	m.byRef[t.OurRef] = t
	if t.IdempotencyKey != "" {
		m.byKey[t.IdempotencyKey] = t
	}
	return t, nil
}

func (m *mockTxnRepo) GetByID(_ context.Context, id uuid.UUID) (*model.Transaction, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.txns[id]
	if !ok {
		return nil, nil
	}
	return t, nil
}

func (m *mockTxnRepo) GetByOurRef(_ context.Context, ourRef string) (*model.Transaction, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.byRef[ourRef]
	if !ok {
		return nil, nil
	}
	return t, nil
}

func (m *mockTxnRepo) GetByParentID(_ context.Context, parentID uuid.UUID) ([]model.Transaction, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var children []model.Transaction
	for _, t := range m.txns {
		if t.ParentID != nil && *t.ParentID == parentID {
			children = append(children, *t)
		}
	}
	return children, nil
}

func (m *mockTxnRepo) UpdateStatus(_ context.Context, id uuid.UUID, status model.TransactionStatus, failureReason string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if t, ok := m.txns[id]; ok {
		t.Status = status
		t.FailureReason = failureReason
		if status == model.TxnStatusCompleted || status == model.TxnStatusFailed {
			now := time.Now()
			t.CompletedAt = &now
		}
	}
	return nil
}

func (m *mockTxnRepo) UpdateProviderRef(_ context.Context, id uuid.UUID, providerRef string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if t, ok := m.txns[id]; ok {
		t.ProviderRef = providerRef
	}
	return nil
}

func (m *mockTxnRepo) GetByIdempotencyKey(_ context.Context, key string) (*model.Transaction, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.byKey[key]
	if !ok {
		return nil, nil
	}
	return t, nil
}

var _ repository.BankAccountRepository = (*mockBankRepo)(nil)

type mockBankRepo struct {
	mu       sync.Mutex
	accounts map[uuid.UUID]*model.BankAccount
}

func newMockBankRepo() *mockBankRepo {
	return &mockBankRepo{
		accounts: make(map[uuid.UUID]*model.BankAccount),
	}
}

func (m *mockBankRepo) Create(_ context.Context, ba *model.BankAccount) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if ba.ID == uuid.Nil {
		ba.ID = uuid.New()
	}
	m.accounts[ba.ID] = ba
	return nil
}

func (m *mockBankRepo) GetByUserID(_ context.Context, userID uuid.UUID, limit, offset int) ([]model.BankAccount, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var result []model.BankAccount
	for _, a := range m.accounts {
		if a.UserID == userID && a.IsActive {
			result = append(result, *a)
		}
	}
	if offset >= len(result) {
		return nil, nil
	}
	result = result[offset:]
	if limit > 0 && limit < len(result) {
		result = result[:limit]
	}
	return result, nil
}

func (m *mockBankRepo) GetByID(_ context.Context, id uuid.UUID) (*model.BankAccount, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.accounts[id]
	if !ok {
		return nil, nil
	}
	return a, nil
}

func (m *mockBankRepo) UpdatePriority(_ context.Context, id uuid.UUID, priority int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if a, ok := m.accounts[id]; ok {
		a.Priority = priority
	}
	return nil
}

func (m *mockBankRepo) UpdateBalance(_ context.Context, id uuid.UUID, balance model.Amount) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if a, ok := m.accounts[id]; ok {
		a.LastBalance = balance
	}
	return nil
}

func (m *mockBankRepo) Delete(_ context.Context, id uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.accounts, id)
	return nil
}
