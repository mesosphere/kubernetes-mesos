/*
Package framework includes a kubernetes framework.
that implements the interfaces of:

1: The mesos scheduler.

2: The kubernetes scheduler.

3: The kubernetes pod registry.

It acts as the 'scheduler' and the 'registry' of the PodRegistryStorage
to provide scheduling and Pod management on top of mesos.
*/
package scheduler
