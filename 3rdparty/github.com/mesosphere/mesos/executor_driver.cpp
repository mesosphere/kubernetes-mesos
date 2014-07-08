#include <stdio.h>
#include <string>
#include <assert.h>

#include <mesos/executor.hpp>

#include "c-api.hpp"
#include "utils.hpp"

using namespace std;
using namespace mesos;


class CExecutor : public Executor {
public:
  CExecutor() {}

  virtual ~CExecutor() {}

  virtual void registered(
      ExecutorDriver* driver,
      const ExecutorInfo& executorInfo,
      const FrameworkInfo& frameworkInfo,
      const SlaveInfo& slaveInfo);

  virtual void reregistered(
      ExecutorDriver* driver,
      const SlaveInfo& slaveInfo);

  virtual void disconnected(ExecutorDriver* driver);

  virtual void launchTask(ExecutorDriver* driver, const TaskInfo& task);

  virtual void killTask(ExecutorDriver* driver, const TaskID& taskId);

  virtual void frameworkMessage(ExecutorDriver* driver, const string& data);

  virtual void shutdown(ExecutorDriver* driver);

  virtual void error(ExecutorDriver* driver, const string& message);

  ExecutorCallbacks callbacks;
  void* payload;
};


ExecutorPtrPair executor_init(
    ExecutorCallbacks* callbacks,
    void* payload)
{
  TRACE("executor_init()\n");
  CExecutor* executor = new CExecutor();

  MesosExecutorDriver* driver = new MesosExecutorDriver(executor);
  if (callbacks != NULL) {
    executor->callbacks = *callbacks;
  }

  executor->payload = payload;

  ExecutorPtrPair pair;
  pair.executor = executor;
  pair.driver = driver;

  return pair;
}


void executor_destroy(void* driver, void* executor)
{
  TRACE("executor_destroy()\n");
  assert(driver != NULL);
  assert(executor != NULL);

  MesosExecutorDriver* mdriver =
      reinterpret_cast<MesosExecutorDriver*>(driver);
  CExecutor* cexecutor =
      reinterpret_cast<CExecutor*>(executor);

  delete mdriver;
  delete cexecutor;
}


ExecutorDriverStatus executor_start(ExecutorDriverPtr driver) {
  TRACE("executor_start()\n");
  assert(driver != NULL);

  MesosExecutorDriver* mdriver =
      reinterpret_cast<MesosExecutorDriver*>(driver);

  return mdriver->start();
}


ExecutorDriverStatus executor_stop(ExecutorDriverPtr driver) {
  TRACE("executor_stop()\n");
  assert(driver != NULL);

  MesosExecutorDriver* mdriver =
      reinterpret_cast<MesosExecutorDriver*>(driver);

  return mdriver->stop();
}


ExecutorDriverStatus executor_abort(ExecutorDriverPtr driver) {
  TRACE("executor_abort()\n");
  assert(driver != NULL);

  MesosExecutorDriver* mdriver =
      reinterpret_cast<MesosExecutorDriver*>(driver);

  return mdriver->abort();
}


ExecutorDriverStatus executor_join(ExecutorDriverPtr driver) {
  TRACE("executor_join()\n");
  assert(driver != NULL);

  MesosExecutorDriver* mdriver =
      reinterpret_cast<MesosExecutorDriver*>(driver);

  return mdriver->join();
}


ExecutorDriverStatus executor_run(ExecutorDriverPtr driver) {
  TRACE("executor_run()\n");
  assert(driver != NULL);

  MesosExecutorDriver* mdriver =
      reinterpret_cast<MesosExecutorDriver*>(driver);

  return mdriver->run();
}


ExecutorDriverStatus executor_sendStatusUpdate(
    ExecutorDriverPtr driver,
    ProtobufObj* status) {
  TRACE("executor_sendStatusUpdate()\n");
  assert(driver != NULL);
  assert(status != NULL);

  MesosExecutorDriver* mdriver =
      reinterpret_cast<MesosExecutorDriver*>(driver);

  TaskStatus taskStatus;
  if (!utils::deserialize<TaskStatus>(taskStatus, status)) {
    return DRIVER_ABORTED;
  }

  return mdriver->sendStatusUpdate(taskStatus);
}


