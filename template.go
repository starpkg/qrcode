package qrcode

import (
	"fmt"
	"net/url"
	"sort"
	"strings"

	"go.starlark.net/starlark"
)

// template builds a QR from a named format template, filling the standard
// boilerplate from simple parameters — so a script doesn't hand-write the
// mailto:/WIFI:/MECARD: wire formats.
//
//	template(kind, level=<cfg>, quiet_zone=<cfg>, **params) -> QR
//
// kind is one of: url, email, wifi, tel, sms, geo, vcard. The remaining keyword
// arguments are the template's parameters (see templateParams for each kind).
// The returned value is a QR identical to encode()'s, so .ascii()/.pure_ascii()/
// .svg()/.bmp() all apply, and it shares level/quiet_zone/max_output_bytes.
func (m *Module) template(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	if len(args) != 1 {
		return none, fmt.Errorf("%s: want exactly 1 positional argument (kind), got %d", b.Name(), len(args))
	}
	kind, ok := starlark.AsString(args[0])
	if !ok {
		return none, fmt.Errorf("%s: kind must be a string, got %s", b.Name(), args[0].Type())
	}

	level := m.ext.GetString(configKeyECLevel)
	quietZone := m.ext.GetInt(configKeyQuietZone)
	params := map[string]string{}
	for _, kv := range kwargs {
		name := string(kv[0].(starlark.String))
		switch name {
		case "level":
			s, _ := starlark.AsString(kv[1])
			level = s
		case "quiet_zone":
			i, err := starlark.AsInt32(kv[1])
			if err != nil {
				return none, fmt.Errorf("%s: quiet_zone must be an int", b.Name())
			}
			quietZone = int(i)
		default:
			params[name] = starlarkToString(kv[1])
		}
	}

	content, err := templateContent(kind, params)
	if err != nil {
		return none, err
	}
	if quietZone < 0 {
		return none, fmt.Errorf("%s: quiet_zone must not be negative", b.Name())
	}
	ecl, err := resolveECLevel(level)
	if err != nil {
		return none, fmt.Errorf("%s: %w", b.Name(), err)
	}
	matrix, err := encodeMatrix(content, ecl)
	if err != nil {
		return none, err
	}
	return &qrValue{matrix: matrix, quietZone: quietZone, maxOutput: m.maxOutput()}, nil
}

// starlarkToString renders a keyword value as a plain string (unquoting a
// String; otherwise its Starlark repr, e.g. 30 -> "30", True -> "True").
func starlarkToString(v starlark.Value) string {
	if s, ok := starlark.AsString(v); ok {
		return s
	}
	return v.String()
}

// templateContent assembles the wire content for a format template, validating
// required parameters and escaping per each format's rules. It performs no I/O
// and is unit-testable.
func templateContent(kind string, p map[string]string) (string, error) {
	required := func(keys ...string) error {
		for _, k := range keys {
			if strings.TrimSpace(p[k]) == "" {
				return fmt.Errorf("qrcode.template %q: missing required parameter %q (want: %s)", kind, k, strings.Join(templateParams[kind], ", "))
			}
		}
		return nil
	}
	switch kind {
	case "url":
		if err := required("url"); err != nil {
			return "", err
		}
		return p["url"], nil
	case "tel":
		if err := required("number"); err != nil {
			return "", err
		}
		return "tel:" + p["number"], nil
	case "sms":
		if err := required("number"); err != nil {
			return "", err
		}
		return "SMSTO:" + p["number"] + ":" + p["message"], nil
	case "geo":
		if err := required("lat", "lng"); err != nil {
			return "", err
		}
		return "geo:" + p["lat"] + "," + p["lng"], nil
	case "email":
		if err := required("to"); err != nil {
			return "", err
		}
		q := url.Values{}
		if p["subject"] != "" {
			q.Set("subject", p["subject"])
		}
		if p["body"] != "" {
			q.Set("body", p["body"])
		}
		s := "mailto:" + p["to"]
		if len(q) > 0 {
			s += "?" + q.Encode()
		}
		return s, nil
	case "wifi":
		if err := required("ssid"); err != nil {
			return "", err
		}
		security := p["security"]
		if security == "" {
			security = "WPA"
		}
		hidden := "false"
		if b := p["hidden"]; b == "true" || b == "True" || b == "1" {
			hidden = "true"
		}
		return fmt.Sprintf("WIFI:T:%s;S:%s;P:%s;H:%s;;",
			wifiEscape(security), wifiEscape(p["ssid"]), wifiEscape(p["password"]), hidden), nil
	case "vcard":
		if err := required("name"); err != nil {
			return "", err
		}
		var b strings.Builder
		b.WriteString("MECARD:N:" + mecardEscape(p["name"]) + ";")
		for _, f := range []struct{ tag, key string }{
			{"TEL", "phone"}, {"EMAIL", "email"}, {"ORG", "org"}, {"URL", "url"},
		} {
			if v := p[f.key]; v != "" {
				b.WriteString(f.tag + ":" + mecardEscape(v) + ";")
			}
		}
		b.WriteString(";")
		return b.String(), nil
	default:
		kinds := make([]string, 0, len(templateParams))
		for k := range templateParams {
			kinds = append(kinds, k)
		}
		sort.Strings(kinds)
		return "", fmt.Errorf("qrcode.template: unknown kind %q (supported: %s)", kind, strings.Join(kinds, ", "))
	}
}

// templateParams documents each kind's parameters (required first). Used in
// error messages and mirrored in the README.
var templateParams = map[string][]string{
	"url":   {"url"},
	"email": {"to", "subject", "body"},
	"wifi":  {"ssid", "password", "security", "hidden"},
	"tel":   {"number"},
	"sms":   {"number", "message"},
	"geo":   {"lat", "lng"},
	"vcard": {"name", "phone", "email", "org", "url"},
}

// wifiEscape backslash-escapes the characters special to the WIFI: format.
func wifiEscape(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `;`, `\;`, `,`, `\,`, `:`, `\:`, `"`, `\"`)
	return r.Replace(s)
}

// mecardEscape backslash-escapes the characters special to the MECARD: format.
func mecardEscape(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `;`, `\;`, `:`, `\:`, `,`, `\,`)
	return r.Replace(s)
}
