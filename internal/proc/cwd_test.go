package proc

import "testing"

func TestCWDShort(t *testing.T) {
	cases := []struct{ in, want string }{
		{"/a/b/c/d", "c/d"},
		{"/a/b", "a/b"},
		{"/a", "a"},
		{"/", "/"},
		{"", "/"},
	}
	for _, tc := range cases {
		if got := CWDShort(tc.in); got != tc.want {
			t.Errorf("CWDShort(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
