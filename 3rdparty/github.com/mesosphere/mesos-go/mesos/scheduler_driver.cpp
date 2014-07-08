#include <stdio.h>
#include <string>
#include <map>
#include <vector>

#include <assert.h>

#include <mesos/scheduler.hpp>

#include "c-api.hpp"
#include "utils.hpp"

using namespace std;
using namespace mesos;


class CScheduler : public Scheduler
{
public:
  CScheduler() {}

  virtual ~CScheduler() {}

  virtual void registered(
      SchedulerDriver* driver,
      const FrameworkID& frameworkId,
      const MasterInfo& masterInfo);

  virtual void reregistered(SchedulerDriver*, const MasterInfo& masterInfo);

  virtual void disconnected(SchedulerDriver* driver);

  virtual void resourceOffers(
      SchedulerDriver* driver,
      const vector<Offer>& offers);

  virtual void offerRescinded(SchedulerDriver* driver, const OfferID& offerId);

  virtual void statusUpdate(SchedulerDriver* driver, const TaskStatus& status);

  virtual void frameworkMessage(
      SchedulerDriver* driver,
      const ExecutorID& executorId,
      const SlaveID& slaveId,
      const string& data);

  virtual void slaveLost(SchedulerDriver* driver, const SlaveID& slaveId);

  virtual void executorLost(
      SchedulerDriver* driver,
      const ExecutorID& executorId,
      const SlaveID& slaveId,
      int status);

  virtual void error(SchedulerDriver* driver, const string& message);

  SchedulerCallbacks callbacks;
  void* payload;
  FrameworkInfo info;
};


SchedulerPtrPair scheduler_init(
    SchedulerCallbacks* callbacks,
    void* payload,
    ProtobufObj* framework,
    const char* master)
{
  TRACE("scheduler_init()\n");
  assert(master != NULL);

  SchedulerPtrPair pair;
  pair.driver = NULL;
  pair.scheduler = NULL;

  CScheduler* scheduler = new CScheduler();

  if (!utils::deserialize<FrameworkInfo>(
      scheduler->info,
      framework)) {
    return pair;
  }

  MesosSchedulerDriver* driver = new MesosSchedulerDriver(
     scheduler,
     scheduler->info,
     std::string(master));

  if (callbacks != NULL) {
    scheduler->callbacks = *callbacks;
  }

  scheduler->payload = payload;
  pair.driver = driver;
  pair.scheduler = scheduler;

  return pair;
}


void scheduler_destroy(void* driver, void* scheduler)
{
  TRACE("scheduler_destroy()\n");
  assert(driver != NULL);
  assert(scheduler != NULL);

  MesosSchedulerDriver* mdriver =
      reinterpret_cast<MesosSchedulerDriver*>(driver);
  CScheduler* cscheduler =
      reinterpret_cast<CScheduler*>(scheduler);

  delete mdriver;
  delete cscheduler;
}


SchedulerDriverStatus scheduler_start(SchedulerDriverPtr driver)
{
  TRACE("scheduler_start()\n");
  assert(driver != NULL);

  MesosSchedulerDriver* mdriver =
      reinterpret_cast<MesosSchedulerDriver*>(driver);

  return mdriver->start();
}


SchedulerDriverStatus scheduler_stop(SchedulerDriverPtr driver, int failover)
{
  TRACE("scheduler_stop()\n");
  assert(driver != NULL);

  MesosSchedulerDriver* mdriver =
      reinterpret_cast<MesosSchedulerDriver*>(driver);

  return mdriver->stop(failover);
}


SchedulerDriverStatus scheduler_abort(SchedulerDriverPtr driver)
{
  TRACE("scheduler_abort()\n");
  assert(driver != NULL);

  MesosSchedulerDriver* mdriver =
      reinterpret_cast<MesosSchedulerDriver*>(driver);

  return mdriver->abort();
}


SchedulerDriverStatus scheduler_join(SchedulerDriverPtr driver)
{
  TRACE("scheduler_join()\n");
  assert(driver != NULL);

  MesosSchedulerDriver* mdriver =
      reinterpret_cast<MesosSchedulerDriver*>(driver);

  return mdriver->join();
}


SchedulerDriverStatus scheduler_run(SchedulerDriverPtr driver)
{
  TRACE("scheduler_run()\n");
  assert(driver != NULL);

  MesosSchedulerDriver* mdriver =
      reinterpret_cast<MesosSchedulerDriver*>(driver);

  return mdriver->join();
}


