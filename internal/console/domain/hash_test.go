package domain

import (
	"bytes"
	"testing"
)

func TestContentHashEncodings(t *testing.T) {
	t.Parallel()

	hash, err := ContentHashFromBytes(bytes.Repeat([]byte{0xff}, 32))
	if err != nil {
		t.Fatalf("ContentHashFromBytes() error = %v", err)
	}
	if got, want := hash.Hex(), "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"; got != want {
		t.Fatalf("Hex() = %q, want %q", got, want)
	}
	if got, want := hash.Base64(), "//////////////////////////////////////////8="; got != want {
		t.Fatalf("Base64() = %q, want %q", got, want)
	}
}

func TestContentHashRejectsWrongLength(t *testing.T) {
	t.Parallel()

	if _, err := ContentHashFromBytes(make([]byte, 31)); err == nil {
		t.Fatal("ContentHashFromBytes() error = nil, want length error")
	}
}
