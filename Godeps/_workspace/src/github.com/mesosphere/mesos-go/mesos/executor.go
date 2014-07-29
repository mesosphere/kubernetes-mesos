package mesos

/*
#cgo LDFLAGS: -L. -L/usr/local/lib -lmesos
#cgo linux LDFLAGS: -lstdc++
#cgo darwin CXXFLAGS: -stdlib=libc++
#cgo CXXFLAGS: -std=c++11
#cgo CFLAGS:-I. -I/usr/local/include -I/usr/local/include/mesos

#include <string.h>
#include <c-api.hpp>

extern void executor_registeredCB(void*, ProtobufObj*, ProtobufObj*, ProtobufObj*);
extern void executor_reregisteredCB(void*, ProtobufObj*);
extern void executor_disconnectedCB(void*);
extern void executor_launchTaskCB(void*, ProtobufObj*);
extern void executor_killTaskCB(void*, ProtobufObj*);
extern void executor_frameworkMessageCB(void*, ProtobufObj*);
extern void executor_shutdownCB(void*);
extern void executor_errorCB(void*, ProtobufObj*);

static ExecutorCallbacks getExecutorCallbacks() {
  ExecutorCallbacks callbacks;

  callbacks.registeredCallBack = executor_registeredCB;
  callbacks.reregisteredCallBack = executor_reregisteredCB;
  callbacks.disconnectedCallBack = executor_disconnectedCB;
  callbacks.launchTaskCallBack = executor_launchTaskCB;
  callbacks.killTaskCallBack = executor_killTaskCB;
  callbacks.frameworkMessageCallBack = executor_frameworkMessageCB;
  callbacks.shutdownCallBack = executor_shutdownCB;
  callbacks.errorCallBack = executor_errorCB;

  return callbacks;
}
*/
import "C"

import (
	"errors"
	"log"
	"unsafe"

	"code.google.com/p/goprotobuf/proto"
)

func NewTaskState(val TaskState) *TaskState {
	return &val
}

type ExecutorRegisteredFunc func(*ExecutorDriver, ExecutorInfo, FrameworkInfo, SlaveInfo)
type ExecutorReregisteredFunc func(*ExecutorDriver, SlaveInfo)
type ExecutorDisconnectedFunc func(*ExecutorDriver)
type ExecutorLaunchTaskFunc func(*ExecutorDriver, TaskInfo)
type ExecutorKillTaskFunc func(*ExecutorDriver, TaskID)
type ExecutorFrameworkMessageFunc func(*ExecutorDriver, string)
type ExecutorShutdownFunc func(*ExecutorDriver)
type ExecutorErrorFunc func(*ExecutorDriver, string)

type Executor struct {
	Registered       ExecutorRegisteredFunc
	Reregistered     ExecutorReregisteredFunc
	Disconnected     ExecutorDisconnectedFunc
	LaunchTask       ExecutorLaunchTaskFunc
	KillTask         ExecutorKillTaskFunc
	FrameworkMessage ExecutorFrameworkMessageFunc
	Shutdown         ExecutorShutdownFunc
	Error            ExecutorErrorFunc
}

type ExecutorDriver struct {
	Executor  *Executor
	callbacks C.ExecutorCallbacks
	driver    unsafe.Pointer
	executor  unsafe.Pointer
}

func (edriver *ExecutorDriver) Init() error {
	edriver.callbacks = C.getExecutorCallbacks()

	pair := C.executor_init(&edriver.callbacks, unsafe.Pointer(edriver))
	edriver.driver = pair.driver
	edriver.executor = pair.executor

	return nil
}

func (edriver *ExecutorDriver) Start() error {
	if edriver.driver != nil {
		C.executor_start(C.ExecutorDriverPtr(edriver.driver))
	} else {
		return errors.New("Start() failed: executor driver not initialized")
	}
	return nil
}

func (edriver *ExecutorDriver) Stop() error {
	if edriver.driver != nil {
		C.executor_stop(C.ExecutorDriverPtr(edriver.driver))
	} else {
		return errors.New("Stop() failed: executor driver not initialized")
	}
	return nil
}

func (edriver *ExecutorDriver) Abort() error {
	if edriver.driver != nil {
		C.executor_abort(C.ExecutorDriverPtr(edriver.driver))
	} else {
		return errors.New("Abort() failed: executor driver not initialized")
	}
	return nil
}

func (edriver *ExecutorDriver) Join() error {
	if edriver.driver != nil {
		C.executor_join(C.ExecutorDriverPtr(edriver.driver))
	} else {
		return errors.New("Join() failed: executor driver not initialized")
	}
	return nil
}

func (edriver *ExecutorDriver) Run() error {
	if edriver.driver != nil {
		C.executor_run(C.ExecutorDriverPtr(edriver.driver))
	} else {
		return errors.New("Run() failed: executor driver not initialized")
	}
	return nil
}

func (edriver *ExecutorDriver) SendStatusUpdate(status *TaskStatus) error {
	if edriver.driver != nil {
		statusObj, err := serialize(status)
		if err != nil {
			return err
		}

		C.executor_sendStatusUpdate(
			C.ExecutorDriverPtr(edriver.driver),
			&statusObj)
	} else {
		return errors.New(
			"sendStatusUpdate() failed: executor driver not initialized")
	}

	return nil
}

