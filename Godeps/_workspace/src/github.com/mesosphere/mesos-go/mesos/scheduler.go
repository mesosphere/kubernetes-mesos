package mesos

/*
#cgo LDFLAGS: -L. -L/usr/local/lib -lmesos
#cgo linux LDFLAGS: -lstdc++
#cgo darwin CXXFLAGS: -stdlib=libc++
#cgo CXXFLAGS: -std=c++11
#cgo CFLAGS:-I. -I/usr/local/include -I/usr/local/include/mesos

#include <string.h>
#include <c-api.hpp>

extern void registeredCB(void*, ProtobufObj*, ProtobufObj*);
extern void reregisteredCB(void*, ProtobufObj*);
extern void disconnectedCB(void*);
extern void resourceOffersCB(void*, ProtobufObj*, size_t);
extern void offerRescindedCB(void*, ProtobufObj*);
extern void statusUpdateCB(void*, ProtobufObj*);
extern void frameworkMessageCB(
    void*,
    ProtobufObj*,
    ProtobufObj*,
    ProtobufObj*);
extern void slaveLostCB(void*, ProtobufObj*);
extern void executorLostCB(void*, ProtobufObj*, ProtobufObj*, int);
extern void errorCB(void*, ProtobufObj*);

static SchedulerCallbacks getSchedulerCallbacks() {
  SchedulerCallbacks callbacks;
  callbacks.registeredCallBack = registeredCB;
  callbacks.reregisteredCallBack = reregisteredCB;
  callbacks.disconnectedCallBack = disconnectedCB;
  callbacks.resourceOffersCallBack = resourceOffersCB;
  callbacks.offerRescindedCallBack = offerRescindedCB;
  callbacks.statusUpdateCallBack = statusUpdateCB;
  callbacks.frameworkMessageCallBack = frameworkMessageCB;
  callbacks.slaveLostCallBack = slaveLostCB;
  callbacks.executorLostCallBack = executorLostCB;
  callbacks.errorCallBack = errorCB;
  return callbacks;
}

static size_t sizeOfProtobufMessage() {
  return sizeof(ProtobufObj);
}
*/
import "C"

import (
	"encoding/binary"
	"errors"
	"log"
	"reflect"
	"runtime"
	"unsafe"

	"code.google.com/p/goprotobuf/proto"
)

func ScalarResource(name string, value float64) *Resource {
	return &Resource{
		Name:   proto.String(name),
		Type:   Value_SCALAR.Enum(),
		Scalar: Scalar(value),
	}
}

func Scalar(val float64) *Value_Scalar {
	return &Value_Scalar{Value: &val}
}

type SchedulerRegisteredFunc func(*SchedulerDriver, FrameworkID, MasterInfo)
type SchedulerReregisteredFunc func(*SchedulerDriver, MasterInfo)
type SchedulerDisconnectedFunc func(*SchedulerDriver)
type SchedulerResourceOffersFunc func(*SchedulerDriver, []Offer)
type SchedulerOfferRescindedFunc func(*SchedulerDriver, OfferID)
type SchedulerStatusUpdateFunc func(*SchedulerDriver, TaskStatus)
type SchedulerFrameworkMessageFunc func(*SchedulerDriver, ExecutorID, SlaveID, string)
type SchedulerSlaveLostFunc func(*SchedulerDriver, SlaveID)
type SchedulerExecutorLostFunc func(*SchedulerDriver, ExecutorID, SlaveID, int)
type SchedulerErrorFunc func(*SchedulerDriver, string)

type Scheduler struct {
	Registered       SchedulerRegisteredFunc
	Reregistered     SchedulerReregisteredFunc
	Disconnected     SchedulerDisconnectedFunc
	ResourceOffers   SchedulerResourceOffersFunc
	OfferRescinded   SchedulerOfferRescindedFunc
	StatusUpdate     SchedulerStatusUpdateFunc
	FrameworkMessage SchedulerFrameworkMessageFunc
	SlaveLost        SchedulerSlaveLostFunc
	ExecutorLost     SchedulerExecutorLostFunc
	Error            SchedulerErrorFunc
}

type SchedulerDriver struct {
	Master    string
	Framework FrameworkInfo
	Scheduler *Scheduler
	callbacks C.SchedulerCallbacks
	driver    unsafe.Pointer
	scheduler unsafe.Pointer
}

func serialize(pb proto.Message) (C.ProtobufObj, error) {
	var dataObj C.ProtobufObj
	data, err := proto.Marshal(pb)
	if err != nil {
		return dataObj, errors.New("Could not serialize message")
	}

	dataObj.data = unsafe.Pointer(&data[0])
	dataObj.size = C.size_t(len(data))

	return dataObj, nil
}

