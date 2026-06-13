package qrcode

// Tests for the template() format constructor (template.go).
//
// Sections:
//   - templateContent wire-format correctness per kind
//   - required-param / unknown-kind errors
//   - template() end-to-end through Starlark

import (
	"strings"
	"testing"
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
