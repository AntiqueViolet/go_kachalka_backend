package otp

import (
	"encoding/hex"
	"regexp"
	"testing"

	"github.com/google/uuid"
)

func TestGenerateFormat(t *testing.T) {
	six := regexp.MustCompile(`^\d{6}$`)
	seen := map[string]bool{}
	for range 200 {
		c, err := Generate()
		if err != nil {
			t.Fatal(err)
		}
		if !six.MatchString(c) {
			t.Fatalf("code %q is not 6 digits", c)
		}
		seen[c] = true
	}
	if len(seen) < 190 {
		t.Fatalf("only %d distinct codes out of 200", len(seen))
	}
}

func TestHashBindsKeyIDAndCode(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	id := uuid.New()
	h := Hash(key, id, "123456")
	if !Matches(key, id, "123456", h) {
		t.Fatal("same inputs must match")
	}
	if Matches(key, id, "123457", h) {
		t.Fatal("different code matched")
	}
	if Matches(key, uuid.New(), "123456", h) {
		t.Fatal("different id matched")
	}
	if Matches([]byte("another-key-another-key-another-k"), id, "123456", h) {
		t.Fatal("different key matched")
	}
}

var (
	testKey = []byte("0123456789abcdef0123456789abcdef")
	testID  = uuid.MustParse("0190a0b0-c0d0-7e0f-8a1b-2c3d4e5f6071")
)

func TestGenerateSpreadsOverWholeSpace(t *testing.T) {
	const n = 20000
	var seen [6][10]bool
	var first [10]int
	for range n {
		c, err := Generate()
		if err != nil {
			t.Fatal(err)
		}
		for i := range 6 {
			seen[i][c[i]-'0'] = true
		}
		first[c[0]-'0']++
	}
	for pos := range seen {
		for d, ok := range seen[pos] {
			if !ok {
				t.Errorf("digit %d never appeared at position %d in %d codes", d, pos, n)
			}
		}
	}
	// expected 2000 +/- 42 (1 sd); +/-350 is >8 sd, so a false failure is practically impossible.
	for d, got := range first {
		if got < 1650 || got > 2350 {
			t.Errorf("leading digit %d appeared %d times, expected about 2000", d, got)
		}
	}
}

func TestHashKnownAnswer(t *testing.T) {
	got := hex.EncodeToString(Hash(testKey, testID, "123456"))
	const want = "0ad929f2a82f319a1e82df7d043c05a0dd74aaca42fda847b39d366c57355cb9"
	if got != want {
		t.Fatalf("Hash = %s, want %s", got, want)
	}
}

func TestMatchesRejectsMalformedHash(t *testing.T) {
	h := Hash(testKey, testID, "123456")
	bad := map[string][]byte{
		"nil":       nil,
		"empty":     {},
		"truncated": h[:len(h)-1],
		"extended":  append(append([]byte{}, h...), 0),
		"zeros":     make([]byte, len(h)),
	}
	for name, b := range bad {
		if Matches(testKey, testID, "123456", b) {
			t.Errorf("%s hash matched", name)
		}
	}
}

func TestMatchesRejectsMalformedCode(t *testing.T) {
	h := Hash(testKey, testID, "123456")
	for _, c := range []string{"", "12345", "1234567", "12345a", " 123456", "123456\n", "123456\x00", "１２３４５６"} {
		if Matches(testKey, testID, c, h) {
			t.Errorf("code %q matched", c)
		}
	}
}
