package qrcode

// Tests for the template() format constructor (template.go).
//
// Sections:
//   - templateContent wire-format correctness per kind
//   - required-param / unknown-kind errors
//   - template() end-to-end through Starlark
//   - template() Starlark argument validation (positional count, kind type,
//     level/quiet_zone keyword types)
//   - starlarkToString keyword stringification

import (
	"strings"
	"testing"

	"go.starlark.net/starlark"
)

// --- templateContent wire-format correctness ---------------------------------

func TestTemplateContent(t *testing.T) {
	cases := []struct {
		kind   string
		params map[string]string
		want   string
	}{
		{"url", map[string]string{"url": "https://example.com"}, "https://example.com"},
		{"tel", map[string]string{"number": "+8613800138000"}, "tel:+8613800138000"},
		{"sms", map[string]string{"number": "+1555", "message": "hi"}, "SMSTO:+1555:hi"},
		{"geo", map[string]string{"lat": "30.0", "lng": "120.0"}, "geo:30.0,120.0"},
		{"wifi", map[string]string{"ssid": "Net", "password": "pw"}, "WIFI:T:WPA;S:Net;P:pw;H:false;;"},
		{"wifi", map[string]string{"ssid": "My;Net", "password": "a\\b", "security": "WPA2", "hidden": "true"}, "WIFI:T:WPA2;S:My\\;Net;P:a\\\\b;H:true;;"},
		{"vcard", map[string]string{"name": "Ada", "phone": "+1", "email": "a@b.com"}, "MECARD:N:Ada;TEL:+1;EMAIL:a@b.com;;"},
	}
	for _, c := range cases {
		got, err := templateContent(c.kind, c.params)
		if err != nil {
			t.Errorf("%s%v: unexpected error %v", c.kind, c.params, err)
			continue
		}
		if got != c.want {
			t.Errorf("%s: content = %q, want %q", c.kind, got, c.want)
		}
	}
}

func TestTemplateContentEmail(t *testing.T) {
	got, err := templateContent("email", map[string]string{"to": "a@b.com", "subject": "Hi there", "body": "Hello & welcome"})
	if err != nil {
		t.Fatalf("email: %v", err)
	}
	if !strings.HasPrefix(got, "mailto:a@b.com?") {
		t.Errorf("email content %q missing mailto prefix", got)
	}
	if !strings.Contains(got, "subject=Hi+there") || !strings.Contains(got, "body=Hello+%26+welcome") {
		t.Errorf("email content %q missing url-encoded subject/body", got)
	}
}

// --- errors ------------------------------------------------------------------

func TestTemplateContentErrors(t *testing.T) {
	if _, err := templateContent("wifi", map[string]string{"password": "x"}); err == nil || !strings.Contains(err.Error(), "ssid") {
		t.Errorf("missing ssid should error, got %v", err)
	}
	if _, err := templateContent("nope", map[string]string{}); err == nil || !strings.Contains(err.Error(), "unknown kind") {
		t.Errorf("unknown kind should error, got %v", err)
	}
}

// --- end-to-end through Starlark ---------------------------------------------

func TestTemplateEndToEnd(t *testing.T) {
	res, err := run(t, `
load("qrcode", "template")
wifi = template("wifi", ssid="Net", password="pw", security="WPA2")
wifi_sz = wifi.size
svg = wifi.svg(module_size=2)
u = template("url", url="https://example.com")
u_sz = u.size
geo = template("geo", lat=30, lng=120)   # numbers stringified
geo_ok = geo.size > 0
`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if sz, _ := res["wifi_sz"].(int64); sz < 21 {
		t.Errorf("wifi QR size = %v, want a real QR", res["wifi_sz"])
	}
	if s, _ := res["svg"].(string); !strings.HasPrefix(s, "<svg") {
		t.Errorf("wifi.svg() malformed")
	}
	if sz, _ := res["u_sz"].(int64); sz < 21 {
		t.Errorf("url QR size = %v", res["u_sz"])
	}
	if res["geo_ok"] != true {
		t.Errorf("geo template did not produce a QR")
	}
}

func TestTemplateUnknownKindThroughStarlark(t *testing.T) {
	_, err := run(t, `load("qrcode","template"); template("bogus", x="1")`)
	if err == nil || !strings.Contains(err.Error(), "unknown kind") {
		t.Errorf("unknown kind should error through Starlark, got %v", err)
	}
}

// --- more templateContent wire-format coverage -------------------------------

// TestTemplateContentMore exercises the kind branches and escaping the core
// table does not: a bare mailto with no query, a full MECARD with org/url, the
// wifi nopass + hidden defaults, and MECARD/WIFI special-character escaping.
func TestTemplateContentMore(t *testing.T) {
	cases := []struct {
		name   string
		kind   string
		params map[string]string
		want   string
	}{
		{"email no query", "email", map[string]string{"to": "a@b.com"}, "mailto:a@b.com"},
		{"sms no message", "sms", map[string]string{"number": "+1555"}, "SMSTO:+1555:"},
		{"wifi nopass", "wifi", map[string]string{"ssid": "Cafe", "security": "nopass"}, "WIFI:T:nopass;S:Cafe;P:;H:false;;"},
		{"wifi hidden 1", "wifi", map[string]string{"ssid": "X", "hidden": "1"}, "WIFI:T:WPA;S:X;P:;H:true;;"},
		{"wifi hidden True", "wifi", map[string]string{"ssid": "X", "hidden": "True"}, "WIFI:T:WPA;S:X;P:;H:true;;"},
		{"wifi escape ssid", "wifi", map[string]string{"ssid": `a;b,c:d"e\f`}, `WIFI:T:WPA;S:a\;b\,c\:d\"e\\f;P:;H:false;;`},
		{
			// MECARD escapes ':' inside field values, so the URL's "http://x"
			// becomes "http\://x" — the field is delimited, not the URL.
			"vcard full", "vcard",
			map[string]string{"name": "Ada", "phone": "+1", "email": "a@b.com", "org": "Lab", "url": "http://x"},
			`MECARD:N:Ada;TEL:+1;EMAIL:a@b.com;ORG:Lab;URL:http\://x;;`,
		},
		{"vcard escape", "vcard", map[string]string{"name": `Doe; Jane:`}, `MECARD:N:Doe\; Jane\:;;`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := templateContent(c.kind, c.params)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != c.want {
				t.Errorf("content = %q, want %q", got, c.want)
			}
		})
	}
}

