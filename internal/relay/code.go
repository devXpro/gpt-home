package relay

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// codePrefix starts every connect code; the digit is the code format version.
const codePrefix = "ghr1."

var (
	tokenRE  = regexp.MustCompile(`^[0-9a-f]{64}$`)
	domainRE = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)
)

// Code is the content of a connect code: what the home side needs to reach its relay.
type Code struct {
	Domain string // relay domain, e.g. "homie.jobbyjobs.work"
	Token  string // frps token, 64 lowercase hex chars
}

// codeJSON is the wire form; field order matches what install/server.sh prints.
type codeJSON struct {
	V      int    `json:"v"`
	Domain string `json:"domain"`
	Token  string `json:"token"`
}

// Validate checks the domain and the token shape.
func (c Code) Validate() error {
	if err := validateDomain(c.Domain); err != nil {
		return err
	}
	if !tokenRE.MatchString(c.Token) {
		return errors.New("invalid relay token: want 64 lowercase hex characters")
	}
	return nil
}

// ValidateDomain reports whether d is usable as the relay domain.
func ValidateDomain(d string) error { return validateDomain(d) }

// validateDomain accepts a lowercase DNS name with at least two labels, without scheme, port or path.
func validateDomain(d string) error {
	if len(d) > 253 || !domainRE.MatchString(d) {
		return fmt.Errorf("invalid relay domain %q", d)
	}
	return nil
}

// FormatCode encodes c as "ghr1." + base64url(no padding) of {"v":1,"domain":…,"token":…}.
func FormatCode(c Code) (string, error) {
	if err := c.Validate(); err != nil {
		return "", err
	}
	raw, err := json.Marshal(codeJSON{V: 1, Domain: c.Domain, Token: c.Token})
	if err != nil {
		return "", err
	}
	return codePrefix + base64.RawURLEncoding.EncodeToString(raw), nil
}

// ParseCode decodes a connect code. Surrounding whitespace is ignored. Errors never contain the token.
func ParseCode(s string) (Code, error) {
	s = strings.TrimSpace(s)
	payload, ok := strings.CutPrefix(s, codePrefix)
	if !ok {
		return Code{}, errors.New("not a connect code: it must start with \"" + codePrefix + "\"")
	}
	raw, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return Code{}, errors.New("connect code is damaged: bad base64url")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var cj codeJSON
	if err := dec.Decode(&cj); err != nil {
		return Code{}, errors.New("connect code is damaged: bad JSON")
	}
	if cj.V != 1 {
		return Code{}, fmt.Errorf("unsupported connect code version %d", cj.V)
	}
	c := Code{Domain: strings.ToLower(cj.Domain), Token: cj.Token}
	if err := c.Validate(); err != nil {
		return Code{}, err
	}
	return c, nil
}
