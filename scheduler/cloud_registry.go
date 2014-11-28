package scheduler

import "fmt"
import "github.com/GoogleCloudPlatform/kubernetes/pkg/api"
import "github.com/GoogleCloudPlatform/kubernetes/pkg/cloudprovider"
import "github.com/GoogleCloudPlatform/kubernetes/pkg/registry/minion"

// implements the minion.Registry interface
type CloudRegistry struct {
	cloud cloudprovider.Interface // assumed compatible with MesosCloud implementation
}

func NewCloudRegistry(c cloudprovider.Interface) *CloudRegistry {
	return &CloudRegistry{c}
}

func (r *CloudRegistry) DeleteMinion(api.Context, string) error {
	return fmt.Errorf("unsupported")
}

func (r *CloudRegistry) CreateMinion(api.Context, *api.Minion) error {
	return fmt.Errorf("unsupported")
}

func (r *CloudRegistry) GetMinion(ctx api.Context, minionId string) (*api.Minion, error) {
	instances, ok := r.cloud.Instances()
	if !ok {
		return nil, fmt.Errorf("cloud doesn't support instances")
	}
	hostnames, err := instances.List("")
	if err != nil {
		return nil, err
	}
	for _, m := range hostnames {
		if m != minionId {
			continue
		}
		return toApiMinion(ctx, m), nil
	}
	return nil, minion.ErrDoesNotExist
}

func (r *CloudRegistry) ListMinions(ctx api.Context) (*api.MinionList, error) {
	instances, ok := r.cloud.Instances()
	if !ok {
		return nil, fmt.Errorf("cloud doesn't support instances")
	}
	minions := []api.Minion{}
	hostnames, err := instances.List("")
	if err != nil {
		return nil, err
	}
	for _, m := range hostnames {
		minions = append(minions, *(toApiMinion(ctx, m)))
	}
	ns, _ := api.NamespaceFrom(ctx)
	return &api.MinionList{
		TypeMeta: api.TypeMeta{Kind: "minionList", Namespace: ns},
		Items:    minions,
	}, nil
}

func toApiMinion(ctx api.Context, hostname string) *api.Minion {
	ns, _ := api.NamespaceFrom(ctx)
	return &api.Minion{
		TypeMeta: api.TypeMeta{
			ID:        hostname,
			Kind:      "minion",
			Namespace: ns,
		},
		HostIP: hostname,
	}
}
