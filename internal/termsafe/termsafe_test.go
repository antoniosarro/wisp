package termsafe

import (
	"encoding/json"
	"testing"
)

func TestStrip(t *testing.T) {
	for _, c := range []struct {
		name string
		in   string
		want string
	}{
		{"plain text", "hello", "hello"},
		{"ansi escape", "hello\x1b[31mworld\x1b[0m", "helloworld"},
		{"crnl normalized", "line1\r\nline2", "line1\nline2"},
		{"control removed", "he\x00llo", "hello"},
		{"tab preserved", "a\tb", "a\tb"},
		{"newline preserved", "a\nb", "a\nb"},
		{"mixed", "start\x1b[1m\x00mid\r\nend", "startmid\nend"},
		{"osc 52 bel", "a\x1b]52;c;SGVsbG8=\x07b", "ab"},
		{"osc title st", "a\x1b]0;title\x1b\\b", "ab"},
		{"lone cr removed", "safe\rrm -rf", "saferm -rf"},
		{"c1 control removed", "a\u0085b", "ab"},
		{"8-bit csi stripped", "a\x9b31mb", "ab"},
		{"invalid byte removed", "a\xffb", "ab"},
		{"valid replacement char kept", "a\ufffdb", "a\ufffdb"},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := Strip(c.in)
			if got != c.want {
				t.Errorf("Strip(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestShow(t *testing.T) {
	for _, c := range []struct {
		name string
		in   string
		want string
	}{
		{"plain text", "hello", "hello"},
		{"ctrl 0x00", string([]rune{0x00}), "␀"},
		{"ctrl 0x1f", string([]rune{0x1f}), "␟"},
		{"del 0x7f", string([]rune{0x7f}), "␡"},
		{"esc 0x1b", "\x1b[31m", "␛[31m"},
		{"cr 0x0d", "a\rb", "a␍b"},
		{"ctrl 0x80", string([]rune{0x80}), "�"},
		{"ctrl 0x9f", string([]rune{0x9f}), "�"},
		{"invalid byte 0x9b", "a\x9bb", "a�b"},
		{"invalid byte 0x80", "\x80", "�"},
		{"nbsp 0xa0 kept", "a\u00a0b", "a\u00a0b"},
		{"tab preserved", "a\tb", "a\tb"},
		{"newline preserved", "a\nb", "a\nb"},
		{"mixed", "a\x00b\x7fc\u0080d", "a␀b␡c�d"},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := Show(c.in)
			if got != c.want {
				t.Errorf("Show(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestJSON(t *testing.T) {
	for _, c := range []struct {
		name string
		in   json.RawMessage
		want string
	}{
		{"empty", json.RawMessage{}, ""},
		{"plain json", json.RawMessage(`{"a":1}`), `{"a":1}`},
		{"null escape", json.RawMessage(`{"msg":"he\u0000llo"}`), `{"msg":"hello"}`},
		{"nested array", json.RawMessage(`{"items":["a\u0000b","c"]}`), `{"items":["ab","c"]}`},
		{"keys cleaned", json.RawMessage(`{"ke\u0000y":"val"}`), `{"key":"val"}`},
		{"malformed json", json.RawMessage(`{"broken\u0000`), `{"broken\u0000`},
		{"escaped cr", json.RawMessage(`{"msg":"he\rllo"}`), `{"msg":"hello"}`},
		{"escaped esc", json.RawMessage(`{"msg":"\u001b[31mred\u001b[0m"}`), `{"msg":"red"}`},
		{"raw cr is malformed", json.RawMessage("{\"msg\":\"he\rllo\"}"), `{"msg":"hello"}`},
		{"trailing data is malformed", json.RawMessage(`{"a":"\u0000"}}`), `{"a":"\u0000"}}`},
		{"large int kept exact", json.RawMessage(`{"n":12345678901234567890,"s":"\u0000"}`), `{"n":12345678901234567890,"s":""}`},
		{"html not escaped", json.RawMessage(`{"s":"<a>&\u0000"}`), `{"s":"<a>&"}`},
		{"keys sorted", json.RawMessage(`{"b":"\u0000","a":1}`), `{"a":1,"b":""}`},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := JSON(c.in, Strip)
			if string(got) != c.want {
				t.Errorf("JSON(%s, clean) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestHasControl(t *testing.T) {
	if hasControl("hello") {
		t.Error("hasControl(\"hello\") = true, want false")
	}
	if !hasControl("he\x00llo") {
		t.Error("hasControl(\"he\\x00llo\") = false, want true")
	}
}

func TestIsControl(t *testing.T) {
	for _, c := range []struct {
		r    rune
		want bool
	}{
		{'a', false},
		{'\n', false},
		{'\t', false},
		{0x00, true},
		{0x1b, true},
		{0x1f, true},
		{0x20, false},
		{0x7e, false},
		{0x7f, true},
		{0x80, true},
		{0x9f, true},
		{0xa0, false},
	} {
		if got := isControl(c.r); got != c.want {
			t.Errorf("isControl(%U) = %v, want %v", c.r, got, c.want)
		}
	}
}
