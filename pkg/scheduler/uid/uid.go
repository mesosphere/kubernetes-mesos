package uid

import (
	"fmt"
	"strconv"
	"strings"

	log "github.com/golang/glog"

	"code.google.com/p/go-uuid/uuid"
)

type UID struct {
	group uint64
	name  string
	ser   string
}

func New(group uint64, name string) UID {
	if name == "" {
		log.Fatalf("name must not be empty")
	}
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
		return UID{}, fmt.Errorf("invalid UID %q - expected format <64bit-hex>_<string>", ser)
	}
	group, err := strconv.ParseUint(parts[0], 16, 64)
	if err != nil {
		return UID{}, fmt.Errorf("invalid UID %q - group must be 64bit-hex: %v", ser, err)
	}
	name := parts[1]
	if name == "" {
		return UID{}, fmt.Errorf("invalid UID %q - name must be non-empty", ser)
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
