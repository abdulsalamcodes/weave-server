package model

import (
	"time"

	"github.com/google/uuid"
)

type KYCLevel int

const (
	KYCLevelBasic    KYCLevel = 1
	KYCLevelVerified KYCLevel = 2
	KYCLevelEnhanced KYCLevel = 3
)

type User struct {
	ID                uuid.UUID `json:"id"`
	Phone             string    `json:"phone"`
	Email             string    `json:"email,omitempty"`
	FullName          string    `json:"full_name"`
	BVNHash           string    `json:"-"` // hashed, never returned
	NINHash           string    `json:"-"` // hashed, never returned
	KYCLevel          KYCLevel  `json:"kyc_level"`
	PINHash           string    `json:"-"` // bcrypt hash, never returned
	BiometricEnabled  bool      `json:"biometric_enabled"`
	BiometricPublicKey string   `json:"-"` // stored for WebAuthn, never returned
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

type CreateUserInput struct {
	Phone    string
	FullName string
	PIN      string
}

type UpdateUserInput struct {
	Email    *string
	FullName *string
}

type UserSession struct {
	UserID       uuid.UUID `json:"user_id"`
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	ExpiresAt    time.Time `json:"expires_at"`
}
