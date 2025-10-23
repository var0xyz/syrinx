package main

import (
	"fmt"
	"strings"
	"time"
	"unicode"
)

// trimInvisibleChars removes invisible characters from a string
func trimInvisibleChars(s string) string {
	// First trim standard whitespace
	s = strings.TrimSpace(s)

	// Remove invisible characters (non-printable characters except spaces)
	var result strings.Builder
	for _, r := range s {
		if unicode.IsPrint(r) || r == ' ' {
			result.WriteRune(r)
		}
	}

	// Trim spaces again after removing invisible chars
	return strings.TrimSpace(result.String())
}

// This function extracts the challenge from a signed message. Assumptions:
// The message is clearsigned.
// The challenge begins after the first empty line inside the message block.
// There could be other garbage in the signed message, that's why we don't
// return as soon as we find the string "Challenge: ". We want the whole thing.
func extractChallenge(signedChallenge string) string {
	inMessage := false
	inChallenge := false
	var challenge string
	for line := range strings.SplitSeq(signedChallenge, "\n") {
		if line == "-----BEGIN PGP SIGNATURE-----" {
			inMessage = false
			inChallenge = false
		}
		if inChallenge {
			challenge += line + "\n"
		}
		if line == "-----BEGIN PGP SIGNED MESSAGE-----" {
			inMessage = true
		}
		if inMessage && line == "" {
			inChallenge = true
		}
	}

	return strings.TrimSpace(challenge)
}

func validateChallenge(challenge string) error {
	if challenge == "" {
		return fmt.Errorf("challenge is empty")
	}

	// Example challenge: "2025-10-05T23:04:09Z"
	if len(challenge) != 20 {
		return fmt.Errorf("challenge is not 20 characters long")
	}

	challengeTime, err := time.Parse(time.RFC3339Nano, challenge)
	if err != nil {
		return fmt.Errorf("invalid timestamp format: %v", err)
	}

	// Challenge cannot be too old
	if time.Since(challengeTime) > 1*time.Hour {
		return fmt.Errorf("challenge has expired")
	}

	return nil
}
