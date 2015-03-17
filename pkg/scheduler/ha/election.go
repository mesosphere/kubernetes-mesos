package ha

import (
	log "github.com/golang/glog"
	"github.com/mesosphere/kubernetes-mesos/pkg/election"
)

type roleType int

const (
	followerRole roleType = iota
	masterRole
	retiredRole
)

type candidateService struct {
	sched     *SchedulerProcess
	newDriver DriverFactory
	role      roleType
}

func NewCandidate(s *SchedulerProcess, f DriverFactory) election.Service {
	return &candidateService{
		sched:     s,
		newDriver: f,
		role:      followerRole,
	}
}

func (self *candidateService) Start() {
	if self.role == followerRole {
		log.Info("elected as master")
		self.role = masterRole
		self.sched.Elect(self.newDriver)
	}
}

func (self *candidateService) Stop() {
	if self.role == masterRole {
		log.Info("retiring from master")
		self.role = retiredRole
		// order is important here, watchers of a SchedulerProcess will
		// check SchedulerProcess.Failover() once Done() is closed.
		close(self.sched.failover)
		self.sched.End()
	}
}
