package scheduler

import "fmt"
import "github.com/GoogleCloudPlatform/kubernetes/pkg/cloudprovider"

// implements the minion.Registry interface
type CloudRegistry struct {
	cloud cloudprovider.Interface // assumed compatible with MesosCloud implementation
}

func NewCloudRegistry(c cloudprovider.Interface) *CloudRegistry {
	return &CloudRegistry{c}
}

func (r *CloudRegistry) Contains(minion string) (bool, error) {
	instances, err := r.List()
	if err != nil {
		return false, err
	}
	for _, name := range instances {
		if name == minion {
			return true, nil
		}
	}
	return false, nil
}

func (r CloudRegistry) Delete(minion string) error {
	return fmt.Errorf("unsupported")
}

func (r CloudRegistry) Insert(minion string) error {
	return fmt.Errorf("unsupported")
}

func (r *CloudRegistry) List() ([]string, error) {
	instances, ok := r.cloud.Instances()
	if !ok {
		return nil, fmt.Errorf("cloud doesn't support instances")
	}
	return instances.List("")
}
