// Package invite makes, parses and hashes invite codes.
//
// A code reads PREFIX-XXXX-XXXX: the organisation's prefix and eight
// characters of Crockford base32 that carry 40 bits from crypto/rand. Input
// is case-insensitive and tolerant of separators; the Crockford aliases
// (I and L for 1, O for 0) are accepted. The database keeps only a SHA-256
// hash of the canonical form, split into a lookup key (the first eight bytes)
// and the full digest, which is compared in constant time.
package invite

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"regexp"
	"strings"
)

// alphabet is Crockford's base32 alphabet.
const alphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// BodyLen is the number of base32 characters after the prefix.
const BodyLen = 8

// MaxInputLen bounds what Parse looks at; a code is short.
const MaxInputLen = 64

// prefixRe is the form of an organisation prefix.
var prefixRe = regexp.MustCompile(`^[A-Z][A-Z0-9]{1,9}$`)

// ErrMalformed is returned for input that cannot be a code.
var ErrMalformed = errors.New("invite: malformed code")

// ValidPrefix reports whether p can serve as an organisation prefix:
// 2 to 10 upper-case letters and digits, starting with a letter.
func ValidPrefix(p string) bool { return prefixRe.MatchString(p) }

// New returns a fresh code for prefix in its display form.
func New(prefix string) (string, error) {
	var b [5]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return Format(prefix, b), nil
}

// Format renders 40 bits as PREFIX-XXXX-XXXX.
func Format(prefix string, b [5]byte) string {
	v := uint64(b[0])<<32 | uint64(b[1])<<24 | uint64(b[2])<<16 | uint64(b[3])<<8 | uint64(b[4])
	var body [BodyLen]byte
	for i := BodyLen - 1; i >= 0; i-- {
		body[i] = alphabet[v&31]
		v >>= 5
	}
	return prefix + "-" + string(body[:4]) + "-" + string(body[4:])
}

// decodeChar maps one input character to its base32 value.
func decodeChar(c byte) (byte, bool) {
	switch {
	case c >= 'a' && c <= 'z':
		c -= 'a' - 'A'
	}
	switch c {
	case 'O':
		c = '0'
	case 'I', 'L':
		c = '1'
	}
	i := strings.IndexByte(alphabet, c)
	if i < 0 {
		return 0, false
	}
	return byte(i), true
}

// isSeparator reports whether c is ignored between characters.
func isSeparator(c byte) bool {
	switch c {
	case '-', ' ', '_', '.', '\t', '/', ':':
		return true
	}
	return false
}

// Parse reads a code typed by a person or an agent and returns its canonical
// body (eight upper-case characters). The prefix, when present, must equal
// prefix (case-insensitive); a code without a prefix is accepted too.
func Parse(prefix, input string) (string, error) {
	if len(input) > MaxInputLen {
		return "", ErrMalformed
	}
	s := strings.TrimSpace(input)
	// Strip the prefix when the input starts with it, followed by a
	// separator or directly by the body.
	if prefix != "" && hasPrefixASCIIFold(s, prefix) {
		rest := s[len(prefix):]
		if len(rest) > 0 && isSeparator(rest[0]) {
			s = rest
		} else if countBody(rest) == BodyLen {
			s = rest
		}
	}
	var out []byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		if isSeparator(c) {
			continue
		}
		v, ok := decodeChar(c)
		if !ok {
			return "", ErrMalformed
		}
		out = append(out, alphabet[v])
		if len(out) > BodyLen {
			return "", ErrMalformed
		}
	}
	if len(out) != BodyLen {
		return "", ErrMalformed
	}
	return string(out), nil
}

// hasPrefixASCIIFold reports whether s starts with the ASCII string p,
// ignoring ASCII case only.
func hasPrefixASCIIFold(s, p string) bool {
	if len(s) < len(p) {
		return false
	}
	for i := 0; i < len(p); i++ {
		a, b := s[i], p[i]
		if a >= 'a' && a <= 'z' {
			a -= 'a' - 'A'
		}
		if b >= 'a' && b <= 'z' {
			b -= 'a' - 'A'
		}
		if a != b {
			return false
		}
	}
	return true
}

// countBody counts the non-separator characters of s.
func countBody(s string) int {
	n := 0
	for i := 0; i < len(s); i++ {
		if !isSeparator(s[i]) {
			n++
		}
	}
	return n
}

// Display renders a canonical body with prefix.
func Display(prefix, body string) string {
	if len(body) != BodyLen {
		return prefix + "-" + body
	}
	return prefix + "-" + body[:4] + "-" + body[4:]
}

// Digest is the stored form of a code.
type Digest struct {
	Lookup string // hex of the first eight bytes; the index key
	Hash   []byte // the full SHA-256
}

// Hash returns the digest of a canonical body. The prefix is part of the
// hashed text, so a code of one organisation is not a code of another that
// shares a database.
func Hash(prefix, body string) Digest {
	sum := sha256.Sum256([]byte("anet-org/invite/v1\x00" + prefix + "\x00" + body))
	return Digest{Lookup: hex.EncodeToString(sum[:8]), Hash: sum[:]}
}

// Equal compares two digests in constant time.
func Equal(a, b []byte) bool {
	return len(a) == sha256.Size && subtle.ConstantTimeCompare(a, b) == 1
}
