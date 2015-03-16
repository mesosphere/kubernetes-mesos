package ha

import (
	"sync/atomic"

	log "github.com/golang/glog"
	mesos "github.com/mesos/mesos-go/mesosproto"
	bindings "github.com/mesos/mesos-go/scheduler"
	"github.com/mesosphere/kubernetes-mesos/pkg/proc"
)

type stageType int32

const (
	initStage stageType = iota
	standbyStage
	masterStage
	finStage
)

func (stage *stageType) transition(from, to stageType) bool {
	return atomic.CompareAndSwapInt32((*int32)(stage), int32(from), int32(to))
}

func (stage *stageType) set(to stageType) {
	atomic.StoreInt32((*int32)(stage), int32(to))
}

func (stage *stageType) get() stageType {
	return stageType(atomic.LoadInt32((*int32)(stage)))
}

type SchedulerProcess struct {
	proc.Process
	bindings.Scheduler
	stage   stageType
	elected chan struct{} // upon close we've been elected
}

func New(sched bindings.Scheduler) *SchedulerProcess {
	p := &SchedulerProcess{
		Process:   proc.New(),
		Scheduler: sched,
		stage:     initStage,
		elected:   make(chan struct{}),
	}
	return p
}

func (stage stageType) DoLater(p *SchedulerProcess, a proc.Action) {
	p.DoLater(proc.Action(func() {
		if stage != (&p.stage).get() {
			log.Errorf("failed to execute deferred action, expected lifecycle stage %v instead of %v", stage, p.stage)
			return
		}
		a()
	}))
}

func (self *SchedulerProcess) Begin() {
	if (&self.stage).transition(initStage, standbyStage) {
		log.Infoln("scheduler process entered standby stage")
		self.Process.Begin()
		//TODO(jdef) start election listener
	} else {
		log.Errorf("failed to transition from init to standby stage")
	}
}

func (self *SchedulerProcess) End() {
	defer self.Process.End()
	(&self.stage).set(finStage)
	log.Infoln("scheduler process entered fin stage")
}

func (self *SchedulerProcess) Elect(drv bindings.SchedulerDriver) {
	standbyStage.DoLater(self, proc.Action(func() {
		if !(&self.stage).transition(standbyStage, masterStage) {
			log.Errorf("failed to transition from standby to master stage, aborting")
			self.End()
			return
		}
		log.Infoln("scheduler process entered master stage")
		stat, err := drv.Start()
		if stat == mesos.Status_DRIVER_RUNNING && err == nil {
			close(self.elected)
			go func() {
				defer self.End()
				_, err := drv.Join()
				if err != nil {
					log.Errorf("driver failed with error: %v", err)
				}
			}()
			return
		}
		if err != nil {
			log.Errorf("failed to start scheduler driver: %v", err)
		} else {
			log.Errorf("expected RUNNING status, not %v", stat)
		}
		self.End()
	}))
}

func (self *SchedulerProcess) Elected() <-chan struct{} {
	return self.elected
}

func (self *SchedulerProcess) Registered(drv bindings.SchedulerDriver, fid *mesos.FrameworkID, mi *mesos.MasterInfo) {
	masterStage.DoLater(self, proc.Action(func() {
		self.Scheduler.Registered(drv, fid, mi)
	}))
}

func (self *SchedulerProcess) Reregistered(drv bindings.SchedulerDriver, mi *mesos.MasterInfo) {
	masterStage.DoLater(self, proc.Action(func() {
		self.Scheduler.Reregistered(drv, mi)
	}))
}

func (self *SchedulerProcess) Disconnected(drv bindings.SchedulerDriver) {
	masterStage.DoLater(self, proc.Action(func() {
		self.Scheduler.Disconnected(drv)
	}))
}

func (self *SchedulerProcess) ResourceOffers(drv bindings.SchedulerDriver, off []*mesos.Offer) {
	masterStage.DoLater(self, proc.Action(func() {
		self.Scheduler.ResourceOffers(drv, off)
	}))
}

func (self *SchedulerProcess) OfferRescinded(drv bindings.SchedulerDriver, oid *mesos.OfferID) {
	masterStage.DoLater(self, proc.Action(func() {
		self.Scheduler.OfferRescinded(drv, oid)
	}))
}

func (self *SchedulerProcess) StatusUpdate(drv bindings.SchedulerDriver, ts *mesos.TaskStatus) {
	masterStage.DoLater(self, proc.Action(func() {
		self.Scheduler.StatusUpdate(drv, ts)
	}))
}

func (self *SchedulerProcess) FrameworkMessage(drv bindings.SchedulerDriver, eid *mesos.ExecutorID, sid *mesos.SlaveID, m string) {
	masterStage.DoLater(self, proc.Action(func() {
		self.Scheduler.FrameworkMessage(drv, eid, sid, m)
	}))
}

func (self *SchedulerProcess) SlaveLost(drv bindings.SchedulerDriver, sid *mesos.SlaveID) {
	masterStage.DoLater(self, proc.Action(func() {
		self.Scheduler.SlaveLost(drv, sid)
	}))
}

func (self *SchedulerProcess) ExecutorLost(drv bindings.SchedulerDriver, eid *mesos.ExecutorID, sid *mesos.SlaveID, x int) {
	masterStage.DoLater(self, proc.Action(func() {
		self.Scheduler.ExecutorLost(drv, eid, sid, x)
	}))
}

func (self *SchedulerProcess) Error(drv bindings.SchedulerDriver, msg string) {
	masterStage.DoLater(self, proc.Action(func() {
		self.Scheduler.Error(drv, msg)
	}))
}
