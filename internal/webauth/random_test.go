package webauth

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"
)

func TestRandomTokenFromReaderUsesRequestedEntropyBytes(t *testing.T) {
	src := bytes.Repeat([]byte{0xab}, 24)
	got, err := randomTokenFrom(bytes.NewReader(src), len(src))
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := base64.RawURLEncoding.DecodeString(got)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(decoded, src) {
		t.Fatalf("decoded token does not match supplied entropy: %x", decoded)
	}
}

func TestRandomTokenFromReaderPropagatesEntropyFailure(t *testing.T) {
	if _, err := randomTokenFrom(strings.NewReader("short"), 32); err == nil {
		t.Fatal("expected entropy read failure")
	}
}