func serializeItem(pb proto.Message) ([]byte, error) {
	var ret []byte

	data, err := proto.Marshal(pb)
	if err != nil {
		return ret, errors.New("Could not serialize request")
	}

	length := uint64(len(data))
	lengthSlice := make([]byte, 8)
	binary.LittleEndian.PutUint64(lengthSlice, length)
	ret = append(ret, lengthSlice...)
	ret = append(ret, data...)

	return ret, nil
}

func (sdriver *SchedulerDriver) Init() error {
	var cmsg *C.char = C.CString(sdriver.Master)

	dataObj, err := serialize(&sdriver.Framework)
	if err != nil {
		return err
	}

	sdriver.callbacks = C.getSchedulerCallbacks()

	pair := C.scheduler_init(
		&sdriver.callbacks,
		unsafe.Pointer(sdriver),
		&dataObj,
		cmsg)

	sdriver.driver = pair.driver
	sdriver.scheduler = pair.scheduler

	return nil
}

func (sdriver *SchedulerDriver) Start() error {
	if sdriver.driver != nil {
		C.scheduler_start(C.SchedulerDriverPtr(sdriver.driver))
	} else {
		return errors.New("Start() failed: scheduler driver not initialized")
	}
	return nil
}

func (sdriver *SchedulerDriver) Stop(failover bool) error {
	if sdriver.driver != nil {
		var failoverInt C.int = 0
		if failover {
			failoverInt = 1
		}

		C.scheduler_stop(C.SchedulerDriverPtr(sdriver.driver), failoverInt)
	} else {
		return errors.New("Stop() failed: scheduler driver not initialized")
	}
	return nil
}

func (sdriver *SchedulerDriver) Abort() error {
	if sdriver.driver != nil {
		C.scheduler_abort(C.SchedulerDriverPtr(sdriver.driver))
	} else {
		return errors.New("Abort() failed: scheduler driver not initialized")
	}
	return nil
}

func (sdriver *SchedulerDriver) Join() error {
	if sdriver.driver != nil {
		C.scheduler_join(C.SchedulerDriverPtr(sdriver.driver))
	} else {
		return errors.New("Join() failed: scheduler driver not initialized")
	}
	return nil
}

func (sdriver *SchedulerDriver) Run() error {
	if sdriver.driver != nil {
		C.scheduler_run(C.SchedulerDriverPtr(sdriver.driver))
	} else {
		return errors.New("Run() failed: scheduler driver not initialized")
	}
	return nil
}

func (sdriver *SchedulerDriver) RequestResources(requests []Request) error {
	if sdriver.driver != nil {
		var requestsData []byte
		for _, request := range requests {
			requestItemData, err := serializeItem(&request)
			if err != nil {
				return err
			}

			requestsData = append(requestsData, requestItemData...)
		}

		requestsObj := C.ProtobufObj{
			data: unsafe.Pointer(&requestsData[0]),
			size: C.size_t(len(requestsData)),
		}

		C.scheduler_requestResources(
			C.SchedulerDriverPtr(sdriver.driver),
			&requestsObj)
	} else {
		return errors.New(
			"RequestResources() failed: scheduler driver not initialized")
	}

	return nil
}

func (sdriver *SchedulerDriver) LaunchTasks(
	offerId *OfferID,
	tasks []TaskInfo,
	filters ...Filters) error {

	if sdriver.driver != nil {
		offerObj, err := serialize(offerId)
		if err != nil {
			return err
		}

		var tasksData []byte
		for _, task := range tasks {
			taskItemData, err := serializeItem(&task)
			if err != nil {
				return err
			}
			tasksData = append(tasksData, taskItemData...)
		}

		tasksObj := C.ProtobufObj{
			data: unsafe.Pointer(&tasksData[0]),
			size: C.size_t(len(tasksData)),
		}

		var filters_ *C.ProtobufObj = nil
		if len(filters) > 1 {
			filtersObj, err := serialize(&filters[0])
			if err != nil {
				return err
			}

			filters_ = &filtersObj
		}

		C.scheduler_launchTasks(
			C.SchedulerDriverPtr(sdriver.driver),
			&offerObj,
			&tasksObj,
			filters_)
	} else {
		return errors.New("LaunchTasks() failed: scheduler driver not initialized")
	}

	return nil
}

func (sdriver *SchedulerDriver) KillTask(taskId *TaskID) error {
	if sdriver.driver != nil {
		message, err := serialize(taskId)
		if err != nil {
			return err
		}

		C.scheduler_killTask(C.SchedulerDriverPtr(sdriver.driver), &message)
	} else {
		return errors.New("KillTask() failed: scheduler driver not initialized")
	}

	return nil
}

