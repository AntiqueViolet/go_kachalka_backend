// Package contact normalizes phone numbers and email addresses.
package contact

import (
	"errors"
	"net/mail"
	"strings"
	"unicode/utf8"

	"github.com/nyaruka/phonenumbers"
)

var (
	// ErrInvalidPhone means the input is not a valid number that can receive SMS.
	ErrInvalidPhone = errors.New("invalid phone number")
	// ErrInvalidEmail means the input is not a plain ASCII address with a host-name domain.
	ErrInvalidEmail = errors.New("invalid email")
)

// defaultRegion is used for numbers entered without a leading "+".
const defaultRegion = "RU"

const (
	maxPhoneInput = 32
	maxEmailLen   = 254
	maxLocalLen   = 64
	maxLabelLen   = 63
)

// NormalizePhone parses a user-entered number and returns it in E.164.
// Only ASCII digits, "+", spaces and the usual separators are accepted: the parser would
// otherwise turn letters into digits and drop extensions, sending the SMS to another number.
// Only numbers that can receive SMS (mobile or fixed-line-or-mobile) pass.
func NormalizePhone(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > maxPhoneInput || strings.IndexFunc(raw, notPhoneRune) >= 0 {
		return "", ErrInvalidPhone
	}
	num, err := phonenumbers.Parse(raw, defaultRegion)
	if err != nil || !phonenumbers.IsValidNumber(num) {
		return "", ErrInvalidPhone
	}
	switch phonenumbers.GetNumberType(num) {
	case phonenumbers.MOBILE, phonenumbers.FIXED_LINE_OR_MOBILE:
	default:
		return "", ErrInvalidPhone
	}
	return phonenumbers.Format(num, phonenumbers.E164), nil
}

func notPhoneRune(r rune) bool {
	return (r < '0' || r > '9') && !strings.ContainsRune("+ -().\u00a0", r)
}

// NormalizeEmail trims and lowercases an address and checks that it is a bare ASCII addr-spec
// with a local part of at most 64 characters and a host-name domain.
// Non-ASCII addresses are rejected on purpose: Go and Postgres citext lowercase them
// differently, and an IDN domain and its punycode form would be two keys for one mailbox.
func NormalizeEmail(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" || len(s) > maxEmailLen || !isASCII(s) {
		return "", ErrInvalidEmail
	}
	s = strings.ToLower(s)
	addr, err := mail.ParseAddress(s)
	if err != nil || addr.Name != "" || addr.Address != s {
		return "", ErrInvalidEmail
	}
	at := strings.LastIndexByte(s, '@')
	if at > maxLocalLen || !isHostname(s[at+1:]) {
		return "", ErrInvalidEmail
	}
	return s, nil
}

func isASCII(s string) bool {
	for i := range len(s) {
		if s[i] >= utf8.RuneSelf {
			return false
		}
	}
	return true
}

// isHostname accepts lowercase host names: at least two labels of 1–63 letters, digits or
// hyphens, no label starting or ending with a hyphen, and a top-level label with a letter.
// IP addresses and literals such as [127.0.0.1] are rejected.
func isHostname(d string) bool {
	labels := strings.Split(d, ".")
	if len(labels) < 2 {
		return false
	}
	for _, l := range labels {
		if l == "" || len(l) > maxLabelLen || l[0] == '-' || l[len(l)-1] == '-' {
			return false
		}
		for i := range len(l) {
			if c := l[i]; (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' {
				return false
			}
		}
	}
	return strings.ContainsFunc(labels[len(labels)-1], func(r rune) bool { return r >= 'a' && r <= 'z' })
}
