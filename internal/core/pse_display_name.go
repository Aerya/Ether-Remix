package core

import (
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"
)

// MaxCustomNameLength bounds an owner-chosen display name, in characters.
const MaxCustomNameLength = 64

// ErrInvalidCustomName rejects a display name that is too long or has control characters.
var ErrInvalidCustomName = errors.New("display name must be at most 64 characters, without control characters")

// CleanCustomName trims an owner-chosen display name and validates it. An
// empty result means "use the name from Plex".
func CleanCustomName(raw string) (string, error) {
	name := strings.TrimSpace(raw)
	if !utf8.ValidString(name) || utf8.RuneCountInString(name) > MaxCustomNameLength {
		return "", ErrInvalidCustomName
	}
	if strings.IndexFunc(name, unicode.IsControl) >= 0 {
		return "", ErrInvalidCustomName
	}
	return name, nil
}

// ActivityName labels a user in statistics and activity: the owner-chosen
// name first, then the account name. Sign-in keeps using Username.
func (u User) ActivityName() string {
	if u.CustomName != "" {
		return u.CustomName
	}
	if u.Username != "" {
		return u.Username
	}
	return u.Title
}
