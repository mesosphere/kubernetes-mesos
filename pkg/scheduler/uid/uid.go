package uid

import (
	"fmt"
	"strconv"
	"strings"
	"errors"

	"code.google.com/p/go-uuid/uuid"
)

type UID struct {
	group uint64
	name  string
	ser   string
}

func New(group uint64, name string) UID {
	return UID{
		group: group,
		name:  name,
		ser:   fmt.Sprintf("%x_%s", group, name),
	}
}

func (u UID) Name() string {
	return u.name
}

func (u UID) Group() uint64 {
	return u.group
}

func (u UID) String() string {
	return u.ser
}

func Parse(ser string) (UID, error) {
	parts := strings.SplitN(ser, "_", 2)
	if len(parts) != 2 {
		return UID{}, errors.New("invalid UID format (expected <uint64>_<string>)")
	}
	group, err := strconv.ParseUint(parts[0], 16, 64)
	if err != nil {
		return UID{}, fmt.Errorf("invalid UID group %q: %v", parts[0], err)
	}
	name := parts[1]
	if name == "" {
		return UID{}, fmt.Errorf("invalid UID name %q (expected non-empty)", name)
	}
	return UID{
		group: group,
		name:  name,
		ser:   ser,
	}, nil
}

func Generate(group uint64) UID {
	return New(group, uuid.New())
}
