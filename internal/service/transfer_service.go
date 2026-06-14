package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/abdulsalamcodes/weave-server/internal/model"
	"github.com/abdulsalamcodes/weave-server/internal/repository"
)

var (
	ErrTransferNotFound        = errors.New("transfer not found")
	ErrDuplicateTransfer       = errors.New("duplicate transfer request")
	ErrInvalidRecipient        = errors.New("invalid recipient account")
	ErrTransferFailed          = errors.New("transfer failed")
)

type TransferService struct {
	txnRepo       repository.TransactionRepository
	walletRepo    repository.WalletRepository
	walletService *WalletService
	sourcingEng   *SourcingEngine
	payoutService *PayoutService
	pool          *pgxpool.Pool
	logger        *slog.Logger
}

func NewTransferService(
	txnRepo repository.TransactionRepository,
	walletRepo repository.WalletRepository,
	walletService *WalletService,
	sourcingEng *SourcingEngine,
	payoutService *PayoutService,
	pool *pgxpool.Pool,
	logger *slog.Logger,
) *TransferService {
	return &TransferService{
		txnRepo:       txnRepo,
		walletRepo:    walletRepo,
		walletService: walletService,
		sourcingEng:   sourcingEng,
		payoutService: payoutService,
		pool:          pool,
		logger:        logger,
	}
}

type TransferRequest struct {
	Amount           model.Amount
	RecipientAccount string
	RecipientBank    string
	RecipientName    string
	IdempotencyKey   string
}

type TransferResult struct {
	TransactionID uuid.UUID
	OurRef        string
	Status        string
	DebitPlan     *model.DebitPlan
}

