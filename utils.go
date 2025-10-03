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

	if len(challenge) != 35 {
		return fmt.Errorf("challenge is not 35 characters long")
	}

	// Extract timestamp from challenge format "Challenge: 2025-09-30T07:51:25.032Z"
	parts := strings.Split(challenge, " ")
	if len(parts) != 2 || parts[0] != "Challenge:" {
		return fmt.Errorf("invalid challenge format")
	}

	timestamp := parts[1]
	challengeTime, err := time.Parse(time.RFC3339Nano, timestamp)
	if err != nil {
		return fmt.Errorf("invalid timestamp format: %v", err)
	}

	// Check if challenge is older than 24 hours
	if time.Since(challengeTime) > 24*time.Hour {
		return fmt.Errorf("challenge has expired")
	}

	return nil
}
