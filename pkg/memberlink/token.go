package memberlink

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// ErrTokenLifetime refuses a token whose expiry is more than
// MaxTokenLifetime ahead.
var ErrTokenLifetime = errors.New("token lifetime over 1h")

var (
	errTokenNoExpiry = errors.New("token has no expiry")
	errTokenExpired  = errors.New("token expired")
)

// claims reads a JWT's payload without verifying it. A token that is not a
// JWT has no claims.
func claims(token string) (c struct {
	Exp float64 `json:"exp"`
	Iss string  `json:"iss"`
}) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return c
	}
	if raw, err := base64.RawURLEncoding.DecodeString(parts[1]); err == nil {
		_ = json.Unmarshal(raw, &c)
	}
	return c
}

// TokenFresh refuses a token to forward: one with no expiry, an expired one
// or one valid for more than MaxTokenLifetime. Its errors never hold the
// token.
func TokenFresh(token string, now time.Time) error {
	c := claims(token)
	if c.Exp == 0 {
		return errTokenNoExpiry
	}
	exp := time.Unix(int64(c.Exp), 0)
	if !exp.After(now) {
		return errTokenExpired
	}
	if exp.Sub(now) > MaxTokenLifetime {
		return ErrTokenLifetime
	}
	return nil
}

// TokenIssuer is a token's unverified iss claim, "" when it has none.
func TokenIssuer(token string) string {
	return claims(token).Iss
}
