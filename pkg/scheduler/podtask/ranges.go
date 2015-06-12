package podtask

import (
	"sort"

	"github.com/gogo/protobuf/proto"
	mesos "github.com/mesos/mesos-go/mesosproto"
)

// Range is an all-inclusive range defined by a lower and upper bound.
type Range [2]uint64

// Ranges represents a list of Ranges.
type Ranges []Range

// NewRanges returns squashed Ranges from the given numbers.
func NewRanges(ns ...uint64) Ranges {
	xs := append(uint64s{}, ns...)
	sort.Sort(xs)
	rs := make(Ranges, len(xs))
	for i := range xs {
		rs[i][0], rs[i][1] = xs[i], xs[i]
	}
	return rs.Squash()
}

// NewPortRanges returns Ranges from the "ports" resource in the
// given *mesos.Offer. If that resource isn't provided, nil will be returned.
//
// The returned Ranges are sorted and have all overlapping ranges merged from
// left to right. e.g. [[0, 5], [4, 3], [10, 7]] -> [[0, 5], [7, 10]]
func NewPortRanges(o *mesos.Offer) Ranges {
	if o == nil {
		return Ranges{}
	}

	var r *mesos.Resource
	for i := range o.Resources {
		if o.Resources[i].GetName() == "ports" {
			r = o.Resources[i]
			break
		}
	}

	if r == nil {
		return Ranges{}
	}

	offered := r.GetRanges().GetRange()
	rs := make(Ranges, len(offered))
	for i, r := range offered {
		if lo, hi := r.GetBegin(), r.GetEnd(); lo <= hi {
			rs[i][0], rs[i][1] = lo, hi
		} else {
			rs[i][0], rs[i][1] = hi, lo
		}
	}
	sort.Sort(rs)

	return rs.Squash()
}

// These three methods implement sort.Interface
func (rs Ranges) Len() int           { return len(rs) }
func (rs Ranges) Less(i, j int) bool { return rs[i][0] < rs[j][0] && rs[i][1] < rs[j][1] }
func (rs Ranges) Swap(i, j int)      { rs[i], rs[j] = rs[j], rs[i] }

// Size returns the sum of the Size of all Ranges.
func (rs Ranges) Size() uint64 {
	var sz uint64
	for i := range rs {
		sz += 1 + (rs[i][1] - rs[i][0])
	}
	return sz
}

// Squash merges overlapping and continuous Ranges. It assumes they're pre-sorted.
func (rs Ranges) Squash() Ranges {
	if len(rs) < 2 {
		return rs
	}
	squashed := Ranges{rs[0]}
	for i := 1; i < len(rs); i++ {
		switch max := squashed[len(squashed)-1][1]; {
		case 1+max < rs[i][0]: // no overlap nor continuity: push
			squashed = append(squashed, rs[i])
		case max <= rs[i][1]: // overlap or continuity: squash
			squashed[len(squashed)-1][1] = rs[i][1]
		}
	}
	return squashed
}

// Search performs a binary search for n returning the index of the Range it was
// found at or -1 if not found.
func (rs Ranges) Search(n uint64) int {
	for lo, hi := 0, len(rs)-1; lo <= hi; {
		switch m := lo + (hi-lo)/2; {
		case n < rs[m][0]:
			hi = m - 1
		case n > rs[m][1]:
			lo = m + 1
		default:
			return m
		}
	}
	return -1
}

// Partition partitions Ranges around n. It returns the partitioned Ranges
// and a boolean indicating if n was found.
func (rs Ranges) Partition(n uint64) (Ranges, bool) {
	i := rs.Search(n)
	if i < 0 {
		return rs, false
	}

	pn := make(Ranges, 0, len(rs)+1)
	switch pn = append(pn, rs[:i]...); {
	case rs[i][0] == rs[i][1]: // delete
	case rs[i][0] == n: // increment lower bound
		pn = append(pn, Range{rs[i][0] + 1, rs[i][1]})
	case rs[i][1] == n: // decrement upper bound
		pn = append(pn, Range{rs[i][0], rs[i][1] - 1})
	default: // split
		pn = append(pn, Range{rs[i][0], n - 1}, Range{n + 1, rs[i][1]})
	}
	return append(pn, rs[i+1:]...), true
}

// Min returns the minimum number in Ranges. It will panic on empty Ranges.
func (rs Ranges) Min() uint64 { return rs[0][0] }

// Max returns the maximum number in Ranges. It will panic on empty Ranges.
func (rs Ranges) Max() uint64 { return rs[len(rs)-1][1] }

// resource returns a *mesos.Resource with the given name and Ranges.
func (rs Ranges) resource(name string) *mesos.Resource {
	vr := make([]*mesos.Value_Range, len(rs))
	for i := range rs {
		vr[i] = &mesos.Value_Range{
			Begin: proto.Uint64(rs[i][0]),
			End:   proto.Uint64(rs[i][1]),
		}
	}
	return &mesos.Resource{
		Name:   proto.String(name),
		Type:   mesos.Value_RANGES.Enum(),
		Ranges: &mesos.Value_Ranges{Range: vr},
	}
}

// uint64s is an utility used to sort a slice of uint64s
type uint64s []uint64

// These three methods implement sort.Interface
func (ns uint64s) Len() int           { return len(ns) }
func (ns uint64s) Less(i, j int) bool { return ns[i] < ns[j] }
func (ns uint64s) Swap(i, j int)      { ns[i], ns[j] = ns[j], ns[i] }
