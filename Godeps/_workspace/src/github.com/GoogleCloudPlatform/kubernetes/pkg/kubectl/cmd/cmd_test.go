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

package cmd

import (
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"testing"

	"github.com/GoogleCloudPlatform/kubernetes/pkg/api"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/api/latest"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/api/meta"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/api/validation"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/client"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/kubectl"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/kubectl/resource"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/runtime"
)

type internalType struct {
	Kind       string
	APIVersion string

	Name string
}

type externalType struct {
	Kind       string `json:"kind"`
	APIVersion string `json:"apiVersion"`

	Name string `json:"name"`
}

func (*internalType) IsAnAPIObject() {}
func (*externalType) IsAnAPIObject() {}

func newExternalScheme() (*runtime.Scheme, meta.RESTMapper, runtime.Codec) {
	scheme := runtime.NewScheme()
	scheme.AddKnownTypeWithName("", "Type", &internalType{})
	scheme.AddKnownTypeWithName("unlikelyversion", "Type", &externalType{})

	codec := runtime.CodecFor(scheme, "unlikelyversion")
	mapper := meta.NewDefaultRESTMapper([]string{"unlikelyversion"}, func(version string) (*meta.VersionInterfaces, bool) {
		return &meta.VersionInterfaces{
			Codec:            codec,
			ObjectConvertor:  scheme,
			MetadataAccessor: meta.NewAccessor(),
		}, (version == "unlikelyversion")
	})
	for _, version := range []string{"unlikelyversion"} {
		for kind := range scheme.KnownTypes(version) {
			mixedCase := false
			scope := meta.RESTScopeNamespace
			mapper.Add(scope, kind, version, mixedCase)
		}
	}

	return scheme, mapper, codec
}

type testPrinter struct {
	Objects []runtime.Object
	Err     error
}

func (t *testPrinter) PrintObj(obj runtime.Object, out io.Writer) error {
	t.Objects = append(t.Objects, obj)
	fmt.Fprintf(out, "%#v", obj)
	return t.Err
}

type testDescriber struct {
	Name, Namespace string
	Output          string
	Err             error
}

func (t *testDescriber) Describe(namespace, name string) (output string, err error) {
	t.Namespace, t.Name = namespace, name
	return t.Output, t.Err
}

type testFactory struct {
	Mapper       meta.RESTMapper
	Typer        runtime.ObjectTyper
	Client       kubectl.RESTClient
	Describer    kubectl.Describer
	Printer      kubectl.ResourcePrinter
	Validator    validation.Schema
	Namespace    string
	ClientConfig *client.Config
	Err          error
}

func NewTestFactory() (*Factory, *testFactory, runtime.Codec) {
	scheme, mapper, codec := newExternalScheme()
	t := &testFactory{
		Validator: validation.NullSchema{},
		Mapper:    mapper,
		Typer:     scheme,
	}
	return &Factory{
		Object: func() (meta.RESTMapper, runtime.ObjectTyper) {
			return t.Mapper, t.Typer
		},
		RESTClient: func(*meta.RESTMapping) (resource.RESTClient, error) {
			return t.Client, t.Err
		},
		Describer: func(*meta.RESTMapping) (kubectl.Describer, error) {
			return t.Describer, t.Err
		},
		Printer: func(mapping *meta.RESTMapping, noHeaders bool) (kubectl.ResourcePrinter, error) {
			return t.Printer, t.Err
		},
		Validator: func() (validation.Schema, error) {
			return t.Validator, t.Err
		},
		DefaultNamespace: func() (string, error) {
			return t.Namespace, t.Err
		},
		ClientConfig: func() (*client.Config, error) {
			return t.ClientConfig, t.Err
		},
	}, t, codec
}

func NewAPIFactory() (*Factory, *testFactory, runtime.Codec) {
	t := &testFactory{
		Validator: validation.NullSchema{},
	}
	return &Factory{
		Object: func() (meta.RESTMapper, runtime.ObjectTyper) {
			return latest.RESTMapper, api.Scheme
		},
		RESTClient: func(*meta.RESTMapping) (resource.RESTClient, error) {
			return t.Client, t.Err
		},
		Describer: func(*meta.RESTMapping) (kubectl.Describer, error) {
			return t.Describer, t.Err
		},
		Printer: func(mapping *meta.RESTMapping, noHeaders bool) (kubectl.ResourcePrinter, error) {
			return t.Printer, t.Err
		},
		Validator: func() (validation.Schema, error) {
			return t.Validator, t.Err
		},
		DefaultNamespace: func() (string, error) {
			return t.Namespace, t.Err
		},
		ClientConfig: func() (*client.Config, error) {
			return t.ClientConfig, t.Err
		},
	}, t, latest.Codec
}

func objBody(codec runtime.Codec, obj runtime.Object) io.ReadCloser {
	return ioutil.NopCloser(bytes.NewReader([]byte(runtime.EncodeOrDie(codec, obj))))
}

func stringBody(body string) io.ReadCloser {
	return ioutil.NopCloser(bytes.NewReader([]byte(body)))
}

// Verify that resource.RESTClients constructed from a factory respect mapping.APIVersion
func TestClientVersions(t *testing.T) {
	f := NewFactory(nil)

	versions := []string{
		"v1beta1",
		"v1beta2",
		"v1beta3",
	}
	for _, version := range versions {
		mapping := &meta.RESTMapping{
			APIVersion: version,
		}
		c, err := f.RESTClient(mapping)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		client := c.(*client.RESTClient)
		if client.APIVersion() != version {
			t.Errorf("unexpected Client APIVersion: %s %v", client.APIVersion, client)
		}
	}
}

func ExamplePrintReplicationController() {
	f, tf, codec := NewAPIFactory()
	tf.Printer = kubectl.NewHumanReadablePrinter(false)
	tf.Client = &client.FakeRESTClient{
		Codec:  codec,
		Client: nil,
	}
	cmd := f.NewCmdRunContainer(os.Stdout)
	ctrl := &api.ReplicationController{
		ObjectMeta: api.ObjectMeta{
			Name:   "foo",
			Labels: map[string]string{"foo": "bar"},
		},
		Spec: api.ReplicationControllerSpec{
			Replicas: 1,
			Selector: map[string]string{"foo": "bar"},
			Template: &api.PodTemplateSpec{
				ObjectMeta: api.ObjectMeta{
					Labels: map[string]string{"foo": "bar"},
				},
				Spec: api.PodSpec{
					Containers: []api.Container{
						{
							Name:  "foo",
							Image: "someimage",
						},
					},
				},
			},
		},
	}
	err := f.PrintObject(cmd, ctrl, os.Stdout)
	if err != nil {
		fmt.Printf("Unexpected error: %v", err)
	}
	// Output:
	// CONTROLLER   CONTAINER(S)   IMAGE(S)    SELECTOR   REPLICAS
	// foo          foo            someimage   foo=bar    1
}
