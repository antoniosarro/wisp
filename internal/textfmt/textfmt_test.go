package textfmt

import "testing"

func TestPrefixKeepsCharactersWhole(t *testing.T) {
	for _, c := range []struct {
		s    string
		n    int
		want string
	}{
		{"abc", 5, "abc"},
		{"abc", 2, "ab"},
		{"aé", 2, "a"}, // é is two bytes
		{"aé", 3, "aé"},
		{"日本", 4, "日"},
		{"日本", 0, ""},
	} {
		if got := Prefix(c.s, c.n); got != c.want {
			t.Errorf("Prefix(%q, %d) = %q, want %q", c.s, c.n, got, c.want)
		}
	}
	if got := Cut("héllo", 2); got != "h…" {
		t.Errorf("Cut = %q", got)
	}
	if got := CutRunes("héllo", 3); got != "hé…" {
		t.Errorf("CutRunes = %q", got)
	}
}

func TestSize(t *testing.T) {
	for n, want := range map[int64]string{512: "512 B", 1536: "1.5 KB", 3 << 20: "3.0 MB"} {
		if got := Size(n); got != want {
			t.Errorf("Size(%d) = %q, want %q", n, got, want)
		}
	}
}
