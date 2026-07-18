package recovery

import (
	"fmt"
	"strings"
	"unicode"
)

const recommendedPasswordLen = 16

// PasswordStrengthWarning returns a non-empty message when pw is weaker than
// recommended. Callers should print it and still accept the password (except
// empty, which remains invalid for the bundle).
func PasswordStrengthWarning(pw string) string {
	if pw == "" {
		return ""
	}
	if len(pw) < recommendedPasswordLen {
		return fmt.Sprintf(
			"password is only %d characters (recommend ≥%d with upper, lower, digit, and symbol)",
			len(pw), recommendedPasswordLen,
		)
	}

	var upper, lower, digit, symbol bool
	for _, r := range pw {
		switch {
		case unicode.IsUpper(r):
			upper = true
		case unicode.IsLower(r):
			lower = true
		case unicode.IsDigit(r):
			digit = true
		case unicode.IsPunct(r) || unicode.IsSymbol(r):
			symbol = true
		}
	}
	var missing []string
	if !upper {
		missing = append(missing, "uppercase")
	}
	if !lower {
		missing = append(missing, "lowercase")
	}
	if !digit {
		missing = append(missing, "digit")
	}
	if !symbol {
		missing = append(missing, "symbol")
	}
	if len(missing) == 0 {
		return ""
	}
	return fmt.Sprintf(
		"password is missing %s (recommend mixed case, digits, and symbols)",
		strings.Join(missing, ", "),
	)
}
