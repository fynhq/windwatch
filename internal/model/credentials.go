package model

import (
	"errors"
	"unicode/utf8"
)

const (
	MinUsernameLength = 3
	MaxUsernameLength = 64
	MinPasswordLength = 10
	MaxPasswordLength = 256
)

var (
	ErrUsernameTooShort = errors.New("username must be at least 3 characters")
	ErrUsernameTooLong  = errors.New("username must be at most 64 characters")
	ErrPasswordTooShort = errors.New("password must be at least 10 characters")
	ErrPasswordTooLong  = errors.New("password must be at most 256 characters")
)

func ValidateUsername(username string) error {
	switch count := utf8.RuneCountInString(username); {
	case count < MinUsernameLength:
		return ErrUsernameTooShort
	case count > MaxUsernameLength:
		return ErrUsernameTooLong
	default:
		return nil
	}
}

func ValidatePassword(password string) error {
	switch count := utf8.RuneCountInString(password); {
	case count < MinPasswordLength:
		return ErrPasswordTooShort
	case count > MaxPasswordLength:
		return ErrPasswordTooLong
	default:
		return nil
	}
}
