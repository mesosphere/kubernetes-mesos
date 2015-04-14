package constraint

import (
	"math"
	"strconv"

	"github.com/mesosphere/kubernetes-mesos/pkg/offers"
	"github.com/mesosphere/kubernetes-mesos/pkg/scheduler/podtask"
)

// inspired by https://github.com/mesosphere/marathon/blob/55a637d54cb60e853f7c000dbc4d71c9f5591448/src/main/scala/mesosphere/mesos/Constraints.scala

type Checker struct {
	GroupByDefault uint64
}

var (
	defaultChecker Checker
)

// return true if the offer meets the specified constraint given the currently running tasks
func Check(tasks []podtask.T, offer offers.Perishable, c *Constraint) bool {
	return defaultChecker.Check(tasks, offer, c)
}

// return true if the offer meets the specified constraint given the currently running tasks
func (self *Checker) Check(tasks []podtask.T, offer offers.Perishable, c *Constraint) bool {
	//TODO(jdef) implement attribute matching for things besides hostname
	switch {
	case c.Field == "hostname":
		return self.checkHostName(tasks, offer, c)
	default:
		// this will be reached in case we want to schedule for an attribute
		// that's not supplied. AKA check missing attribute
		return c.Operator == UnlikeOperator
	}
}

// return true if all tasks pass the filter func, otherwise false
func forall(tasks []podtask.T, filter func(task *podtask.T) bool) bool {
	for i := range tasks {
		if !filter(&tasks[i]) {
			return false
		}
	}
	return true
}

func getIntValue(s string, def uint64) uint64 {
	if s == "inf" {
		return math.MaxUint64
	}
	if x, err := strconv.ParseUint(s, 10, 64); err != nil {
		return def
	} else {
		return x
	}
}

// groupFunc returns a value that's used to group tasks together.
// returns a map of group-value to group-size
func groupCounts(tasks []podtask.T, groupFunc func(t *podtask.T) string) map[string]int {
	grouped := make(map[string]int)
	for i := range tasks {
		t := &tasks[i]
		g := groupFunc(t)
		v := grouped[g]
		grouped[g] = v + 1
	}
	return grouped
}

func (self *Checker) checkGroupBy(tasks []podtask.T, constraintValue string, groupFunc func(t *podtask.T) string) bool {
	// minimum group count
	min := getIntValue(constraintValue, self.GroupByDefault)
	if self.GroupByDefault > min {
		min = self.GroupByDefault
	}

	// group by func and get task count of the smallest group
	grouped := groupCounts(tasks, groupFunc)
	minCount := -1
	for _, v := range grouped {
		if sz := v; minCount < 0 || sz < minCount {
			minCount = sz
		}
	}
	if minCount < 0 {
		minCount = 0
	}

	if count, found := grouped[constraintValue]; found {
		// this offer matches the smallest grouping when there are >= min groupings
		sz := len(grouped)
		return uint64(sz) >= min && count == minCount
	} else {
		// constraint value from the offer was not grouped (matches no running tasks)
		return true
	}
}

//TODO(jdef) we need to update task.Pod.Status.Host when
//we see updates from either apiserver or from StatusUpdate
func (self *Checker) checkHostName(tasks []podtask.T, offer offers.Perishable, c *Constraint) bool {
	h := offer.Host()
	switch c.Operator {
	case LikeOperator:
		return h == c.Value
	case UnlikeOperator:
		return h != c.Value
	case UniqueOperator:
		return forall(tasks, func(t *podtask.T) bool { return t.Pod.Status.Host != h })
	case GroupByOperator:
		return self.checkGroupBy(tasks, h, func(t *podtask.T) string { return t.Pod.Status.Host })
	case ClusterOperator:
		// hostname must match or be empty
		return (c.Value == "" || c.Value == h) &&
			// all running tasks must have the same hostname as the one in the offer
			forall(tasks, func(t *podtask.T) bool { return t.Pod.Status.Host == h })
	default:
		return false
	}
}