// TestTemplateContentRequiredErrors covers each kind's missing-required-param
// branch (the error names the missing key and lists the kind's params).
func TestTemplateContentRequiredErrors(t *testing.T) {
	cases := []struct {
		kind, missing string
		params        map[string]string
	}{
		{"url", "url", map[string]string{}},
		{"tel", "number", map[string]string{}},
		{"sms", "number", map[string]string{"message": "hi"}},
		{"geo", "lat", map[string]string{"lng": "1"}},
		{"geo", "lng", map[string]string{"lat": "1"}},
		{"email", "to", map[string]string{"subject": "x"}},
		{"vcard", "name", map[string]string{"phone": "+1"}},
		// whitespace-only is treated as missing (required uses TrimSpace).
		{"url", "url", map[string]string{"url": "   "}},
	}
	for _, c := range cases {
		t.Run(c.kind+"/"+c.missing, func(t *testing.T) {
			_, err := templateContent(c.kind, c.params)
			if err == nil {
				t.Fatalf("expected missing-%s error, got nil", c.missing)
			}
			if !strings.Contains(err.Error(), c.missing) {
				t.Errorf("error %q does not name missing param %q", err, c.missing)
			}
		})
	}
}

// --- template() Starlark argument validation ---------------------------------

func TestTemplateArgErrors(t *testing.T) {
	cases := []struct {
		name, script, wantSubstr string
	}{
		{"no positional", `load("qrcode","template"); template()`, "want exactly 1 positional argument"},
		{"two positional", `load("qrcode","template"); template("url", "x")`, "want exactly 1 positional argument"},
		{"non-string kind", `load("qrcode","template"); template(123)`, "kind must be a string"},
		{"bad quiet_zone type", `load("qrcode","template"); template("url", url="x", quiet_zone="big")`, "quiet_zone must be an int"},
		{"negative quiet_zone", `load("qrcode","template"); template("url", url="x", quiet_zone=-1)`, "quiet_zone must not be negative"},
		{"bad level", `load("qrcode","template"); template("url", url="x", level="Z")`, "L, M, Q, or H"},
		{"missing required", `load("qrcode","template"); template("wifi")`, "ssid"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := run(t, c.script)
			if err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !strings.Contains(err.Error(), c.wantSubstr) {
				t.Errorf("error %q does not contain %q", err, c.wantSubstr)
			}
		})
	}
}

// TestTemplateLevelKeyword verifies the level keyword is threaded through (a
// valid level renders; the QR still has a real dimension).
func TestTemplateLevelKeyword(t *testing.T) {
	res, err := run(t, `
load("qrcode", "template")
sz = template("url", url="https://example.com", level="H").size
`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if sz, _ := res["sz"].(int64); sz < 21 {
		t.Errorf("level=H QR size = %v, want a real QR", res["sz"])
	}
}

// --- starlarkToString keyword stringification --------------------------------

// TestStarlarkToString covers the keyword-value stringifier directly: strings
// pass through unquoted, other values render via their Starlark repr.
func TestStarlarkToString(t *testing.T) {
	cases := []struct {
		in   starlark.Value
		want string
	}{
		{starlark.String("hello"), "hello"},
		{starlark.String(""), ""},
		{starlark.MakeInt(30), "30"},
		{starlark.MakeInt(-7), "-7"},
		{starlark.True, "True"},
		{starlark.False, "False"},
		{starlark.Float(1.5), "1.5"},
	}
	for _, c := range cases {
		if got := starlarkToString(c.in); got != c.want {
			t.Errorf("starlarkToString(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestTemplateNumberStringified is the end-to-end form: a numeric keyword (geo
// lat/lng) is stringified into the wire content.
func TestTemplateNumberStringified(t *testing.T) {
	got, err := templateContent("geo", map[string]string{"lat": "30", "lng": "120"})
	if err != nil {
		t.Fatalf("geo: %v", err)
	}
	if got != "geo:30,120" {
		t.Errorf("geo content = %q, want %q", got, "geo:30,120")
	}
}
