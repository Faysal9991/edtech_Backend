package notification

import "testing"

func TestCryptorRoundTrip(t *testing.T) {
	c, err := NewCryptor("01234567890123456789012345678901")
	if err != nil {
		t.Fatal(err)
	}
	encrypted, err := c.Encrypt("device-token-secret")
	if err != nil {
		t.Fatal(err)
	}
	if encrypted == "device-token-secret" {
		t.Fatal("token was not encrypted")
	}
	plain, err := c.Decrypt(encrypted)
	if err != nil || plain != "device-token-secret" {
		t.Fatalf("round trip failed: %q %v", plain, err)
	}
}
