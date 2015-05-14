package uid

import (
	"fmt"
	"strconv"
	"strings"

	"code.google.com/p/go-uuid/uuid"
	log "github.com/golang/glog"
)

type UID struct {
	group uint64
	name  string
	ser   string
}

func New(group uint64, name string) *UID {
	if name == "" {
		name = uuid.New()
	}
	return &UID{
		group: group,
		name:  name,
		ser:   fmt.Sprintf("%x_%s", group, name),
	}
}

func (self *UID) Name() string {
	if self != nil {
		return self.name
	}
	return ""
}

func (self *UID) Group() uint64 {
	if self != nil {
		return self.group
	}
	return 0
}

func (self *UID) String() string {
	if self != nil {
		return self.ser
	}
	return ""
}

func Parse(ser string) *UID {
	parts := strings.SplitN(ser, "_", 2)
	if len(parts) != 2 {
		return nil
	}
	group, err := strconv.ParseUint(parts[0], 16, 64)
	if err != nil {
		log.Errorf("illegal UID group %q: %v", parts[0], err)
		return nil
	}
	if parts[1] == "" {
		log.Errorf("missing UID name: %q", ser)
		return nil
	}
	return &UID{
		group: group,
		name:  parts[1],
		ser:   ser,
	}
}
