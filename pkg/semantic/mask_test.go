package semantic

import "testing"

func TestMask(t *testing.T) {
	in := "Failed password for bob@example.com from 173.234.31.186 key=sk-abcdefghijklmnop1234"
	want := "Failed password for <email> from <ip> key=<token>"
	if got := Mask(in); got != want {
		t.Fatalf("Mask = %q, want %q", got, want)
	}
}
