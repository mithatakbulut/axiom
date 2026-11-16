package domain

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
)

type ID string

func NewID(prefix string) (ID, error) {
	if prefix == "" || strings.ContainsAny(prefix, "_ \t\n") {
		return "", fmt.Errorf("invalid ID prefix %q", prefix)
	}
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate ID: %w", err)
	}
	return ID(prefix + "_" + hex.EncodeToString(b)), nil
}

func (id ID) Validate(prefix string) error {
	s := string(id)
	if !strings.HasPrefix(s, prefix+"_") || len(s) != len(prefix)+33 {
		return fmt.Errorf("invalid %s ID", prefix)
	}
	if _, err := hex.DecodeString(s[len(prefix)+1:]); err != nil {
		return fmt.Errorf("invalid %s ID", prefix)
	}
	return nil
}
