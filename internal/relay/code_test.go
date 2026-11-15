package relay

import (
	"encoding/base64"
	"strings"
	"testing"
)

const testToken = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func TestCodeRoundTrip(t *testing.T) {
	in := Code{Domain: "homie.jobbyjobs.work", Token: testToken}
	s, err := FormatCode(in)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(s, "ghr1.") {
		t.Fatalf("code %q lacks the ghr1. prefix", s)
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(s, "ghr1."))
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"v":1,"domain":"homie.jobbyjobs.work","token":"` + testToken + `"}`; string(raw) != want {
		t.Fatalf("payload = %s, want %s", raw, want)
	}
	out, err := ParseCode("  " + s + "\n")
	if err != nil {
		t.Fatal(err)
	}
	if out != in {
		t.Fatalf("round trip = %+v, want %+v", out, in)
	}
}

func encode(payload string) string {
	return "ghr1." + base64.RawURLEncoding.EncodeToString([]byte(payload))
}

func TestParseCodeErrors(t *testing.T) {
	tests := []struct {
		name, code, wantErr string
	}{
		{"empty", "", "must start with"},
		{"wrong prefix", "ghr2.abc", "must start with"},
		{"bad base64", "ghr1.!!!", "bad base64url"},
		{"padded base64", "ghr1." + base64.URLEncoding.EncodeToString([]byte(`{"v":1}`)), "bad base64url"},
		{"bad json", encode(`{"v":1,`), "bad JSON"},
		{"unknown field", encode(`{"v":1,"domain":"a.b","token":"` + testToken + `","x":1}`), "bad JSON"},
		{"version 2", encode(`{"v":2,"domain":"a.b","token":"` + testToken + `"}`), "version 2"},
		{"no domain", encode(`{"v":1,"token":"` + testToken + `"}`), "invalid relay domain"},
		{"domain with scheme", encode(`{"v":1,"domain":"https://a.b","token":"` + testToken + `"}`), "invalid relay domain"},
		{"domain with port", encode(`{"v":1,"domain":"a.b:443","token":"` + testToken + `"}`), "invalid relay domain"},
		{"single label", encode(`{"v":1,"domain":"localhost","token":"` + testToken + `"}`), "invalid relay domain"},
		{"short token", encode(`{"v":1,"domain":"a.b","token":"abc"}`), "64 lowercase hex"},
		{"upper token", encode(`{"v":1,"domain":"a.b","token":"` + strings.ToUpper(testToken) + `"}`), "64 lowercase hex"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseCode(tt.code)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("err = %v, want it to contain %q", err, tt.wantErr)
			}
			if strings.Contains(err.Error(), testToken) || strings.Contains(err.Error(), strings.ToUpper(testToken)) {
				t.Fatalf("error leaks the token: %v", err)
			}
		})
	}
}

func TestParseCodeLowercasesDomain(t *testing.T) {
	c, err := ParseCode(encode(`{"v":1,"domain":"Homie.Example.COM","token":"` + testToken + `"}`))
	if err != nil {
		t.Fatal(err)
	}
	if c.Domain != "homie.example.com" {
		t.Fatalf("domain = %q", c.Domain)
	}
}

func TestFormatCodeValidates(t *testing.T) {
	if _, err := FormatCode(Code{Domain: "a.b", Token: "nope"}); err == nil {
		t.Fatal("FormatCode accepted a bad token")
	}
	if _, err := FormatCode(Code{Domain: "", Token: testToken}); err == nil {
		t.Fatal("FormatCode accepted an empty domain")
	}
}
