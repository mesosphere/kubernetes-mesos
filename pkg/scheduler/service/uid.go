package service

import (
	"fmt"
	"strconv"
	"strings"

	"code.google.com/p/go-uuid/uuid"
	log "github.com/golang/glog"
)

type UID struct {
	group uint64
	ser   string
}

func NewUID(group uint64) *UID {
	return &UID{
		group: group,
		ser:   fmt.Sprintf("%x_%s", group, uuid.New()),
	}
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

func ParseUID(ser string) *UID {
	parts := strings.SplitN(ser, "_", 2)
	if len(parts) != 2 {
		return nil
	}
	group, err := strconv.ParseUint(parts[0], 16, 64)
	if err != nil {
		log.Errorf("illegal UID group %q: %v", parts[0], err)
		return nil
	}
	return &UID{
		group: group,
		ser:   ser,
	}
}