func (edriver *ExecutorDriver) SendFrameworkMessage(message string) error {
	if edriver.driver != nil {
		var cdata *C.char = C.CString(message)

		C.executor_sendFrameworkMessage(C.ExecutorDriverPtr(edriver.driver), cdata)
	} else {
		return errors.New(
			"SendFrameworkMessage() failed: executor driver not initialized")
	}

	return nil
}

func (edriver *ExecutorDriver) Destroy() {
	C.executor_destroy(edriver.driver, edriver.executor)
}

///////////////
// Callbacks //
///////////////

//export executor_registeredCB
func executor_registeredCB(
	ptr unsafe.Pointer,
	executorInfo *C.ProtobufObj,
	frameworkInfo *C.ProtobufObj,
	slaveInfo *C.ProtobufObj) {
	if ptr != nil {
		var driver *ExecutorDriver = (*ExecutorDriver)(ptr)

		if driver.Executor.Registered == nil {
			return
		}

		executorData := C.GoBytes(executorInfo.data, C.int(executorInfo.size))
		var executor ExecutorInfo
		err := proto.Unmarshal(executorData, &executor)
		if err != nil {
			return
		}

		frameworkData := C.GoBytes(frameworkInfo.data, C.int(frameworkInfo.size))
		var framework FrameworkInfo
		err = proto.Unmarshal(frameworkData, &framework)
		if err != nil {
			return
		}

		slaveData := C.GoBytes(slaveInfo.data, C.int(slaveInfo.size))
		var slave SlaveInfo
		err = proto.Unmarshal(slaveData, &slave)
		if err != nil {
			return
		}

		driver.Executor.Registered(driver, executor, framework, slave)
	}
}

//export executor_reregisteredCB
func executor_reregisteredCB(ptr unsafe.Pointer, slaveInfo *C.ProtobufObj) {
	if ptr != nil {
		var driver *ExecutorDriver = (*ExecutorDriver)(ptr)
		if driver.Executor.Reregistered == nil {
			return
		}

		slaveData := C.GoBytes(slaveInfo.data, C.int(slaveInfo.size))
		var slave SlaveInfo
		err := proto.Unmarshal(slaveData, &slave)
		if err != nil {
			return
		}

		driver.Executor.Reregistered(driver, slave)
	}
}

//export executor_disconnectedCB
func executor_disconnectedCB(ptr unsafe.Pointer) {
	if ptr != nil {
		var driver *ExecutorDriver = (*ExecutorDriver)(ptr)
		if driver.Executor.Disconnected == nil {
			return
		}
		driver.Executor.Disconnected(driver)
	}
}

//export executor_launchTaskCB
func executor_launchTaskCB(ptr unsafe.Pointer, taskInfo *C.ProtobufObj) {
	if ptr != nil {
		var driver *ExecutorDriver = (*ExecutorDriver)(ptr)
		if driver.Executor.LaunchTask == nil {
			return
		}

		taskData := C.GoBytes(taskInfo.data, C.int(taskInfo.size))
		var task TaskInfo
		err := proto.Unmarshal(taskData, &task)
		if err != nil {
			return
		}

		driver.Executor.LaunchTask(driver, task)
	}
}

//export executor_killTaskCB
func executor_killTaskCB(ptr unsafe.Pointer, taskId *C.ProtobufObj) {
	if ptr != nil {
		var driver *ExecutorDriver = (*ExecutorDriver)(ptr)
		if driver.Executor.LaunchTask == nil {
			return
		}

		taskData := C.GoBytes(taskId.data, C.int(taskId.size))
		var task TaskID
		err := proto.Unmarshal(taskData, &task)
		if err != nil {
			return
		}

		driver.Executor.KillTask(driver, task)
	}
}

//export executor_frameworkMessageCB
func executor_frameworkMessageCB(ptr unsafe.Pointer, message *C.ProtobufObj) {
	if ptr != nil {
		data := C.GoBytes(message.data, C.int(message.size))
		var messageString string = string(data)

		var driver *ExecutorDriver = (*ExecutorDriver)(ptr)
		if driver.Executor.Error == nil {
			return
		}

		driver.Executor.FrameworkMessage(driver, messageString)
	}
}

//export executor_shutdownCB
func executor_shutdownCB(ptr unsafe.Pointer) {
	if ptr != nil {
		var driver *ExecutorDriver = (*ExecutorDriver)(ptr)
		if driver.Executor.Error == nil {
			return
		}
		driver.Executor.Shutdown(driver)
	}
}

//export executor_errorCB
func executor_errorCB(ptr unsafe.Pointer, message *C.ProtobufObj) {
	if ptr != nil {
		data := C.GoBytes(message.data, C.int(message.size))
		var errorString string = string(data)

		var driver *ExecutorDriver = (*ExecutorDriver)(ptr)
		if driver.Executor.Error == nil {
			log.Print("Mesos error: " + errorString)
			return
		}
		driver.Executor.Error(driver, errorString)
	}
}
