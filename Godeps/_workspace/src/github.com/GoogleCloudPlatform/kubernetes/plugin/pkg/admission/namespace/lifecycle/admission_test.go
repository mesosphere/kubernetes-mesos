/*
Copyright 2015 Google Inc. All rights reserved.

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

package lifecycle

import (
	"testing"

	"github.com/GoogleCloudPlatform/kubernetes/pkg/admission"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/api"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/client"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/client/cache"
)

// TestAdmission
func TestAdmission(t *testing.T) {
	namespaceObj := &api.Namespace{
		ObjectMeta: api.ObjectMeta{
			Name:      "test",
			Namespace: "",
		},
		Status: api.NamespaceStatus{
			Phase: api.NamespaceActive,
		},
	}
	store := cache.NewStore(cache.MetaNamespaceIndexFunc)
	store.Add(namespaceObj)
	mockClient := &client.Fake{}
	handler := &lifecycle{
		client: mockClient,
		store:  store,
	}
	pod := api.Pod{
		ObjectMeta: api.ObjectMeta{Name: "123", Namespace: namespaceObj.Namespace},
		Spec: api.PodSpec{
			Volumes:    []api.Volume{{Name: "vol"}},
			Containers: []api.Container{{Name: "ctr", Image: "image"}},
		},
	}
	err := handler.Admit(admission.NewAttributesRecord(&pod, namespaceObj.Namespace, "pods", "CREATE"))
	if err != nil {
		t.Errorf("Unexpected error returned from admission handler: %v", err)
	}

	// change namespace state to terminating
	namespaceObj.Status.Phase = api.NamespaceTerminating
	store.Add(namespaceObj)

	// verify create operations in the namespace cause an error
	err = handler.Admit(admission.NewAttributesRecord(&pod, namespaceObj.Namespace, "pods", "CREATE"))
	if err == nil {
		t.Errorf("Expected error rejecting creates in a namespace when it is terminating")
	}

	// verify update operations in the namespace can proceed
	err = handler.Admit(admission.NewAttributesRecord(&pod, namespaceObj.Namespace, "pods", "UPDATE"))
	if err != nil {
		t.Errorf("Unexpected error returned from admission handler: %v", err)
	}

	// verify delete operations in the namespace can proceed
	err = handler.Admit(admission.NewAttributesRecord(nil, namespaceObj.Namespace, "pods", "DELETE"))
	if err != nil {
		t.Errorf("Unexpected error returned from admission handler: %v", err)
	}

}
