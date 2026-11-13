package token

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"time"

	"github.com/google/uuid"
)

const (
	refreshDomain = "refresh:v1"
	refreshRawLen = 16 + 8 + sha256.Size
	minRefreshKey = 32
	// replaySkew is how far before the recorded rotation time a replay is still accepted
	// (clocks of several instances may differ slightly).
	replaySkew = 5 * time.Second
)

// encodedRefreshLen is the only accepted token length. Checking it first also rejects line
// breaks, which Go's base64 decoder skips even in strict mode.
var encodedRefreshLen = base64.RawURLEncoding.EncodedLen(refreshRawLen)

// RefreshCodec derives refresh tokens from (session id, generation) with an HMAC.
// The server stores no token material: it recomputes the token, which makes replays idempotent.
type RefreshCodec struct {
	key []byte
}

// NewRefreshCodec panics if key is shorter than 32 bytes: with a weak key anyone could mint tokens.
// It runs once at startup with the key derived from APP_SECRET.
func NewRefreshCodec(key []byte) *RefreshCodec {
	if len(key) < minRefreshKey {
		panic("token: refresh key must be at least 32 bytes")
	}
	return &RefreshCodec{key: bytes.Clone(key)}
}

// Encode returns the token for the given generation (≥ 1) of a session.
func (c *RefreshCodec) Encode(sessionID uuid.UUID, generation int64) string {
	buf := make([]byte, 0, refreshRawLen)
	buf = append(buf, sessionID[:]...)
	buf = binary.BigEndian.AppendUint64(buf, uint64(generation))
	buf = append(buf, c.mac(sessionID, generation)...)
	return base64.RawURLEncoding.EncodeToString(buf)
}

// Decode verifies the MAC and returns the session and generation. Only the canonical spelling
// is accepted: exact length, strict base64 and a generation of at least 1.
func (c *RefreshCodec) Decode(raw string) (uuid.UUID, int64, error) {
	if len(raw) != encodedRefreshLen {
		return uuid.Nil, 0, ErrInvalid
	}
	b, err := base64.RawURLEncoding.Strict().DecodeString(raw)
	if err != nil || len(b) != refreshRawLen {
		return uuid.Nil, 0, ErrInvalid
	}
	var sid uuid.UUID
	copy(sid[:], b[:16])
	gen := int64(binary.BigEndian.Uint64(b[16:24]))
	if gen < 1 || !hmac.Equal(b[24:], c.mac(sid, gen)) {
		return uuid.Nil, 0, ErrInvalid
	}
	return sid, gen, nil
}

func (c *RefreshCodec) mac(sessionID uuid.UUID, generation int64) []byte {
	m := hmac.New(sha256.New, c.key)
	m.Write([]byte(refreshDomain))
	m.Write(sessionID[:])
	m.Write(binary.BigEndian.AppendUint64(nil, uint64(generation)))
	return m.Sum(nil)
}

// Decision is what the auth service should do with a presented refresh token.
type Decision int

const (
	// DecisionRotate: the current token; advance the generation.
	DecisionRotate Decision = iota + 1
	// DecisionReplay: the previous token within the grace window; return the current token again.
	DecisionReplay
	// DecisionReuse: an already spent token; treat as theft and revoke the session.
	DecisionReuse
	// DecisionInvalid: a generation the server never issued.
	DecisionInvalid
)

func (d Decision) String() string {
	switch d {
	case DecisionRotate:
		return "rotate"
	case DecisionReplay:
		return "replay"
	case DecisionReuse:
		return "reuse"
	case DecisionInvalid:
		return "invalid"
	}
	return fmt.Sprintf("Decision(%d)", int(d))
}

// Decide applies the rotation rules from spec section 5.3. current is the session's generation
// (sessions start at 1); rotatedAt is nil until the first rotation. A replay is accepted from
// replaySkew before the rotation time to grace after it, so a clock that jumped back can't widen
// it; grace ≤ 0 turns replays off. A generation above current (e.g. after restoring an older
// database backup) is invalid.
func Decide(presented, current int64, rotatedAt *time.Time, now time.Time, grace time.Duration) Decision {
	switch {
	case presented < 1 || presented > current:
		return DecisionInvalid
	case presented == current:
		return DecisionRotate
	case presented == current-1 && rotatedAt != nil && inReplayWindow(now.Sub(*rotatedAt), grace):
		return DecisionReplay
	default:
		return DecisionReuse
	}
}

func inReplayWindow(sinceRotation, grace time.Duration) bool {
	return grace > 0 && sinceRotation >= -replaySkew && sinceRotation <= grace
}