func (sdriver *SchedulerDriver) DeclineOffer(
	offerId *OfferID,
	filters ...Filters) error {
	if sdriver.driver != nil {
		message, err := serialize(offerId)
		if err != nil {
			return err
		}

		var filters_ *C.ProtobufObj = nil
		if len(filters) > 1 {
			filtersObj, err := serialize(&filters[0])
			if err != nil {
				return err
			}

			filters_ = &filtersObj
		}

		C.scheduler_declineOffer(
			C.SchedulerDriverPtr(sdriver.driver),
			&message,
			filters_)
	} else {
		return errors.New("Start() failed: scheduler driver not initialized")
	}
	return nil
}

func (sdriver *SchedulerDriver) ReviveOffers() error {
	if sdriver.driver != nil {
		C.scheduler_reviveOffers(C.SchedulerDriverPtr(sdriver.driver))
	} else {
		return errors.New("ReviveOffers() failed: scheduler driver not initialized")
	}
	return nil
}

func (sdriver *SchedulerDriver) SendFrameworkMessage(
	executorId *ExecutorID,
	slaveId *SlaveID,
	data string) error {
	if sdriver.driver != nil {
		executorMessage, executorErr := serialize(executorId)
		if executorErr != nil {
			return executorErr
		}

		slaveMessage, slaveErr := serialize(slaveId)
		if slaveErr != nil {
			return slaveErr
		}

		var cdata *C.char = C.CString(data)

		C.scheduler_sendFrameworkMessage(
			C.SchedulerDriverPtr(sdriver.driver),
			&executorMessage,
			&slaveMessage,
			cdata)
	} else {
		return errors.New(
			"SendFrameworkMessage() failed: scheduler driver not initialized")
	}

	return nil
}

func (sdriver *SchedulerDriver) Destroy() {
	C.scheduler_destroy(sdriver.driver, sdriver.scheduler)
}

func (sdriver *SchedulerDriver) Wait() {
	for {
		// For now, wait for juicy details.
		runtime.Gosched()
	}
}

///////////////
// Callbacks //
///////////////

//export registeredCB
func registeredCB(
	ptr unsafe.Pointer,
	frameworkMessage *C.ProtobufObj,
	masterMessage *C.ProtobufObj) {
	if ptr != nil {
		var driver *SchedulerDriver = (*SchedulerDriver)(ptr)

		if driver.Scheduler.Registered == nil {
			return
		}

		frameworkData := C.GoBytes(
			frameworkMessage.data,
			C.int(frameworkMessage.size))

		var frameworkId FrameworkID
		err := proto.Unmarshal(frameworkData, &frameworkId)
		if err != nil {
			return
		}

		masterData := C.GoBytes(masterMessage.data, C.int(masterMessage.size))
		var masterInfo MasterInfo
		err = proto.Unmarshal(masterData, &masterInfo)
		if err != nil {
			return
		}

		driver.Scheduler.Registered(driver, frameworkId, masterInfo)
	}
}

//export reregisteredCB
func reregisteredCB(ptr unsafe.Pointer, masterMessage *C.ProtobufObj) {
	if ptr != nil {
		var driver *SchedulerDriver = (*SchedulerDriver)(ptr)
		if driver.Scheduler.Reregistered == nil {
			return
		}

		masterData := C.GoBytes(masterMessage.data, C.int(masterMessage.size))
		var masterInfo MasterInfo
		err := proto.Unmarshal(masterData, &masterInfo)
		if err != nil {
			return
		}

		driver.Scheduler.Reregistered(driver, masterInfo)
	}
}

//export disconnectedCB
func disconnectedCB(ptr unsafe.Pointer) {
	if ptr != nil {
		var driver *SchedulerDriver = (*SchedulerDriver)(ptr)
		if driver.Scheduler.Disconnected == nil {
			return
		}
		driver.Scheduler.Disconnected(driver)
	}
}

//export resourceOffersCB
func resourceOffersCB(
	ptr unsafe.Pointer,
	offerMessages *C.ProtobufObj,
	count C.size_t) {

	if ptr != nil {
		var driver *SchedulerDriver = (*SchedulerDriver)(ptr)

		// XXX(nnielsen): Verify memory assumptions.
		var messageSlice []C.ProtobufObj
		sliceHeader := (*reflect.SliceHeader)((unsafe.Pointer(&messageSlice)))
		sliceHeader.Cap = int(C.sizeOfProtobufMessage() * count)
		sliceHeader.Len = int(C.sizeOfProtobufMessage() * count)
		sliceHeader.Data = uintptr(unsafe.Pointer(offerMessages))

		var offers []Offer

		for i := 0; i < int(count); i++ {
			data := C.GoBytes((messageSlice[i]).data, C.int((messageSlice[i]).size))

			var offer Offer
			err := proto.Unmarshal(data, &offer)
			if err == nil {
				offers = append(offers, offer)
			}
		}
		driver.Scheduler.ResourceOffers(driver, offers)
	}
}

