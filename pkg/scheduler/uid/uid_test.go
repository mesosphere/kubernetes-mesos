package uid

import (
	"testing"
)

func TestUID_New(t *testing.T) {
	uid := New(uint64(42), "fake-name")
	uid.Name()
}

func TestUID_Parse(t *testing.T) {
	valid := []string{"1234567890abcdef_foo", "123_bar", "face_time"}
	groups := []uint64{0x1234567890abcdef, 0x123, 0xface}

	for i, good := range valid {
		u, err := Parse(good)
		if err != nil {
			t.Errorf("expected Parse not to error, but received %v", err)
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
		_, err := Parse(bad)
		if err == nil {
			t.Errorf("expected Parse(%q) to error, but received none", bad)
		}
	}
}

func TestUID_Generate(t *testing.T) {
	group := uint64(0x1234567890abcdef)
	uid := Generate(group)

	if uid.Group() != group {
		t.Errorf("expected matching group, but received %x", uid.Group())
	}

	uid2 := Generate(group)
	if uid2 == uid {
		t.Errorf("expected sequentially generated UIDs not to match, but %v == %v", uid, uid2)
	}
	// much more testing is possible for a uuid, but we'll skip it because we're delegating to a well known library
}