func (s *TransferService) InitiateTransfer(ctx context.Context, userID uuid.UUID, req TransferRequest) (res *TransferResult, err error) {
	// Check idempotency
	if req.IdempotencyKey != "" {
		existing, err := s.txnRepo.GetByIdempotencyKey(ctx, req.IdempotencyKey)
		if err != nil {
			return nil, fmt.Errorf("check idempotency: %w", err)
		}
		if existing != nil {
			return &TransferResult{
				TransactionID: existing.ID,
				OurRef:        existing.OurRef,
				Status:        string(existing.Status),
			}, nil
		}
	}

	// Build debit plan
	plan, err := s.sourcingEng.BuildDebitPlan(ctx, userID, req.Amount)
	if err != nil {
		return nil, fmt.Errorf("build debit plan: %w", err)
	}

	if s.pool != nil {
		tx, err := s.pool.Begin(ctx)
		if err != nil {
			return nil, fmt.Errorf("begin transaction: %w", err)
		}
		defer func() {
			if err != nil {
				if rbErr := tx.Rollback(ctx); rbErr != nil {
					s.logger.Error("tx rollback failed", "error", rbErr)
				}
			}
		}()
		ctx = repository.WithTx(ctx, tx)
		defer func() {
			if err == nil {
				if cErr := tx.Commit(ctx); cErr != nil {
					err = fmt.Errorf("commit transaction: %w", cErr)
				}
			}
		}()
	}

	ourRef := fmt.Sprintf("WVF-%s", uuid.New().String()[:8])

	// Create parent transaction
	parentTxn, err := s.txnRepo.Create(ctx, model.CreateTransactionInput{
		UserID:            userID,
		Type:              model.TxnTypeDebitLeg,
		Amount:            req.Amount,
		Fee:               plan.Fees,
		Currency:          "NGN",
		RecipientAccount:  req.RecipientAccount,
		RecipientBankCode: req.RecipientBank,
		RecipientName:     req.RecipientName,
		OurRef:            ourRef,
		IdempotencyKey:    req.IdempotencyKey,
	})
	if err != nil {
		return nil, fmt.Errorf("create parent transaction: %w", err)
	}

	// Process wallet leg (if present)
	for _, leg := range plan.Legs {
		if leg.Source == "wallet" {
			wallet, err := s.walletRepo.GetByUserID(ctx, userID)
			if err != nil {
				return nil, fmt.Errorf("get wallet: %w", err)
			}
			if wallet == nil {
				return nil, ErrWalletNotFound
			}

			legRef := fmt.Sprintf("%s-WL", ourRef)

			// Hold funds
			if err := s.walletRepo.Hold(ctx, wallet.ID, leg.Amount+leg.Fee); err != nil {
				return nil, fmt.Errorf("hold wallet funds: %w", err)
			}

			// Create debit leg transaction
			_, err = s.txnRepo.Create(ctx, model.CreateTransactionInput{
				UserID:   userID,
				ParentID: &parentTxn.ID,
				Type:     model.TxnTypeDebitLeg,
				Amount:   leg.Amount,
				Fee:      leg.Fee,
				Currency: "NGN",
				SourceProvider: "wallet",
				OurRef:   legRef,
			})
			if err != nil {
				if rhErr := s.walletRepo.ReleaseHold(ctx, wallet.ID, leg.Amount+leg.Fee); rhErr != nil {
					s.logger.Error("release hold failed", "error", rhErr, "wallet_id", wallet.ID)
				}
				return nil, fmt.Errorf("create debit leg: %w", err)
			}
		}
	}

	// Mark parent as processing
	if err := s.txnRepo.UpdateStatus(ctx, parentTxn.ID, model.TxnStatusProcessing, ""); err != nil {
		return nil, fmt.Errorf("update status: %w", err)
	}

	// Execute payout leg (via PSP)
	payoutRef := fmt.Sprintf("%s-PO", ourRef)
	payoutTxn, err := s.txnRepo.Create(ctx, model.CreateTransactionInput{
		UserID:            userID,
		ParentID:          &parentTxn.ID,
		Type:              model.TxnTypePayoutLeg,
		Amount:            req.Amount,
		Fee:               0,
		Currency:          "NGN",
		RecipientAccount:  req.RecipientAccount,
		RecipientBankCode: req.RecipientBank,
		RecipientName:     req.RecipientName,
		SourceProvider:    "paystack",
		OurRef:            payoutRef,
	})
	if err != nil {
		return nil, fmt.Errorf("create payout leg: %w", err)
	}

	// Execute real payout via Paystack
	if s.payoutService != nil && s.payoutService.IsEnabled() {
		providerRef, err := s.payoutService.SendPayout(
			ctx, payoutRef, req.Amount,
			req.RecipientAccount, req.RecipientBank, req.RecipientName,
		)
		if err != nil {
			s.logger.Error("payout failed", "error", err, "payout_ref", payoutRef)
			s.txnRepo.UpdateStatus(ctx, payoutTxn.ID, model.TxnStatusFailed, err.Error())
			s.txnRepo.UpdateStatus(ctx, parentTxn.ID, model.TxnStatusFailed, "payout failed")

			// Release holds (within tx — rolled back anyway, but keeps data consistent)
			for _, leg := range plan.Legs {
				if leg.Source == "wallet" {
					if wallet, _ := s.walletRepo.GetByUserID(ctx, userID); wallet != nil {
						if rhErr := s.walletRepo.ReleaseHold(ctx, wallet.ID, leg.Amount+leg.Fee); rhErr != nil {
							s.logger.Error("release hold on payout failure", "error", rhErr, "wallet_id", wallet.ID)
						}
					}
				}
			}

			return nil, fmt.Errorf("payout failed: %w", err)
		}
		s.txnRepo.UpdateProviderRef(ctx, payoutTxn.ID, providerRef)
	} else {
		// Fallback: mark as completed (no Paystack configured)
		s.logger.Warn("payout service not configured, marking as completed",
			"payout_ref", payoutRef,
		)
		s.txnRepo.UpdateProviderRef(ctx, payoutTxn.ID, "simulated-"+payoutRef)
	}

	// Complete wallet debit (release hold and deduct)
	for _, leg := range plan.Legs {
		if leg.Source == "wallet" {
			wallet, _ := s.walletRepo.GetByUserID(ctx, userID)
			if wallet != nil {
				s.walletRepo.Debit(ctx, wallet.ID, leg.Amount+leg.Fee)
			}
		}
	}

	// Mark parent completed
	if err := s.txnRepo.UpdateStatus(ctx, parentTxn.ID, model.TxnStatusCompleted, ""); err != nil {
		return nil, fmt.Errorf("complete transaction: %w", err)
	}
	if err := s.txnRepo.UpdateStatus(ctx, payoutTxn.ID, model.TxnStatusCompleted, ""); err != nil {
		return nil, fmt.Errorf("complete payout: %w", err)
	}

	s.logger.Info("transfer completed",
		"transaction_id", parentTxn.ID,
		"our_ref", ourRef,
		"amount", req.Amount,
		"recipient", req.RecipientAccount,
	)

	return &TransferResult{
		TransactionID: parentTxn.ID,
		OurRef:        ourRef,
		Status:        "COMPLETED",
		DebitPlan:     plan,
	}, nil
}

func (s *TransferService) GetTransfer(ctx context.Context, txnID uuid.UUID) (*model.Transaction, error) {
	txn, err := s.txnRepo.GetByID(ctx, txnID)
	if err != nil {
		return nil, fmt.Errorf("get transfer: %w", err)
	}
	if txn == nil {
		return nil, ErrTransferNotFound
	}
	return txn, nil
}

func (s *TransferService) GetTransferByRef(ctx context.Context, ourRef string) (*model.Transaction, error) {
	txn, err := s.txnRepo.GetByOurRef(ctx, ourRef)
	if err != nil {
		return nil, fmt.Errorf("get transfer by ref: %w", err)
	}
	if txn == nil {
		return nil, ErrTransferNotFound
	}
	return txn, nil
}
