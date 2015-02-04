package scheduler

import (
	"github.com/gogo/protobuf/proto"
	mesos "github.com/mesos/mesos-go/mesosproto"
)

// create a range resource for the listed ports
func rangeResource(name string, ports []uint64) *mesos.Resource {
	if len(ports) == 0 {
		// pod may consist of a container that doesn't expose any ports on the host
		return nil
	}
	return &mesos.Resource{
		Name:   proto.String(name),
		Type:   mesos.Value_RANGES.Enum(),
		Ranges: newRanges(ports),
	}
}

// generate port ranges from a list of ports. this implementation is very naive
func newRanges(ports []uint64) *mesos.Value_Ranges {
	r := make([]*mesos.Value_Range, 0)
	for _, port := range ports {
		x := proto.Uint64(port)
		r = append(r, &mesos.Value_Range{Begin: x, End: x})
	}
	return &mesos.Value_Ranges{Range: r}
}

func newTaskID(id string) *mesos.TaskID {
	return &mesos.TaskID{Value: proto.String(id)}
}

func newTaskInfo(name string) *mesos.TaskInfo {
	return &mesos.TaskInfo{
		Name: proto.String(name),
	}
}

func newOfferID(id string) *mesos.OfferID {
	return &mesos.OfferID{Value: proto.String(id)}
}
