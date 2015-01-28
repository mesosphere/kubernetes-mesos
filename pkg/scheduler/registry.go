package scheduler

import (
	"github.com/GoogleCloudPlatform/kubernetes/pkg/api"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/registry/etcd"
)

/**
HACK(jdef): we're not using etcd but k8s has implemented namespace support and
we're going to try to honor that by namespacing pod keys. Hence, the following
funcs that were stolen from:
    https://github.com/GoogleCloudPlatform/kubernetes/blob/release-0.5/pkg/registry/etcd/etcd.go
**/

const PodPath = "/pods"

// makePodKey constructs etcd paths to pod items enforcing namespace rules.
func makePodKey(ctx api.Context, id string) (string, error) {
	return etcd.MakeEtcdItemKey(ctx, PodPath, id)
}
