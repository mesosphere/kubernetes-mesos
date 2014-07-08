#ifndef __MESOS_C_API_HPP__
#define __MESOS_C_API_HPP__

typedef struct {
  void* data;
  size_t size;
} ProtobufObj;

// Function pointer type definitions for scheduler callbacks
typedef void (*scheduler_registeredCallBack_t)(
    void*,
    ProtobufObj*,
    ProtobufObj*);
typedef void (*scheduler_reregisteredCallBack_t)(void*, ProtobufObj*);
typedef void (*scheduler_resourceOffersCallBack_t)(void*, ProtobufObj*, size_t);
typedef void (*scheduler_statusUpdateCallBack_t)(void*, ProtobufObj*);
typedef void (*scheduler_disconnectedCallBack_t)(void*);
typedef void (*scheduler_offerRescindedCallBack_t)(void*, ProtobufObj*);
typedef void (*scheduler_frameworkMessageCallBack_t)(
    void*,
    ProtobufObj*,
    ProtobufObj*,
    ProtobufObj*);
typedef void (*scheduler_slaveLostCallBack_t)(void*, ProtobufObj*);
typedef void (*scheduler_executorLostCallBack_t)(
    void*,
    ProtobufObj*,
    ProtobufObj*,
    int);
typedef void (*scheduler_errorCallBack_t)(void*, ProtobufObj*);

typedef struct {
  scheduler_registeredCallBack_t        registeredCallBack;
  scheduler_reregisteredCallBack_t      reregisteredCallBack;
  scheduler_resourceOffersCallBack_t    resourceOffersCallBack;
  scheduler_statusUpdateCallBack_t      statusUpdateCallBack;
  scheduler_disconnectedCallBack_t      disconnectedCallBack;
  scheduler_offerRescindedCallBack_t    offerRescindedCallBack;
  scheduler_frameworkMessageCallBack_t  frameworkMessageCallBack;
  scheduler_slaveLostCallBack_t         slaveLostCallBack;
  scheduler_executorLostCallBack_t      executorLostCallBack;
  scheduler_errorCallBack_t             errorCallBack;
} SchedulerCallbacks;

typedef void* SchedulerDriverPtr;
typedef int SchedulerDriverStatus;

typedef struct {
  void* scheduler;
  void* driver;
} SchedulerPtrPair;

// Function pointer type definitions for executor callbacks
typedef void (*executor_registeredCallBack_t)(
    void*,
    ProtobufObj*,
    ProtobufObj*,
    ProtobufObj*);
typedef void (*executor_reregisteredCallBack_t)(void*, ProtobufObj*);
typedef void (*executor_disconnectedCallBack_t)(void*);
typedef void (*executor_launchTaskCallBack_t)(void*, ProtobufObj*);
typedef void (*executor_killTaskCallBack_t)(void*, ProtobufObj*);
typedef void (*executor_frameworkMessageCallBack_t)(void*, ProtobufObj*);
typedef void (*executor_shutdownCallBack_t)(void*);
typedef void (*executor_errorCallBack_t)(void*, ProtobufObj*);

typedef struct {
  executor_registeredCallBack_t         registeredCallBack;
  executor_reregisteredCallBack_t       reregisteredCallBack;
  executor_disconnectedCallBack_t       disconnectedCallBack;
  executor_launchTaskCallBack_t         launchTaskCallBack;
  executor_killTaskCallBack_t           killTaskCallBack;
  executor_frameworkMessageCallBack_t   frameworkMessageCallBack;
  executor_shutdownCallBack_t           shutdownCallBack;
  executor_errorCallBack_t              errorCallBack;
} ExecutorCallbacks;

typedef void* ExecutorDriverPtr;
typedef int ExecutorDriverStatus;

typedef struct {
  void* executor;
  void* driver;
} ExecutorPtrPair;

#ifdef __cplusplus
extern "C" {
#endif

//
// Scheduler driver calls
//

SchedulerDriverStatus scheduler_launchTasks(
    SchedulerDriverPtr driver,
    ProtobufObj* offerId,
    ProtobufObj* tasks,
    ProtobufObj* filters);

SchedulerDriverStatus scheduler_start(SchedulerDriverPtr driver);

SchedulerDriverStatus scheduler_stop(SchedulerDriverPtr driver, int failover);

SchedulerDriverStatus scheduler_abort(SchedulerDriverPtr driver);

SchedulerDriverStatus scheduler_join(SchedulerDriverPtr driver);

SchedulerDriverStatus scheduler_run(SchedulerDriverPtr driver);

SchedulerDriverStatus scheduler_requestResources(
    SchedulerDriverPtr driver,
    ProtobufObj* requestsData);

SchedulerDriverStatus scheduler_declineOffer(
    SchedulerDriverPtr driver,
    ProtobufObj* offerId,
    ProtobufObj* filters);

SchedulerDriverStatus scheduler_killTask(
    SchedulerDriverPtr driver,
    ProtobufObj* taskId);

SchedulerDriverStatus scheduler_reviveOffers(SchedulerDriverPtr driver);

SchedulerDriverStatus scheduler_sendFrameworkMessage(
    SchedulerDriverPtr driver,
    ProtobufObj* executor,
    ProtobufObj* slaveId,
    const char* data);

SchedulerPtrPair scheduler_init(
    SchedulerCallbacks* callbacks,
    void* payload,
    ProtobufObj* framework,
    const char* master);

void scheduler_destroy(void* driver, void* scheduler);

//
// Executor driver calls
//

ExecutorDriverStatus executor_start(ExecutorDriverPtr driver);

ExecutorDriverStatus executor_stop(ExecutorDriverPtr driver);

ExecutorDriverStatus executor_abort(ExecutorDriverPtr driver);

ExecutorDriverStatus executor_join(ExecutorDriverPtr driver);

ExecutorDriverStatus executor_run(ExecutorDriverPtr driver);

ExecutorDriverStatus executor_sendStatusUpdate(
    ExecutorDriverPtr driver,
    ProtobufObj* status);

ExecutorDriverStatus executor_sendFrameworkMessage(
    ExecutorDriverPtr driver,
    const char* data);

ExecutorPtrPair executor_init(
    ExecutorCallbacks* callbacks,
    void* payload);

void executor_destroy(void* driver, void* executor);

#ifdef __cplusplus
}
#endif

#endif // __MESOS_C_API_HPP__ 
