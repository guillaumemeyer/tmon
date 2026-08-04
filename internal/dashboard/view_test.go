package dashboard

import "testing"

func TestDisplayCWD(t *testing.T) {
	t.Setenv("HOME", "/home/u")

	cases := []struct {
		in, want string
	}{
		{"/home/u/code/tmon", "~/code/tmon"}, // under home → home-relative
		{"/home/u", "~"},                     // home dir itself → "~"
		{"/var/www", "/var/www"},             // outside home → unchanged
		{"/", "/"},                           // root → unchanged
		{"code/tmon", "code/tmon"},           // short form → unchanged
		{"?", "?"},                           // unknown → unchanged
		{"", ""},                             // empty → unchanged
	}
	for _, c := range cases {
		if got := displayCWD(c.in); got != c.want {
			t.Errorf("displayCWD(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
