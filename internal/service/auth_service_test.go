package service

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/abdulsalamcodes/weave-server/internal/config"
)

func newTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
}

func TestAuthService_Register(t *testing.T) {
	userRepo := newMockUserRepo()
	walletRepo := newMockWalletRepo()
	log := newTestLogger()

	cfg := config.AuthConfig{
		BcryptCost:     4, // low cost for fast tests
		MaxPINAttempts: 3,
		PINLockoutMins: 15,
	}

	svc := NewAuthService(userRepo, walletRepo, cfg, "test-secret", 15*time.Minute, 7*24*time.Hour, nil, log)

	t.Run("successful registration", func(t *testing.T) {
		session, err := svc.Register(context.Background(), "+2348012345678", "Test User", "123456")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if session == nil {
			t.Fatal("expected session, got nil")
		}
		if session.AccessToken == "" {
			t.Error("expected access token")
		}
		if session.RefreshToken == "" {
			t.Error("expected refresh token")
		}
	})

	t.Run("duplicate phone", func(t *testing.T) {
		_, err := svc.Register(context.Background(), "+2348012345678", "Another", "654321")
		if err != ErrDuplicatePhone {
			t.Errorf("expected ErrDuplicatePhone, got %v", err)
		}
	})
}

func TestAuthService_Login(t *testing.T) {
	userRepo := newMockUserRepo()
	walletRepo := newMockWalletRepo()
	log := newTestLogger()

	cfg := config.AuthConfig{
		BcryptCost:     4,
		MaxPINAttempts: 3,
		PINLockoutMins: 15,
	}

	svc := NewAuthService(userRepo, walletRepo, cfg, "test-secret", 15*time.Minute, 7*24*time.Hour, nil, log)

	phone := "+2348011111111"
	svc.Register(context.Background(), phone, "Login Test", "999999")

	t.Run("successful login", func(t *testing.T) {
		session, err := svc.Login(context.Background(), phone, "999999")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if session == nil {
			t.Fatal("expected session, got nil")
		}
		if session.AccessToken == "" {
			t.Error("expected access token")
		}
	})

	t.Run("wrong phone", func(t *testing.T) {
		_, err := svc.Login(context.Background(), "+2348000000000", "999999")
		if err != ErrUserNotFound {
			t.Errorf("expected ErrUserNotFound, got %v", err)
		}
	})

	t.Run("wrong pin", func(t *testing.T) {
		_, err := svc.Login(context.Background(), phone, "000000")
		if err != ErrInvalidPIN {
			t.Errorf("expected ErrInvalidPIN, got %v", err)
		}
	})
}

func TestAuthService_PINLockout(t *testing.T) {
	userRepo := newMockUserRepo()
	walletRepo := newMockWalletRepo()
	log := newTestLogger()

	cfg := config.AuthConfig{
		BcryptCost:     4,
		MaxPINAttempts: 2, // lockout after 2 failures
		PINLockoutMins: 15,
	}

	svc := NewAuthService(userRepo, walletRepo, cfg, "test-secret", 15*time.Minute, 7*24*time.Hour, nil, log)
	phone := "+2348022222222"
	svc.Register(context.Background(), phone, "Lockout Test", "123456")

	// Two wrong attempts
	svc.Login(context.Background(), phone, "000000")
	svc.Login(context.Background(), phone, "000000")

	t.Run("locked after max attempts", func(t *testing.T) {
		_, err := svc.Login(context.Background(), phone, "123456")
		if err != ErrPINLocked {
			t.Errorf("expected ErrPINLocked, got %v", err)
		}
	})
}

func TestAuthService_VerifyPIN(t *testing.T) {
	userRepo := newMockUserRepo()
	walletRepo := newMockWalletRepo()
	log := newTestLogger()

	svc := NewAuthService(userRepo, walletRepo, config.AuthConfig{
		BcryptCost:     4,
		MaxPINAttempts: 3,
		PINLockoutMins: 15,
	}, "test-secret", 15*time.Minute, 7*24*time.Hour, nil, log)

	user, _ := svc.Register(context.Background(), "+2348033333333", "Verify Test", "555555")
	userID := user.UserID

	t.Run("correct pin", func(t *testing.T) {
		if err := svc.VerifyPIN(context.Background(), userID, "555555"); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("wrong pin", func(t *testing.T) {
		if err := svc.VerifyPIN(context.Background(), userID, "000000"); err != ErrInvalidPIN {
			t.Errorf("expected ErrInvalidPIN, got %v", err)
		}
	})
}

func TestAuthService_ChangePIN(t *testing.T) {
	userRepo := newMockUserRepo()
	walletRepo := newMockWalletRepo()
	log := newTestLogger()

	svc := NewAuthService(userRepo, walletRepo, config.AuthConfig{
		BcryptCost:     4,
		MaxPINAttempts: 3,
		PINLockoutMins: 15,
	}, "test-secret", 15*time.Minute, 7*24*time.Hour, nil, log)
	phone := "+2348044444444"
	user, _ := svc.Register(context.Background(), phone, "Change PIN Test", "123456")

	t.Run("successful pin change", func(t *testing.T) {
		if err := svc.ChangePIN(context.Background(), user.UserID, "123456", "654321"); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// Verify new PIN works for login
		session, err := svc.Login(context.Background(), phone, "654321")
		if err != nil {
			t.Errorf("login with new pin failed: %v", err)
		}
		if session == nil {
			t.Error("expected session")
		}
	})

	t.Run("wrong old pin", func(t *testing.T) {
		if err := svc.ChangePIN(context.Background(), user.UserID, "wrong-pin", "000000"); err != ErrInvalidPIN {
			t.Errorf("expected ErrInvalidPIN, got %v", err)
		}
	})

	t.Run("user not found", func(t *testing.T) {
		if err := svc.ChangePIN(context.Background(), uuid.New(), "123456", "654321"); err != ErrUserNotFound {
			t.Errorf("expected ErrUserNotFound, got %v", err)
		}
	})
}

func TestAuthService_RefreshToken(t *testing.T) {
	userRepo := newMockUserRepo()
	walletRepo := newMockWalletRepo()
	log := newTestLogger()

	svc := NewAuthService(userRepo, walletRepo, config.AuthConfig{
		BcryptCost:     4,
		MaxPINAttempts: 3,
		PINLockoutMins: 15,
	}, "test-secret", 15*time.Minute, 7*24*time.Hour, nil, log)
	phone := "+2348055555555"
	user, _ := svc.Register(context.Background(), phone, "Refresh Test", "123456")
	_ = user

	t.Run("successful refresh", func(t *testing.T) {
		session, err := svc.Login(context.Background(), phone, "123456")
		if err != nil {
			t.Fatalf("login failed: %v", err)
		}
		newSession, err := svc.RefreshToken(context.Background(), session.RefreshToken)
		if err != nil {
			t.Fatalf("refresh failed: %v", err)
		}
		if newSession.AccessToken == "" {
			t.Error("expected new access token")
		}
		if newSession.RefreshToken == "" {
			t.Error("expected new refresh token")
		}
	})

	t.Run("expired token", func(t *testing.T) {
		_, err := svc.RefreshToken(context.Background(), "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIiwic2NvcGUiOiJyZWZyZXNoIiwiZXhwIjoxNTE2MjM5MDIyfQ.dGhlLXNpZ25hdHVyZQ")
		if err == nil {
			t.Error("expected error for expired token")
		}
	})

	t.Run("invalid signature", func(t *testing.T) {
		_, err := svc.RefreshToken(context.Background(), "invalid-token")
		if err == nil {
			t.Error("expected error for invalid token")
		}
	})
}
