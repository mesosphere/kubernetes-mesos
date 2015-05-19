package podtask

import (
	"reflect"
	"testing"

	mesos "github.com/mesos/mesos-go/mesosproto"
)

func TestNewPortRanges(t *testing.T) {
	t.Parallel()

	for i, tt := range []struct {
		Ranges
		want Ranges
	}{
		{Ranges{{2, 0}, {3, 10}}, Ranges{{0, 10}}},
		{Ranges{{0, 2}, {4, 10}}, Ranges{{0, 2}, {4, 10}}},
		{Ranges{{10, 0}}, Ranges{{0, 10}}},
		{Ranges{}, Ranges{}},
		{nil, Ranges{}},
	} {
		offer := &mesos.Offer{Resources: []*mesos.Resource{tt.resource("ports")}}
		if got := NewPortRanges(offer); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("test #%d, %v: got: %v, want: %v", i, tt.Ranges, got, tt.want)
		}
	}
}

func TestRanges_Squash(t *testing.T) {
	t.Parallel()

	for i, tt := range []struct {
		Ranges
		want Ranges
	}{
		{Ranges{}, Ranges{}},
		{Ranges{{0, 1}}, Ranges{{0, 1}}},
		{Ranges{{0, 2}, {1, 5}, {2, 10}}, Ranges{{0, 10}}},
		{Ranges{{0, 2}, {2, 5}, {5, 10}}, Ranges{{0, 10}}},
		{Ranges{{0, 2}, {3, 5}, {6, 10}}, Ranges{{0, 10}}},
		{Ranges{{0, 2}, {4, 11}, {6, 10}}, Ranges{{0, 2}, {4, 11}}},
		{Ranges{{0, 2}, {4, 5}, {6, 7}, {8, 10}}, Ranges{{0, 2}, {4, 10}}},
		{Ranges{{0, 2}, {4, 6}, {8, 10}}, Ranges{{0, 2}, {4, 6}, {8, 10}}},
		{Ranges{{0, 1}, {2, 5}, {4, 8}}, Ranges{{0, 8}}},
	} {
		if got := tt.Squash(); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("test #%d, Squash(%v): got: %v, want: %v", i, tt.Ranges, got, tt.want)
		}
	}
}

func TestRanges_Find(t *testing.T) {
	t.Parallel()

	for i, tt := range []struct {
		Ranges
		n    uint64
		want int
	}{
		{Ranges{{0, 2}, {3, 5}, {7, 10}}, 0, 0},
		{Ranges{{0, 2}, {3, 5}, {7, 10}}, 1, 0},
		{Ranges{{0, 2}, {3, 5}, {7, 10}}, 2, 0},
		{Ranges{{0, 2}, {3, 5}, {7, 10}}, 3, 1},
		{Ranges{{0, 2}, {3, 5}, {7, 10}}, 4, 1},
		{Ranges{{0, 2}, {3, 5}, {7, 10}}, 5, 1},
		{Ranges{{0, 2}, {3, 5}, {7, 10}}, 6, -1},
		{Ranges{{0, 2}, {3, 5}, {7, 10}}, 7, 2},
		{Ranges{{0, 2}, {3, 5}, {7, 10}}, 8, 2},
		{Ranges{{0, 2}, {3, 5}, {7, 10}}, 9, 2},
		{Ranges{{0, 2}, {3, 5}, {7, 10}}, 10, 2},
		{Ranges{{0, 2}, {3, 5}, {7, 10}}, 11, -1},
	} {
		if got := tt.Find(tt.n); got != tt.want {
			t.Errorf("test #%d: Find(%v, %v): got: %v, want: %v", i, tt.Ranges, tt.n, got, tt.want)
		}
	}
}

func TestRanges_Partition(t *testing.T) {
	t.Parallel()

	for i, tt := range []struct {
		Ranges
		n     uint64
		want  Ranges
		found bool
	}{
		{Ranges{}, 0, Ranges{}, false},
		{Ranges{{0, 10}, {12, 20}}, 100, Ranges{{0, 10}, {12, 20}}, false},
		{Ranges{{0, 10}, {12, 20}}, 0, Ranges{{1, 10}, {12, 20}}, true},
		{Ranges{{0, 10}, {12, 20}}, 13, Ranges{{0, 10}, {12, 12}, {14, 20}}, true},
		{Ranges{{0, 10}, {12, 20}}, 5, Ranges{{0, 4}, {6, 10}, {12, 20}}, true},
		{Ranges{{0, 10}, {12, 20}}, 19, Ranges{{0, 10}, {12, 18}, {20, 20}}, true},
		{Ranges{{0, 10}, {12, 20}}, 10, Ranges{{0, 9}, {12, 20}}, true},
	} {
		if got, found := tt.Partition(tt.n); !reflect.DeepEqual(got, tt.want) || found != tt.found {
			t.Errorf("test #%d: Sub(%v, %v): got: (%v, %t), want: (%v, %t)", i, tt.Ranges, tt.n, got, found, tt.want, tt.found)
		}
	}
}

func TestRanges_MinMax(t *testing.T) {
	t.Parallel()

	for i, tt := range []struct {
		Ranges
		min, max uint64
	}{
		{Ranges{{1, 10}, {100, 1000}}, 1, 1000},
		{Ranges{{0, 10}, {12, 20}}, 0, 20},
		{Ranges{{5, 10}}, 5, 10},
		{Ranges{{0, 0}}, 0, 0},
	} {
		if got, want := tt.Min(), tt.min; got != want {
			t.Errorf("test #%d: Min(%v): got: %d, want: %d", i, tt.Ranges, got, want)
		}
		if got, want := tt.Max(), tt.max; got != want {
			t.Errorf("test #%d: Max(%v): got: %d, want: %d", i, tt.Ranges, got, want)
		}
	}
}
