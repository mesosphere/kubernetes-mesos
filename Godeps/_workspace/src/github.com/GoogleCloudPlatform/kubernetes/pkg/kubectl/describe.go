/*
Copyright 2014 Google Inc. All rights reserved.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package kubectl

import (
	"fmt"
	"io"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/GoogleCloudPlatform/kubernetes/pkg/api"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/client"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/labels"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/types"
	"github.com/golang/glog"
)

// Describer generates output for the named resource or an error
// if the output could not be generated. Implementors typically
// abstract the retrieval of the named object from a remote server.
type Describer interface {
	Describe(namespace, name string) (output string, err error)
}

// ObjectDescriber is an interface for displaying arbitrary objects with extra
// information. Use when an object is in hand (on disk, or already retrieved).
// Implementors may ignore the additional information passed on extra, or use it
// by default. ObjectDescribers may return ErrNoDescriber if no suitable describer
// is found.
type ObjectDescriber interface {
	DescribeObject(object interface{}, extra ...interface{}) (output string, err error)
}

// ErrNoDescriber is a structured error indicating the provided object or objects
// cannot be described.
type ErrNoDescriber struct {
	Types []string
}

// Error implements the error interface.
func (e ErrNoDescriber) Error() string {
	return fmt.Sprintf("no describer has been defined for %v", e.Types)
}

// Describer returns the default describe functions for each of the standard
// Kubernetes types.
func DescriberFor(kind string, c *client.Client) (Describer, bool) {
	switch kind {
	case "Pod":
		return &PodDescriber{c}, true
	case "ReplicationController":
		return &ReplicationControllerDescriber{c}, true
	case "Service":
		return &ServiceDescriber{c}, true
	case "Minion", "Node":
		return &NodeDescriber{c}, true
	case "LimitRange":
		return &LimitRangeDescriber{c}, true
	case "ResourceQuota":
		return &ResourceQuotaDescriber{c}, true
	}
	return nil, false
}

// DefaultObjectDescriber can describe the default Kubernetes objects.
var DefaultObjectDescriber ObjectDescriber

func init() {
	d := &Describers{}
	err := d.Add(
		describeLimitRange,
		describeQuota,
		describePod,
		describeService,
		describeReplicationController,
		describeNode,
	)
	if err != nil {
		glog.Fatalf("Cannot register describers: %v", err)
	}
	DefaultObjectDescriber = d
}

// LimitRangeDescriber generates information about a limit range
type LimitRangeDescriber struct {
	client.Interface
}

func (d *LimitRangeDescriber) Describe(namespace, name string) (string, error) {
	lr := d.LimitRanges(namespace)

	limitRange, err := lr.Get(name)
	if err != nil {
		return "", err
	}
	return describeLimitRange(limitRange)
}

func describeLimitRange(limitRange *api.LimitRange) (string, error) {
	return tabbedString(func(out io.Writer) error {
		fmt.Fprintf(out, "Name:\t%s\n", limitRange.Name)
		fmt.Fprintf(out, "Type\tResource\tMin\tMax\n")
		fmt.Fprintf(out, "----\t--------\t---\t---\n")
		for i := range limitRange.Spec.Limits {
			item := limitRange.Spec.Limits[i]
			maxResources := item.Max
			minResources := item.Min

			set := map[api.ResourceName]bool{}
			for k := range maxResources {
				set[k] = true
			}
			for k := range minResources {
				set[k] = true
			}

			for k := range set {
				// if no value is set, we output -
				maxValue := "-"
				minValue := "-"

				maxQuantity, maxQuantityFound := maxResources[k]
				if maxQuantityFound {
					maxValue = maxQuantity.String()
				}

				minQuantity, minQuantityFound := minResources[k]
				if minQuantityFound {
					minValue = minQuantity.String()
				}

				msg := "%v\t%v\t%v\t%v\n"
				fmt.Fprintf(out, msg, item.Type, k, minValue, maxValue)
			}
		}
		return nil
	})
}

// ResourceQuotaDescriber generates information about a resource quota
type ResourceQuotaDescriber struct {
	client.Interface
}

func (d *ResourceQuotaDescriber) Describe(namespace, name string) (string, error) {
	rq := d.ResourceQuotas(namespace)

	resourceQuota, err := rq.Get(name)
	if err != nil {
		return "", err
	}

	return describeQuota(resourceQuota)
}

func describeQuota(resourceQuota *api.ResourceQuota) (string, error) {
	return tabbedString(func(out io.Writer) error {
		fmt.Fprintf(out, "Name:\t%s\n", resourceQuota.Name)
		fmt.Fprintf(out, "Resource\tUsed\tHard\n")
		fmt.Fprintf(out, "--------\t----\t----\n")

		resources := []api.ResourceName{}
		for resource := range resourceQuota.Status.Hard {
			resources = append(resources, resource)
		}
		sort.Sort(SortableResourceNames(resources))

		msg := "%v\t%v\t%v\n"
		for i := range resources {
			resource := resources[i]
			hardQuantity := resourceQuota.Status.Hard[resource]
			usedQuantity := resourceQuota.Status.Used[resource]
			fmt.Fprintf(out, msg, resource, usedQuantity.String(), hardQuantity.String())
		}
		return nil
	})
}

// PodDescriber generates information about a pod and the replication controllers that
// create it.
type PodDescriber struct {
	client.Interface
}

func (d *PodDescriber) Describe(namespace, name string) (string, error) {
	rc := d.ReplicationControllers(namespace)
	pc := d.Pods(namespace)

	pod, err := pc.Get(name)
	if err != nil {
		eventsInterface := d.Events(namespace)
		events, err2 := eventsInterface.List(
			labels.Everything(),
			eventsInterface.GetFieldSelector(&name, &namespace, nil, nil))
		if err2 == nil && len(events.Items) > 0 {
			return tabbedString(func(out io.Writer) error {
				fmt.Fprintf(out, "Pod '%v': error '%v', but found events.\n", name, err)
				describeEvents(events, out)
				return nil
			})
		}
		return "", err
	}

	var events *api.EventList
	if ref, err := api.GetReference(pod); err != nil {
		glog.Errorf("Unable to construct reference to '%#v': %v", pod, err)
	} else {
		ref.Kind = ""
		events, _ = d.Events(namespace).Search(ref)
	}

	rcs, err := getReplicationControllersForLabels(rc, labels.Set(pod.Labels))
	if err != nil {
		return "", err
	}

	return describePod(pod, rcs, events)
}

func describePod(pod *api.Pod, rcs []api.ReplicationController, events *api.EventList) (string, error) {
	return tabbedString(func(out io.Writer) error {
		fmt.Fprintf(out, "Name:\t%s\n", pod.Name)
		fmt.Fprintf(out, "Image(s):\t%s\n", makeImageList(&pod.Spec))
		fmt.Fprintf(out, "Host:\t%s\n", pod.Status.Host+"/"+pod.Status.HostIP)
		fmt.Fprintf(out, "Labels:\t%s\n", formatLabels(pod.Labels))
		fmt.Fprintf(out, "Status:\t%s\n", string(pod.Status.Phase))
		fmt.Fprintf(out, "Replication Controllers:\t%s\n", printReplicationControllersByLabels(rcs))
		if len(pod.Status.Conditions) > 0 {
			fmt.Fprint(out, "Conditions:\n  Type\tStatus\n")
			for _, c := range pod.Status.Conditions {
				fmt.Fprintf(out, "  %v \t%v \n",
					c.Type,
					c.Status)
			}
		}
		if events != nil {
			describeEvents(events, out)
		}
		return nil
	})
}

// ReplicationControllerDescriber generates information about a replication controller
// and the pods it has created.
type ReplicationControllerDescriber struct {
	client.Interface
}

func (d *ReplicationControllerDescriber) Describe(namespace, name string) (string, error) {
	rc := d.ReplicationControllers(namespace)
	pc := d.Pods(namespace)

	controller, err := rc.Get(name)
	if err != nil {
		return "", err
	}

	running, waiting, succeeded, failed, err := getPodStatusForReplicationController(pc, controller)
	if err != nil {
		return "", err
	}

	events, _ := d.Events(namespace).Search(controller)

	return describeReplicationController(controller, events, running, waiting, succeeded, failed)
}

func describeReplicationController(controller *api.ReplicationController, events *api.EventList, running, waiting, succeeded, failed int) (string, error) {
	return tabbedString(func(out io.Writer) error {
		fmt.Fprintf(out, "Name:\t%s\n", controller.Name)
		if controller.Spec.Template != nil {
			fmt.Fprintf(out, "Image(s):\t%s\n", makeImageList(&controller.Spec.Template.Spec))
		} else {
			fmt.Fprintf(out, "Image(s):\t%s\n", "<no template>")
		}
		fmt.Fprintf(out, "Selector:\t%s\n", formatLabels(controller.Spec.Selector))
		fmt.Fprintf(out, "Labels:\t%s\n", formatLabels(controller.Labels))
		fmt.Fprintf(out, "Replicas:\t%d current / %d desired\n", controller.Status.Replicas, controller.Spec.Replicas)
		fmt.Fprintf(out, "Pods Status:\t%d Running / %d Waiting / %d Succeeded / %d Failed\n", running, waiting, succeeded, failed)
		if events != nil {
			describeEvents(events, out)
		}
		return nil
	})
}

// ServiceDescriber generates information about a service.
type ServiceDescriber struct {
	client.Interface
}

func (d *ServiceDescriber) Describe(namespace, name string) (string, error) {
	c := d.Services(namespace)

	service, err := c.Get(name)
	if err != nil {
		return "", err
	}

	endpoints, _ := d.Endpoints(namespace).Get(name)
	events, _ := d.Events(namespace).Search(service)

	return describeService(service, endpoints, events)
}

func describeService(service *api.Service, endpoints *api.Endpoints, events *api.EventList) (string, error) {
	if endpoints == nil {
		endpoints = &api.Endpoints{}
	}
	return tabbedString(func(out io.Writer) error {
		fmt.Fprintf(out, "Name:\t%s\n", service.Name)
		fmt.Fprintf(out, "Labels:\t%s\n", formatLabels(service.Labels))
		fmt.Fprintf(out, "Selector:\t%s\n", formatLabels(service.Spec.Selector))
		fmt.Fprintf(out, "IP:\t%s\n", service.Spec.PortalIP)
		if len(service.Spec.PublicIPs) > 0 {
			list := strings.Join(service.Spec.PublicIPs, ", ")
			fmt.Fprintf(out, "Public IPs:\t%s\n", list)
		}
		fmt.Fprintf(out, "Port:\t%d\n", service.Spec.Port)
		fmt.Fprintf(out, "Endpoints:\t%s\n", formatEndpoints(endpoints))
		fmt.Fprintf(out, "Session Affinity:\t%s\n", service.Spec.SessionAffinity)
		if events != nil {
			describeEvents(events, out)
		}
		return nil
	})
}

// NodeDescriber generates information about a node.
type NodeDescriber struct {
	client.Interface
}

func (d *NodeDescriber) Describe(namespace, name string) (string, error) {
	mc := d.Nodes()
	node, err := mc.Get(name)
	if err != nil {
		return "", err
	}

	var pods []api.Pod
	allPods, err := d.Pods(namespace).List(labels.Everything())
	if err != nil {
		return "", err
	}
	for _, pod := range allPods.Items {
		if pod.Status.Host != name {
			continue
		}
		pods = append(pods, pod)
	}

	var events *api.EventList
	if ref, err := api.GetReference(node); err != nil {
		glog.Errorf("Unable to construct reference to '%#v': %v", node, err)
	} else {
		// TODO: We haven't decided the namespace for Node object yet.
		ref.UID = types.UID(ref.Name)
		events, _ = d.Events("").Search(ref)
	}

	return describeNode(node, pods, events)
}

func describeNode(node *api.Node, pods []api.Pod, events *api.EventList) (string, error) {
	return tabbedString(func(out io.Writer) error {
		fmt.Fprintf(out, "Name:\t%s\n", node.Name)
		fmt.Fprintf(out, "Labels:\t%s\n", formatLabels(node.Labels))
		fmt.Fprintf(out, "CreationTimestamp:\t%s\n", node.CreationTimestamp.Time.Format(time.RFC1123Z))
		if len(node.Status.Conditions) > 0 {
			fmt.Fprint(out, "Conditions:\n  Type\tStatus\tLastProbeTime\tLastTransitionTime\tReason\tMessage\n")
			for _, c := range node.Status.Conditions {
				fmt.Fprintf(out, "  %v \t%v \t%s \t%s \t%v \t%v\n",
					c.Type,
					c.Status,
					c.LastProbeTime.Time.Format(time.RFC1123Z),
					c.LastTransitionTime.Time.Format(time.RFC1123Z),
					c.Reason,
					c.Message)
			}
		}
		var addresses []string
		for _, address := range node.Status.Addresses {
			addresses = append(addresses, address.Address)
		}
		fmt.Fprintf(out, "Addresses:\t%s\n", strings.Join(addresses, ","))
		if len(node.Status.Capacity) > 0 {
			fmt.Fprintf(out, "Capacity:\n")
			for resource, value := range node.Status.Capacity {
				fmt.Fprintf(out, " %s:\t%s\n", resource, value.String())
			}
		}
		if len(node.Spec.PodCIDR) > 0 {
			fmt.Fprintf(out, "PodCIDR:\t%s\n", node.Spec.PodCIDR)
		}
		if len(node.Spec.ExternalID) > 0 {
			fmt.Fprintf(out, "ExternalID:\t%s\n", node.Spec.ExternalID)
		}
		fmt.Fprintf(out, "Pods:\t(%d in total)\n", len(pods))
		for _, pod := range pods {
			fmt.Fprintf(out, "  %s\n", pod.Name)
		}
		if events != nil {
			describeEvents(events, out)
		}
		return nil
	})
}

func describeEvents(el *api.EventList, w io.Writer) {
	if len(el.Items) == 0 {
		fmt.Fprint(w, "No events.")
		return
	}
	sort.Sort(SortableEvents(el.Items))
	fmt.Fprint(w, "Events:\n  FirstSeen\tLastSeen\tCount\tFrom\tSubobjectPath\tReason\tMessage\n")
	for _, e := range el.Items {
		fmt.Fprintf(w, "  %s\t%s\t%d\t%v\t%v\t%v\t%v\n",
			e.FirstTimestamp.Time.Format(time.RFC1123Z),
			e.LastTimestamp.Time.Format(time.RFC1123Z),
			e.Count,
			e.Source,
			e.InvolvedObject.FieldPath,
			e.Reason,
			e.Message)
	}
}

// Get all replication controllers whose selectors would match a given set of
// labels.
// TODO Move this to pkg/client and ideally implement it server-side (instead
// of getting all RC's and searching through them manually).
func getReplicationControllersForLabels(c client.ReplicationControllerInterface, labelsToMatch labels.Labels) ([]api.ReplicationController, error) {
	// Get all replication controllers.
	// TODO this needs a namespace scope as argument
	rcs, err := c.List(labels.Everything())
	if err != nil {
		return nil, fmt.Errorf("error getting replication controllers: %v", err)
	}

	// Find the ones that match labelsToMatch.
	var matchingRCs []api.ReplicationController
	for _, controller := range rcs.Items {
		selector := labels.SelectorFromSet(controller.Spec.Selector)
		if selector.Matches(labelsToMatch) {
			matchingRCs = append(matchingRCs, controller)
		}
	}
	return matchingRCs, nil
}

func printReplicationControllersByLabels(matchingRCs []api.ReplicationController) string {
	// Format the matching RC's into strings.
	var rcStrings []string
	for _, controller := range matchingRCs {
		rcStrings = append(rcStrings, fmt.Sprintf("%s (%d/%d replicas created)", controller.Name, controller.Status.Replicas, controller.Spec.Replicas))
	}

	list := strings.Join(rcStrings, ", ")
	if list == "" {
		return "<none>"
	}
	return list
}

func getPodStatusForReplicationController(c client.PodInterface, controller *api.ReplicationController) (running, waiting, succeeded, failed int, err error) {
	rcPods, err := c.List(labels.SelectorFromSet(controller.Spec.Selector))
	if err != nil {
		return
	}
	for _, pod := range rcPods.Items {
		switch pod.Status.Phase {
		case api.PodRunning:
			running++
		case api.PodPending:
			waiting++
		case api.PodSucceeded:
			succeeded++
		case api.PodFailed:
			failed++
		}
	}
	return
}

// newErrNoDescriber creates a new ErrNoDescriber with the names of the provided types.
func newErrNoDescriber(types ...reflect.Type) error {
	names := []string{}
	for _, t := range types {
		names = append(names, t.String())
	}
	return ErrNoDescriber{Types: names}
}

// Describers implements ObjectDescriber against functions registered via Add. Those functions can
// be strongly typed. Types are exactly matched (no conversion or assignable checks).
type Describers struct {
	searchFns map[reflect.Type][]typeFunc
}

// DescribeObject implements ObjectDescriber and will attempt to print the provided object to a string,
// if at least one describer function has been registered with the exact types passed, or if any
// describer can print the exact object in its first argument (the remainder will be provided empty
// values). If no function registered with Add can satisfy the passed objects, an ErrNoDescriber will
// be returned
// TODO: reorder and partial match extra.
func (d *Describers) DescribeObject(exact interface{}, extra ...interface{}) (string, error) {
	exactType := reflect.TypeOf(exact)
	fns, ok := d.searchFns[exactType]
	if !ok {
		return "", newErrNoDescriber(exactType)
	}
	if len(extra) == 0 {
		for _, typeFn := range fns {
			if len(typeFn.Extra) == 0 {
				return typeFn.Describe(exact, extra...)
			}
		}
		typeFn := fns[0]
		for _, t := range typeFn.Extra {
			v := reflect.New(t).Elem()
			extra = append(extra, v.Interface())
		}
		return fns[0].Describe(exact, extra...)
	}

	types := []reflect.Type{}
	for _, obj := range extra {
		types = append(types, reflect.TypeOf(obj))
	}
	for _, typeFn := range fns {
		if typeFn.Matches(types) {
			return typeFn.Describe(exact, extra...)
		}
	}
	return "", newErrNoDescriber(append([]reflect.Type{exactType}, types...)...)
}

// Add adds one or more describer functions to the Describer. The passed function must
// match the signature:
//
//     func(...) (string, error)
//
// Any number of arguments may be provided.
func (d *Describers) Add(fns ...interface{}) error {
	for _, fn := range fns {
		fv := reflect.ValueOf(fn)
		ft := fv.Type()
		if ft.Kind() != reflect.Func {
			return fmt.Errorf("expected func, got: %v", ft)
		}
		if ft.NumIn() == 0 {
			return fmt.Errorf("expected at least one 'in' params, got: %v", ft)
		}
		if ft.NumOut() != 2 {
			return fmt.Errorf("expected two 'out' params - (string, error), got: %v", ft)
		}
		types := []reflect.Type{}
		for i := 0; i < ft.NumIn(); i++ {
			types = append(types, ft.In(i))
		}
		if ft.Out(0) != reflect.TypeOf(string("")) {
			return fmt.Errorf("expected string return, got: %v", ft)
		}
		var forErrorType error
		// This convolution is necessary, otherwise TypeOf picks up on the fact
		// that forErrorType is nil.
		errorType := reflect.TypeOf(&forErrorType).Elem()
		if ft.Out(1) != errorType {
			return fmt.Errorf("expected error return, got: %v", ft)
		}

		exact := types[0]
		extra := types[1:]
		if d.searchFns == nil {
			d.searchFns = make(map[reflect.Type][]typeFunc)
		}
		fns := d.searchFns[exact]
		fn := typeFunc{Extra: extra, Fn: fv}
		fns = append(fns, fn)
		d.searchFns[exact] = fns
	}
	return nil
}

// typeFunc holds information about a describer function and the types it accepts
type typeFunc struct {
	Extra []reflect.Type
	Fn    reflect.Value
}

// Matches returns true when the passed types exactly match the Extra list.
// TODO: allow unordered types to be matched and reorderd.
func (fn typeFunc) Matches(types []reflect.Type) bool {
	if len(fn.Extra) != len(types) {
		return false
	}
	for i := range types {
		if fn.Extra[i] != types[i] {
			return false
		}
	}
	return true
}

// Describe invokes the nested function with the exact number of arguments.
func (fn typeFunc) Describe(exact interface{}, extra ...interface{}) (string, error) {
	values := []reflect.Value{reflect.ValueOf(exact)}
	for _, obj := range extra {
		values = append(values, reflect.ValueOf(obj))
	}
	out := fn.Fn.Call(values)
	s := out[0].Interface().(string)
	var err error
	if !out[1].IsNil() {
		err = out[1].Interface().(error)
	}
	return s, err
}