SchedulerDriverStatus scheduler_requestResources(
    SchedulerDriverPtr driver,
    ProtobufObj* requests)
{
  TRACE("scheduler_requestResources()\n");
  assert(driver != NULL);
  assert(requests != NULL);

  MesosSchedulerDriver* mdriver =
      reinterpret_cast<MesosSchedulerDriver*>(driver);

  vector<Request> requests_;
  if (!utils::deserialize<Request>(requests_, requests)) {
    return DRIVER_ABORTED;
  }

  return mdriver->requestResources(requests_);
}


SchedulerDriverStatus scheduler_launchTasks(
    SchedulerDriverPtr driver,
    ProtobufObj* offerId,
    ProtobufObj* tasks,
    ProtobufObj* filters)
{
  TRACE("scheduler_launchTasks()\n");
  assert(driver != NULL);
  assert(offerId != NULL);
  assert(tasks != NULL);

  MesosSchedulerDriver* mdriver =
      reinterpret_cast<MesosSchedulerDriver*>(driver);

  OfferID offer;
  if (!utils::deserialize<OfferID>(offer, offerId)) {
    return DRIVER_ABORTED;
  }

  vector<TaskInfo> taskInfos;
  if (!utils::deserialize<TaskInfo>(taskInfos, tasks)) {
    return DRIVER_ABORTED;
  }

  Filters filters_;
  if (filters != NULL && filters->data != NULL) {
    if (!utils::deserialize<Filters>(filters_, filters)) {
      return DRIVER_ABORTED;
    }
  }

  return mdriver->launchTasks(offer, taskInfos, filters_);
}


SchedulerDriverStatus scheduler_killTask(
    SchedulerDriverPtr driver,
    ProtobufObj* taskIdMessage)
{
  TRACE("scheduler_killTask()\n");

  MesosSchedulerDriver* mdriver =
      reinterpret_cast<MesosSchedulerDriver*>(driver);
  assert(driver != NULL);
  assert(taskIdMessage != NULL);

  TaskID taskId;
  if (!utils::deserialize<TaskID>(taskId, taskIdMessage)) {
    return DRIVER_ABORTED;
  }

  return mdriver->killTask(taskId);
}


SchedulerDriverStatus scheduler_declineOffer(
    SchedulerDriverPtr driver,
    ProtobufObj* offerId,
    ProtobufObj* filters)
{
  TRACE("scheduler_declineOffer()\n");
  assert(driver != NULL);
  assert(offerId != NULL);

  MesosSchedulerDriver* mdriver =
      reinterpret_cast<MesosSchedulerDriver*>(driver);

  Filters filters_;
  if (filters != NULL && filters->data != NULL) {
    if (!utils::deserialize<Filters>(filters_, filters)) {
      return DRIVER_ABORTED;
    }
  }

  OfferID offer;
  if (!utils::deserialize<OfferID>(offer, offerId)) {
    return DRIVER_ABORTED;
  }

  return mdriver->declineOffer(offer, filters_);
}


SchedulerDriverStatus scheduler_reviveOffers(SchedulerDriverPtr driver)
{
  TRACE("scheduler_reviveOffers()\n");
  assert(driver != NULL);

  MesosSchedulerDriver* mdriver =
      reinterpret_cast<MesosSchedulerDriver*>(driver);

  return mdriver->reviveOffers();
}


SchedulerDriverStatus scheduler_sendFrameworkMessage(
    SchedulerDriverPtr driver,
    ProtobufObj* executorId,
    ProtobufObj* slaveId,
    const char* data)
{
  TRACE("scheduler_sendFrameworkMessage()\n");
  assert(driver != NULL);
  MesosSchedulerDriver* mdriver =
      reinterpret_cast<MesosSchedulerDriver*>(driver);

  ExecutorID executor;
  if (!utils::deserialize<ExecutorID>(executor, executorId)) {
    return DRIVER_ABORTED;
  }

  SlaveID slave;
  if (!utils::deserialize<SlaveID>(slave, slaveId)) {
    return DRIVER_ABORTED;
  }

  return mdriver->sendFrameworkMessage(executor, slave, data);
}


void CScheduler::registered(SchedulerDriver* driver,
    const FrameworkID& frameworkId,
    const MasterInfo& masterInfo)
{
  TRACE("Callback: registered()\n");

  if (callbacks.registeredCallBack == NULL) {
    return;
  }

  std::string encodedFramework;
  ProtobufObj frameworkObj =
      utils::serialize<const FrameworkID>(frameworkId, encodedFramework);

  std::string encodedMaster;
  ProtobufObj masterObj =
      utils::serialize<const MasterInfo>(masterInfo, encodedMaster);

  callbacks.registeredCallBack(payload, &frameworkObj, &masterObj);
}


