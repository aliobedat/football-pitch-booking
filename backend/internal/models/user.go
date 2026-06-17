package models

import "time"

type UserRole string

const (
	RolePlayer UserRole = "player"
	RoleOwner  UserRole = "owner"
	RoleAdmin  UserRole = "admin"
)

// User mirrors the users table. Phone-first OTP is the sole auth method, so
// there is no password field; email is an optional/secondary identifier.
type User struct {
	ID        int       `json:"id"`
	FullName  string    `json:"full_name"`
	Email     string    `json:"email"`
	Phone     string    `json:"phone,omitempty"`
	Role      UserRole  `json:"role"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// SafeUser is the public-facing representation of a user — no sensitive fields.
// Always return this type in API responses, never models.User directly.
type SafeUser struct {
	ID        int       `json:"id"`
	FullName  string    `json:"full_name"`
	Email     string    `json:"email"`
	Phone     string    `json:"phone,omitempty"`
	Role      UserRole  `json:"role"`
	CreatedAt time.Time `json:"created_at"`
}

func (u *User) Safe() SafeUser {
	return SafeUser{
		ID:        u.ID,
		FullName:  u.FullName,
		Email:     u.Email,
		Phone:     u.Phone,
		Role:      u.Role,
		CreatedAt: u.CreatedAt,
	}
}

// RefreshToken mirrors the refresh_tokens table.
type RefreshToken struct {
	ID        int       `json:"-"`
	UserID    int       `json:"-"`
	TokenHash string    `json:"-"`
	ExpiresAt time.Time `json:"-"`
	Revoked   bool      `json:"-"`
	CreatedAt time.Time `json:"-"`
}
