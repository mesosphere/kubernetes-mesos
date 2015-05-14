package uid

import (
	"testing"
)

func TestUID_Parse(t *testing.T) {
	valid := []string{"1234567890abcdef_foo", "123_bar", "face_time"}
	groups := []uint64{0x1234567890abcdef, 0x123, 0xface}

	for i, good := range valid {
		u := Parse(good)
		if u == nil {
			t.Errorf("expected parsed UID, not nil")
		}
		if groups[i] != u.Group() {
			t.Errorf("expected matching group instead of %x", u.Group())
		}
		if good != u.String() {
			t.Errorf("expected %q instead of %q", good, u.String())
		}
	}

	invalid := []string{"", "bad"}
	for _, bad := range invalid {
		u := Parse(bad)
		if u != nil {
			t.Errorf("expected nil UID instead of %v", u)
		}
	}
}
