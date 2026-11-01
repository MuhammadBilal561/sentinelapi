package canonical

import (
	"crypto/sha256"
	"encoding/hex"
)

// FingerprintPrefix starts every fingerprint string.
const FingerprintPrefix = "sha256:"

// Fingerprint returns the SHA-256 digest of canonicalJSON in the form
// "sha256:" followed by 64 lowercase hexadecimal digits. The input should be
// the output of Encode or Canonicalize; fingerprinting arbitrary bytes is
// allowed but only canonical bytes give equal fingerprints for equal JSON.
//
// There is no domain separation here: this is a generic JSON primitive.
func Fingerprint(canonicalJSON []byte) string {
	sum := sha256.Sum256(canonicalJSON)
	return FingerprintPrefix + hex.EncodeToString(sum[:])
}

// FingerprintValue canonically encodes v and returns its fingerprint.
func FingerprintValue(v any) (string, error) {
	b, err := Encode(v)
	if err != nil {
		return "", err
	}
	return Fingerprint(b), nil
}
