package main

import (
	"encoding/hex"
	"testing"
)

func TestLoadKCPAEADKey(t *testing.T) {
	want := []byte("0123456789abcdef0123456789abcdef")
	t.Setenv(kcpKeyEnv, hex.EncodeToString(want))
	got, err := loadKCPAEADKey()
	if err != nil {
		t.Fatalf("load key: %v", err)
	}
	if string(got) != string(want) {
		t.Fatalf("key mismatch: got %x want %x", got, want)
	}
}

func TestLoadKCPAEADKeyRejectsInvalidInput(t *testing.T) {
	for _, input := range []string{"", "abcd", "not-hex"} {
		t.Run(input, func(t *testing.T) {
			t.Setenv(kcpKeyEnv, input)
			if _, err := loadKCPAEADKey(); err == nil {
				t.Fatalf("input %q should fail", input)
			}
		})
	}
}
