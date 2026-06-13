package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"

	"github.com/abdulsalamcodes/weave-server/internal/config"
	"github.com/abdulsalamcodes/weave-server/internal/middleware"
	"github.com/abdulsalamcodes/weave-server/internal/model"
	"github.com/abdulsalamcodes/weave-server/internal/repository"
)

var (
	ErrUserNotFound      = errors.New("user not found")
	ErrInvalidPIN        = errors.New("invalid pin")
	ErrPINLocked         = errors.New("pin locked due to too many attempts")
	ErrDuplicatePhone    = errors.New("phone number already registered")
)

type AuthService struct {
	userRepo  *repository.UserRepo
	walletRepo *repository.WalletRepo
	cfg       config.AuthConfig
	jwtSecret string
	accessTTL time.Duration
	refreshTTL time.Duration
	logger    *slog.Logger
}

func NewAuthService(
	userRepo *repository.UserRepo,
	walletRepo *repository.WalletRepo,
	cfg config.AuthConfig,
	jwtSecret string,
	accessTTL, refreshTTL time.Duration,
	logger *slog.Logger,
) *AuthService {
	return &AuthService{
		userRepo:   userRepo,
		walletRepo: walletRepo,
		cfg:        cfg,
		jwtSecret:  jwtSecret,
		accessTTL:  accessTTL,
		refreshTTL: refreshTTL,
		logger:     logger,
	}
}

func (s *AuthService) Register(ctx context.Context, phone, fullName, pin string) (*model.UserSession, error) {
	existing, err := s.userRepo.GetByPhone(ctx, phone)
	if err != nil {
		return nil, fmt.Errorf("check existing user: %w", err)
	}
	if existing != nil {
		return nil, ErrDuplicatePhone
	}

	pinHash, err := bcrypt.GenerateFromPassword([]byte(pin), s.cfg.BcryptCost)
	if err != nil {
		return nil, fmt.Errorf("hash pin: %w", err)
	}

	user, err := s.userRepo.Create(ctx, model.CreateUserInput{
		Phone:    phone,
		FullName: fullName,
		PIN:      string(pinHash),
	})
	if err != nil {
		return nil, fmt.Errorf("create user: %w", err)
	}

	// Create wallet for user
	wallet := &model.Wallet{
		UserID:        user.ID,
		Type:          model.WalletTypeUser,
		Balance:       0,
		LedgerBalance: 0,
		Currency:      "NGN",
	}
	if err := s.walletRepo.Create(ctx, wallet); err != nil {
		return nil, fmt.Errorf("create wallet: %w", err)
	}

	session, err := s.generateSession(ctx, user.ID)
	if err != nil {
		return nil, fmt.Errorf("generate session: %w", err)
	}

	s.logger.Info("user registered", "user_id", user.ID, "phone", phone)
	return session, nil
}

func (s *AuthService) Login(ctx context.Context, phone, pin string) (*model.UserSession, error) {
	user, err := s.userRepo.GetByPhone(ctx, phone)
	if err != nil {
		return nil, fmt.Errorf("get user: %w", err)
	}
	if user == nil {
		return nil, ErrUserNotFound
	}

	// Check PIN lockout
	failedAttempts, err := s.userRepo.RecentFailedPINAttempts(ctx, user.ID, time.Duration(s.cfg.PINLockoutMins)*time.Minute)
	if err != nil {
		return nil, fmt.Errorf("check pin attempts: %w", err)
	}
	if failedAttempts >= s.cfg.MaxPINAttempts {
		return nil, ErrPINLocked
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.PINHash), []byte(pin)); err != nil {
		s.userRepo.RecordPINAttempt(ctx, user.ID, false, getIP(ctx))
		return nil, ErrInvalidPIN
	}

	s.userRepo.RecordPINAttempt(ctx, user.ID, true, getIP(ctx))

	session, err := s.generateSession(ctx, user.ID)
	if err != nil {
		return nil, fmt.Errorf("generate session: %w", err)
	}

	s.logger.Info("user logged in", "user_id", user.ID)
	return session, nil
}

func (s *AuthService) VerifyPIN(ctx context.Context, userID uuid.UUID, pin string) error {
	user, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return fmt.Errorf("get user: %w", err)
	}
	if user == nil {
		return ErrUserNotFound
	}

	failedAttempts, err := s.userRepo.RecentFailedPINAttempts(ctx, userID, time.Duration(s.cfg.PINLockoutMins)*time.Minute)
	if err != nil {
		return fmt.Errorf("check pin attempts: %w", err)
	}
	if failedAttempts >= s.cfg.MaxPINAttempts {
		return ErrPINLocked
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.PINHash), []byte(pin)); err != nil {
		s.userRepo.RecordPINAttempt(ctx, userID, false, getIP(ctx))
		return ErrInvalidPIN
	}

	s.userRepo.RecordPINAttempt(ctx, userID, true, getIP(ctx))
	return nil
}

func (s *AuthService) RefreshToken(ctx context.Context, refreshToken string) (*model.UserSession, error) {
	claims := &jwtValidatorClaims{}
	token, err := jwt.ParseWithClaims(refreshToken, claims, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, jwt.ErrSignatureInvalid
		}
		return []byte(s.jwtSecret), nil
	})
	if err != nil || !token.Valid {
		return nil, errors.New("invalid refresh token")
	}
	if claims.Scope != "refresh" {
		return nil, errors.New("invalid token scope")
	}

	userID, err := uuid.Parse(claims.Subject)
	if err != nil {
		return nil, errors.New("invalid token subject")
	}

	return s.generateSession(ctx, userID)
}

type jwtValidatorClaims struct {
	Scope string `json:"scope,omitempty"`
	jwt.RegisteredClaims
}

func (s *AuthService) ChangePIN(ctx context.Context, userID uuid.UUID, oldPIN, newPIN string) error {
	user, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return fmt.Errorf("get user: %w", err)
	}
	if user == nil {
		return ErrUserNotFound
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.PINHash), []byte(oldPIN)); err != nil {
		return ErrInvalidPIN
	}

	pinHash, err := bcrypt.GenerateFromPassword([]byte(newPIN), s.cfg.BcryptCost)
	if err != nil {
		return fmt.Errorf("hash pin: %w", err)
	}

	return s.userRepo.UpdatePIN(ctx, userID, string(pinHash))
}

func (s *AuthService) generateSession(ctx context.Context, userID uuid.UUID) (*model.UserSession, error) {
	accessToken, err := middleware.GenerateAccessToken(s.jwtSecret, userID, s.accessTTL)
	if err != nil {
		return nil, fmt.Errorf("generate access token: %w", err)
	}

	refreshToken, err := middleware.GenerateRefreshToken(s.jwtSecret, userID, s.refreshTTL)
	if err != nil {
		return nil, fmt.Errorf("generate refresh token: %w", err)
	}

	return &model.UserSession{
		UserID:       userID,
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		ExpiresAt:    time.Now().Add(s.accessTTL),
	}, nil
}

func getIP(ctx context.Context) string {
	return ""
}


