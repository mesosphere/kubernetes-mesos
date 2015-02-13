package podtask

// Concepts that have leaked to where they should not have.

import (
	"github.com/GoogleCloudPlatform/kubernetes/pkg/api"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/registry/etcd"
)

// makePodKey constructs etcd paths to pod items enforcing namespace rules.
func MakePodKey(ctx api.Context, id string) (string, error) {
	return etcd.MakeEtcdItemKey(ctx, PodPath, id)
}