void CScheduler::reregistered(SchedulerDriver*, const MasterInfo& masterInfo)
{
  TRACE("Callback: reregistered()\n");

  if (callbacks.reregisteredCallBack == NULL) {
    return;
  }

  std::string encodedMaster;
  ProtobufObj masterObj =
      utils::serialize<const MasterInfo>(masterInfo, encodedMaster);

  callbacks.reregisteredCallBack(payload, &masterObj);
}


void CScheduler::disconnected(SchedulerDriver* driver)
{
  TRACE("Callback: disconnected()\n");

  if (callbacks.disconnectedCallBack == NULL) {
    return;
  }

  callbacks.disconnectedCallBack(payload);
}


void CScheduler::resourceOffers(
    SchedulerDriver* driver,
    const vector<Offer>& offers)
{
  TRACE("Callback: resourceOffers()\n");

  if (callbacks.resourceOffersCallBack == NULL) {
    return;
  }

  vector<std::string> encodedOffers;
  vector<ProtobufObj> offersObj;
  for (size_t i = 0; i < offers.size(); i++) {
    encodedOffers.push_back("");
    std::string& encodedOffer = encodedOffers.back();
    ProtobufObj offerObj =
        utils::serialize<const Offer>(offers[i], encodedOffers.back());

    offersObj.push_back(offerObj);
  }

  callbacks.resourceOffersCallBack(payload, &offersObj[0], offersObj.size());
}


void CScheduler::offerRescinded(
    SchedulerDriver* driver,
    const OfferID& offerId)
{
  TRACE("Callback: offerRescinded()\n");

  if (callbacks.offerRescindedCallBack == NULL) {
    return;
  }

  std::string encodedOffer;
  ProtobufObj offerObj =
      utils::serialize<const OfferID>(offerId, encodedOffer);

  callbacks.offerRescindedCallBack(payload, &offerObj);
}


void CScheduler::statusUpdate(
    SchedulerDriver* driver,
    const TaskStatus& status)
{
  TRACE("Callback: statusUpdate()\n");

  if (callbacks.statusUpdateCallBack == NULL) {
    return;
  }

  std::string encodedStatus;
  ProtobufObj statusObj =
      utils::serialize<const TaskStatus>(status, encodedStatus);

  callbacks.statusUpdateCallBack(payload, &statusObj);
}


void CScheduler::frameworkMessage(
    SchedulerDriver* driver,
    const ExecutorID& executorId,
    const SlaveID& slaveId,
    const string& data)
{
  TRACE("Callback: frameworkMessage()\n");

  if (callbacks.frameworkMessageCallBack == NULL) {
    return;
  }

  std::string encodedExecutor;
  ProtobufObj executorObj =
      utils::serialize<const ExecutorID>(executorId, encodedExecutor);

  std::string encodedSlave;
  ProtobufObj slaveObj =
      utils::serialize<const SlaveID>(slaveId, encodedSlave);

  ProtobufObj dataObj = utils::serialize(data);

  callbacks.frameworkMessageCallBack(
      payload,
      &executorObj,
      &slaveObj,
      &dataObj);
}


void CScheduler::slaveLost(SchedulerDriver* driver, const SlaveID& slaveId)
{
  TRACE("Callback: slaveLost()\n");

  if (callbacks.slaveLostCallBack == NULL) {
    return;
  }

  std::string encodedSlave;
  ProtobufObj slaveObj =
      utils::serialize<const SlaveID>(slaveId, encodedSlave);

  callbacks.slaveLostCallBack(payload, &slaveObj);
}


void CScheduler::executorLost(
    SchedulerDriver* driver,
    const ExecutorID& executorId,
    const SlaveID& slaveId,
    int status)
{
  TRACE("Callback: executorLost()\n");

  if (callbacks.executorLostCallBack == NULL) {
    return;
  }

  std::string encodedExecutor;
  ProtobufObj executorObj =
      utils::serialize<const ExecutorID>(executorId, encodedExecutor);

  std::string encodedSlave;
  ProtobufObj slaveObj =
      utils::serialize<const SlaveID>(slaveId, encodedSlave);

  callbacks.executorLostCallBack(
      payload,
      &executorObj,
      &slaveObj,
      status);
}


void CScheduler::error(SchedulerDriver* driver, const string& message)
{
  TRACE("Callback: error()\n");

  if (callbacks.errorCallBack == NULL) {
    return;
  }

  ProtobufObj errorObj = utils::serialize(message);
  callbacks.errorCallBack(payload, &errorObj);
}