//export offerRescindedCB
func offerRescindedCB(ptr unsafe.Pointer, offerIdMessage *C.ProtobufObj) {
	if ptr != nil {
		var driver *SchedulerDriver = (*SchedulerDriver)(ptr)
		if driver.Scheduler.OfferRescinded == nil {
			return
		}

		data := C.GoBytes(offerIdMessage.data, C.int(offerIdMessage.size))
		var offerId OfferID
		err := proto.Unmarshal(data, &offerId)
		if err != nil {
			return
		}

		driver.Scheduler.OfferRescinded(driver, offerId)
	}
}

//export statusUpdateCB
func statusUpdateCB(
	ptr unsafe.Pointer,
	statusMessage *C.ProtobufObj) {
	if ptr != nil {
		var driver *SchedulerDriver = (*SchedulerDriver)(ptr)

		if driver.Scheduler.StatusUpdate == nil {
			return
		}

		data := C.GoBytes(statusMessage.data, C.int(statusMessage.size))

		var status TaskStatus
		err := proto.Unmarshal(data, &status)
		if err != nil {
			// XXX(nnielsen): report error.
			return
		}
		driver.Scheduler.StatusUpdate(driver, status)
	}
}

//export frameworkMessageCB
func frameworkMessageCB(
	ptr unsafe.Pointer,
	executorIdMessage *C.ProtobufObj,
	slaveIdMessage *C.ProtobufObj,
	dataMessage *C.ProtobufObj) {
	if ptr != nil {
		var driver *SchedulerDriver = (*SchedulerDriver)(ptr)
		if driver.Scheduler.FrameworkMessage == nil {
			return
		}
		executorData := C.GoBytes(executorIdMessage.data, C.int(executorIdMessage.size))
		var executorId ExecutorID
		err := proto.Unmarshal(executorData, &executorId)
		if err != nil {
			return
		}

		slaveData := C.GoBytes(slaveIdMessage.data, C.int(slaveIdMessage.size))
		var slaveId SlaveID
		err = proto.Unmarshal(slaveData, &slaveId)
		if err != nil {
			return
		}

		message := C.GoBytes(dataMessage.data, C.int(dataMessage.size))
		var messageString string = string(message)

		driver.Scheduler.FrameworkMessage(driver, executorId, slaveId, messageString)
	}
}

//export slaveLostCB
func slaveLostCB(ptr unsafe.Pointer, slaveIdMessage *C.ProtobufObj) {
	if ptr != nil {
		var driver *SchedulerDriver = (*SchedulerDriver)(ptr)
		if driver.Scheduler.SlaveLost == nil {
			return
		}

		data := C.GoBytes(slaveIdMessage.data, C.int(slaveIdMessage.size))
		var slaveId SlaveID
		err := proto.Unmarshal(data, &slaveId)
		if err != nil {
			return
		}

		driver.Scheduler.SlaveLost(driver, slaveId)
	}
}

//export executorLostCB
func executorLostCB(
	ptr unsafe.Pointer,
	executorIdMessage *C.ProtobufObj,
	slaveIdMessage *C.ProtobufObj,
	status C.int) {
	if ptr != nil {
		var driver *SchedulerDriver = (*SchedulerDriver)(ptr)
		if driver.Scheduler.ExecutorLost == nil {
			return
		}

		executorData := C.GoBytes(executorIdMessage.data, C.int(executorIdMessage.size))
		var executorId ExecutorID
		err := proto.Unmarshal(executorData, &executorId)
		if err != nil {
			return
		}

		slaveData := C.GoBytes(slaveIdMessage.data, C.int(slaveIdMessage.size))
		var slaveId SlaveID
		err = proto.Unmarshal(slaveData, &slaveId)
		if err != nil {
			return
		}

		driver.Scheduler.ExecutorLost(driver, executorId, slaveId, int(status))
	}
}

//export errorCB
func errorCB(ptr unsafe.Pointer, message *C.ProtobufObj) {
	if ptr != nil {
		data := C.GoBytes(message.data, C.int(message.size))
		var errorString string = string(data)

		// Special case: If error reporting isn't provided by the user,
		// write to log instead of dropping message.
		var driver *SchedulerDriver = (*SchedulerDriver)(ptr)
		if driver.Scheduler.Error == nil {
			log.Print("Mesos error: " + errorString)
			return
		}
		driver.Scheduler.Error(driver, errorString)
	}
}
