package token

import (
	"encoding/base64"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

var testRefreshKey = []byte("0123456789abcdef0123456789abcdef")

func TestRefreshRoundTrip(t *testing.T) {
	c := NewRefreshCodec(testRefreshKey)
	sid := uuid.New()
	raw := c.Encode(sid, 7)
	gotSID, gotGen, err := c.Decode(raw)
	if err != nil || gotSID != sid || gotGen != 7 {
		t.Fatalf("Decode = %v, %d, %v", gotSID, gotGen, err)
	}
	if raw != c.Encode(sid, 7) {
		t.Fatal("encoding must be deterministic")
	}
}

// Vectors computed independently from the spec formula: base64url(sid ‖ gen ‖ HMAC-SHA256(key,
// "refresh:v1" ‖ sid ‖ gen)). Tokens live on devices for 60 days, so the format must not drift.
func TestRefreshKnownAnswer(t *testing.T) {
	c := NewRefreshCodec(testRefreshKey)
	sid := uuid.MustParse("0192f3a4-5b6c-7d8e-9fa0-b1c2d3e4f506")
	vectors := map[int64]string{
		1:             "AZLzpFtsfY6foLHC0-T1BgAAAAAAAAABn1gx9dtZMAYycA3oX9N253U9vh2nnH0DWP44xpSv878",
		7:             "AZLzpFtsfY6foLHC0-T1BgAAAAAAAAAHnGMOnf7xj0v4xMsXyMcPSwxjB3xDlRU-L6Y_GDWcqC8",
		math.MaxInt64: "AZLzpFtsfY6foLHC0-T1Bn__________M34YiEgGo-O81_kdQcybUaGVZlJOg3xVxfcRmcvzH44",
	}
	for gen, want := range vectors {
		if got := c.Encode(sid, gen); got != want {
			t.Errorf("Encode(gen %d) = %s, want %s", gen, got, want)
		}
	}
}

func TestRefreshRejectsTampering(t *testing.T) {
	c := NewRefreshCodec(testRefreshKey)
	b, err := base64.RawURLEncoding.DecodeString(c.Encode(uuid.New(), 5))
	if err != nil {
		t.Fatal(err)
	}
	reencode := func(f func(b []byte)) string {
		cp := append([]byte(nil), b...)
		f(cp)
		return base64.RawURLEncoding.EncodeToString(cp)
	}
	cases := map[string]string{
		"session first byte":  reencode(func(b []byte) { b[0] ^= 1 }),
		"session last byte":   reencode(func(b []byte) { b[15] ^= 1 }),
		"generation high":     reencode(func(b []byte) { b[16] ^= 1 }),
		"generation 5 to 6":   reencode(func(b []byte) { b[23] = 6 }), // forging the next generation
		"mac first byte":      reencode(func(b []byte) { b[24] ^= 1 }),
		"mac last byte":       reencode(func(b []byte) { b[len(b)-1] ^= 1 }),
		"empty":               "",
		"not base64":          strings.Repeat("!", encodedRefreshLen),
		"too short":           "AAAA",
		"other key":           NewRefreshCodec([]byte("ffffffffffffffffffffffffffffffff")).Encode(uuid.New(), 1),
		"generation 0":        c.Encode(uuid.New(), 0),
		"negative generation": c.Encode(uuid.New(), -1),
	}
	for name, raw := range cases {
		if _, _, err := c.Decode(raw); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: err = %v, want ErrInvalid", name, err)
		}
	}
}

// One token must have exactly one accepted spelling.
func TestRefreshRejectsNonCanonicalEncoding(t *testing.T) {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
	c := NewRefreshCodec(testRefreshKey)
	raw := c.Encode(uuid.New(), 1)
	// 56 bytes leave 2 unused bits in the last character; setting one must not be accepted.
	last := strings.IndexByte(alphabet, raw[len(raw)-1])
	lf, crlf := string([]byte{10}), string([]byte{13, 10})
	variants := map[string]string{
		"padding bits set":  raw[:len(raw)-1] + string(alphabet[last|1]),
		"line feed inside":  raw[:30] + lf + raw[30:],
		"trailing crlf":     raw + crlf,
		"leading line feed": lf + raw,
	}
	for name, v := range variants {
		if _, _, err := c.Decode(v); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: err = %v, want ErrInvalid", name, err)
		}
	}
}

func TestNewRefreshCodecKey(t *testing.T) {
	for _, key := range [][]byte{nil, testRefreshKey[:31]} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("%d-byte key accepted", len(key))
				}
			}()
			NewRefreshCodec(key)
		}()
	}
	key := append([]byte(nil), testRefreshKey...)
	c := NewRefreshCodec(key)
	raw := c.Encode(uuid.New(), 1)
	clear(key) // the caller reusing its buffer must not break the codec
	if _, _, err := c.Decode(raw); err != nil {
		t.Fatalf("codec depends on the caller's key buffer: %v", err)
	}
}

func TestDecide(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	at := func(d time.Duration) *time.Time { v := now.Add(d); return &v }
	const grace = 30 * time.Second
	cases := []struct {
		name               string
		presented, current int64
		rotatedAt          *time.Time
		want               Decision
	}{
		{"current", 5, 5, at(-time.Minute), DecisionRotate},
		{"first rotation", 1, 1, nil, DecisionRotate},
		{"previous within grace", 4, 5, at(-10 * time.Second), DecisionReplay},
		{"previous exactly at grace", 4, 5, at(-grace), DecisionReplay},
		{"previous just after grace", 4, 5, at(-grace - time.Nanosecond), DecisionReuse},
		{"previous long after grace", 4, 5, at(-time.Minute), DecisionReuse},
		{"previous, never rotated", 4, 5, nil, DecisionReuse},
		{"older", 3, 5, at(-10 * time.Second), DecisionReuse},
		{"rotation 2 s in the future (skew)", 4, 5, at(2 * time.Second), DecisionReplay},
		{"rotation exactly replaySkew ahead", 4, 5, at(replaySkew), DecisionReplay},
		{"rotation just beyond replaySkew ahead", 4, 5, at(replaySkew + time.Nanosecond), DecisionReuse},
		{"rotation a year in the future (clock jumped back)", 4, 5, at(365 * 24 * time.Hour), DecisionReuse},
		{"future generation", 6, 5, at(-10 * time.Second), DecisionInvalid},
		{"generation 0", 0, 1, at(-10 * time.Second), DecisionInvalid},
		{"negative generation", -1, 1, at(-10 * time.Second), DecisionInvalid},
		{"corrupt session generation", 1, 0, nil, DecisionInvalid},
		{"no overflow at the extremes", math.MaxInt64, math.MinInt64, at(-10 * time.Second), DecisionInvalid},
	}
	for _, c := range cases {
		if got := Decide(c.presented, c.current, c.rotatedAt, now, grace); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
	// REFRESH_GRACE=0 turns replays off completely, skew window included.
	for _, d := range []time.Duration{0, 2 * time.Second, -2 * time.Second} {
		if got := Decide(4, 5, at(d), now, 0); got != DecisionReuse {
			t.Errorf("grace 0, rotated %v from now: got %v, want reuse", d, got)
		}
	}
}

func TestDecisionString(t *testing.T) {
	want := map[Decision]string{DecisionRotate: "rotate", DecisionReplay: "replay", DecisionReuse: "reuse", DecisionInvalid: "invalid", 0: "Decision(0)"}
	for d, w := range want {
		if d.String() != w {
			t.Errorf("Decision(%d).String() = %q, want %q", int(d), d.String(), w)
		}
	}
}
