# Tuning

There are more settings which can be customized to adapt k8sm's behaviour to you environment. Be warned though that some them are pretty low-level and one has to know the inner workings of k8sm to find sensible values. Moreover, these
settings may change or even disappear from version to version without further notice.

## Scheduler Configuration

The scheduler configuration can be fine-tuned using an ini-style configuration file. The filename is passed via `--scheduler-config` to the `km scheduler` command. The following settings are the default:

```
[scheduler]
; duration an offer is viable, prior to being expired
offer-ttl = 5s

; duration an expired offer lingers in history
offer-linger-ttl = 2m

; duration between offer listener notifications
listener-delay = 1s

; size of the pod updates channel
updates-backlog = 2048

; interval we update the frameworkId stored in etcd
framework-id-refresh-interval = 30s

; wait this amount of time after initial registration before attempting
; implicit reconciliation
initial-implicit-reconciliation-delay = 15s

; interval in between internal task status checks/updates
explicit-reconciliation-max-backoff = 2m

; waiting period after attempting to cancel an ongoing reconciliation
explicit-reconciliation-abort-timeout = 30s

initial-pod-backoff = 1s
max-pod-backoff = 60s
http-handler-timeout = 10s
http-bind-interval = 5s
```