ExecutorDriverStatus executor_sendFrameworkMessage(
    ExecutorDriverPtr driver,
    const char* data) {
  TRACE("executor_sendFrameworkMessage()\n");
  assert(driver != NULL);
  assert(data != NULL);

  MesosExecutorDriver* mdriver =
      reinterpret_cast<MesosExecutorDriver*>(driver);

  return mdriver->sendFrameworkMessage(data);
}


void CExecutor::registered(
      ExecutorDriver* driver,
      const ExecutorInfo& executorInfo,
      const FrameworkInfo& frameworkInfo,
      const SlaveInfo& slaveInfo)
{
  TRACE("Callback: registered()\n");
  if (callbacks.registeredCallBack == NULL) {
    return;
  }

  std::string encodedExecutor;
  ProtobufObj executorObj =
      utils::serialize<const ExecutorInfo>(executorInfo, encodedExecutor);

  std::string encodedFramework;
  ProtobufObj frameworkObj =
      utils::serialize<const FrameworkInfo>(frameworkInfo, encodedFramework);

  std::string encodedSlave;
  ProtobufObj slaveObj =
      utils::serialize<const SlaveInfo>(slaveInfo, encodedSlave);

  callbacks.registeredCallBack(
      payload,
      &executorObj,
      &frameworkObj,
      &slaveObj);
}


void CExecutor::reregistered(
    ExecutorDriver* driver,
    const SlaveInfo& slaveInfo)
{
  TRACE("Callback: reregistered()\n");
  if (callbacks.reregisteredCallBack == NULL) {
    return;
  }

  std::string encodedSlave;
  ProtobufObj slaveObj =
      utils::serialize<const SlaveInfo>(slaveInfo, encodedSlave);

  callbacks.reregisteredCallBack(payload, &slaveObj);
}


void CExecutor::disconnected(ExecutorDriver* driver)
{
  TRACE("Callback: disconnected()\n");
  if (callbacks.disconnectedCallBack == NULL) {
    return;
  }

  callbacks.disconnectedCallBack(payload);
}


void CExecutor::launchTask(ExecutorDriver* driver, const TaskInfo& taskInfo)
{
  TRACE("Callback: launchTask()\n");
  if (callbacks.launchTaskCallBack == NULL) {
    return;
  }

  std::string encodedTask;
  ProtobufObj taskObj =
      utils::serialize<const TaskInfo>(taskInfo, encodedTask);

  callbacks.launchTaskCallBack(payload, &taskObj);
}


void CExecutor::killTask(ExecutorDriver* driver, const TaskID& taskId)
{
  TRACE("Callback: killTask()\n");
  if (callbacks.killTaskCallBack == NULL) {
    return;
  }

  std::string encodedTask;
  ProtobufObj taskObj =
      utils::serialize<const TaskID>(taskId, encodedTask);

  callbacks.killTaskCallBack(payload, &taskObj);
}


void CExecutor::frameworkMessage(ExecutorDriver* driver, const string& data)
{
  TRACE("Callback: frameworkMessage()\n");
  if (callbacks.frameworkMessageCallBack == NULL) {
    return;
  }

  ProtobufObj dataObj = utils::serialize(data);
  callbacks.frameworkMessageCallBack(payload, &dataObj);
}


void CExecutor::shutdown(ExecutorDriver* driver) {
  TRACE("Callback: shutdown()\n");
  if (callbacks.shutdownCallBack == NULL) {
    return;
  }

  callbacks.shutdownCallBack(payload);
}


void CExecutor::error(ExecutorDriver* driver, const string& message)
{
  TRACE("Callback: error()\n");
  if (callbacks.errorCallBack == NULL) {
    return;
  }

  ProtobufObj errorObj = utils::serialize(message);
  callbacks.errorCallBack(payload, &errorObj);
}
