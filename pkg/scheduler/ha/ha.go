package ha

import (
	"fmt"
	"sync/atomic"

	log "github.com/golang/glog"
	mesos "github.com/mesos/mesos-go/mesosproto"
	bindings "github.com/mesos/mesos-go/scheduler"
	"github.com/mesosphere/kubernetes-mesos/pkg/proc"
)

type DriverFactory func() (bindings.SchedulerDriver, error)

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

// execute some action in the deferred context of the process, but only if we
// match the stage of the process at the time the action is executed.
func (stage stageType) Do(p *SchedulerProcess, a proc.Action) <-chan error {
	err := proc.NewErrorOnce(p.Done())
	errOuter := p.Do(proc.Action(func() {
		err.Report(stage.When(p, a))
	}))
	go err.Forward(errOuter)
	return err.Err()
}

// execute some action only if we match the stage of the scheduler process
func (stage stageType) When(p *SchedulerProcess, a proc.Action) (err error) {
	if stage != (&p.stage).get() {
		err = fmt.Errorf("failed to execute deferred action, expected lifecycle stage %v instead of %v", stage, p.stage)
	} else {
		a()
	}
	return
}

type SchedulerProcess struct {
	proc.ProcessInit
	bindings.Scheduler
	stage    stageType
	elected  chan struct{} // upon close we've been elected
	failover chan struct{} // closed indicates that we should failover upon End()
}

func New(sched bindings.Scheduler) *SchedulerProcess {
	p := &SchedulerProcess{
		ProcessInit: proc.New(),
		Scheduler:   sched,
		stage:       initStage,
		elected:     make(chan struct{}),
		failover:    make(chan struct{}),
	}
	return p
}

func (self *SchedulerProcess) Begin() {
	if (&self.stage).transition(initStage, standbyStage) {
		log.Infoln("scheduler process entered standby stage")
		self.ProcessInit.Begin()
	} else {
		log.Errorf("failed to transition from init to standby stage")
	}
}

func (self *SchedulerProcess) End() {
	defer self.ProcessInit.End()
	(&self.stage).set(finStage)
	log.Infoln("scheduler process entered fin stage")
}

func (self *SchedulerProcess) Elect(newDriver DriverFactory) {
	errOnce := proc.NewErrorOnce(self.Done())
	errCh := standbyStage.Do(self, proc.Action(func() {
		if !(&self.stage).transition(standbyStage, masterStage) {
			log.Errorf("failed to transition from standby to master stage, aborting")
			self.End()
			return
		}
		log.Infoln("scheduler process entered master stage")
		drv, err := newDriver()
		if err != nil {
			log.Errorf("failed to fetch scheduler driver: %v", err)
			self.End()
			return
		}
		log.V(1).Infoln("starting driver...")
		stat, err := drv.Start()
		if stat == mesos.Status_DRIVER_RUNNING && err == nil {
			log.Infoln("driver started successfully and is running")
			close(self.elected)
			go func() {
				defer self.End()
				_, err := drv.Join()
				if err != nil {
					log.Errorf("driver failed with error: %v", err)
				}
				errOnce.Report(err)
			}()
			return
		}
		defer self.End()
		if err != nil {
			log.Errorf("failed to start scheduler driver: %v", err)
		} else {
			log.Errorf("expected RUNNING status, not %v", stat)
		}
	}))
	go errOnce.Forward(errCh)
	if err := <-errOnce.Err(); err != nil {
		defer self.End()
		log.Errorf("failed to handle election event, aborting: %v", err)
	}
}

func (self *SchedulerProcess) Elected() <-chan struct{} {
	return self.elected
}

func (self *SchedulerProcess) Failover() <-chan struct{} {
	return self.failover
}

// returns a Process instance that will only execute a proc.Action if the scheduler is the elected master
func (self *SchedulerProcess) Master() proc.Process {
	return proc.DoWith(self, proc.DoerFunc(func(a proc.Action) <-chan error {
		return proc.ErrorChan(masterStage.When(self, a))
	}))
}

func (self *SchedulerProcess) logError(ch <-chan error) {
	self.OnError(ch, func(err error) {
		log.Errorf("failed to execute scheduler action: %v", err)
	})
}

func (self *SchedulerProcess) Registered(drv bindings.SchedulerDriver, fid *mesos.FrameworkID, mi *mesos.MasterInfo) {
	self.logError(self.Master().Do(proc.Action(func() {
		self.Scheduler.Registered(drv, fid, mi)
	})))
}

func (self *SchedulerProcess) Reregistered(drv bindings.SchedulerDriver, mi *mesos.MasterInfo) {
	self.logError(self.Master().Do(proc.Action(func() {
		self.Scheduler.Reregistered(drv, mi)
	})))
}

func (self *SchedulerProcess) Disconnected(drv bindings.SchedulerDriver) {
	self.logError(self.Master().Do(proc.Action(func() {
		self.Scheduler.Disconnected(drv)
	})))
}

func (self *SchedulerProcess) ResourceOffers(drv bindings.SchedulerDriver, off []*mesos.Offer) {
	self.logError(self.Master().Do(proc.Action(func() {
		self.Scheduler.ResourceOffers(drv, off)
	})))
}

func (self *SchedulerProcess) OfferRescinded(drv bindings.SchedulerDriver, oid *mesos.OfferID) {
	self.logError(self.Master().Do(proc.Action(func() {
		self.Scheduler.OfferRescinded(drv, oid)
	})))
}

func (self *SchedulerProcess) StatusUpdate(drv bindings.SchedulerDriver, ts *mesos.TaskStatus) {
	self.logError(self.Master().Do(proc.Action(func() {
		self.Scheduler.StatusUpdate(drv, ts)
	})))
}

func (self *SchedulerProcess) FrameworkMessage(drv bindings.SchedulerDriver, eid *mesos.ExecutorID, sid *mesos.SlaveID, m string) {
	self.logError(self.Master().Do(proc.Action(func() {
		self.Scheduler.FrameworkMessage(drv, eid, sid, m)
	})))
}

func (self *SchedulerProcess) SlaveLost(drv bindings.SchedulerDriver, sid *mesos.SlaveID) {
	self.logError(self.Master().Do(proc.Action(func() {
		self.Scheduler.SlaveLost(drv, sid)
	})))
}

func (self *SchedulerProcess) ExecutorLost(drv bindings.SchedulerDriver, eid *mesos.ExecutorID, sid *mesos.SlaveID, x int) {
	self.logError(self.Master().Do(proc.Action(func() {
		self.Scheduler.ExecutorLost(drv, eid, sid, x)
	})))
}

func (self *SchedulerProcess) Error(drv bindings.SchedulerDriver, msg string) {
	self.logError(self.Master().Do(proc.Action(func() {
		self.Scheduler.Error(drv, msg)
	})))
}
