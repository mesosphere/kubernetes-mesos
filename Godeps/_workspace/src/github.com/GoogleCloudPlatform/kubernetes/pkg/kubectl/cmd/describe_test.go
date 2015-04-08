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
	"net/http"
	"testing"

	"github.com/GoogleCloudPlatform/kubernetes/pkg/client"
)

// Verifies that schemas that are not in the master tree of Kubernetes can be retrieved via Get.
func TestDescribeUnknownSchemaObject(t *testing.T) {
	d := &testDescriber{Output: "test output"}
	f, tf, codec := NewTestFactory()
	tf.Describer = d
	tf.Client = &client.FakeRESTClient{
		Codec: codec,
		Resp:  &http.Response{StatusCode: 200, Body: objBody(codec, &internalType{Name: "foo"})},
	}
	tf.Namespace = "non-default"
	buf := bytes.NewBuffer([]byte{})

	cmd := f.NewCmdDescribe(buf)
	cmd.Run(cmd, []string{"type", "foo"})

	if d.Name != "foo" || d.Namespace != "non-default" {
		t.Errorf("unexpected describer: %#v", d)
	}

	if buf.String() != fmt.Sprintf("%s\n", d.Output) {
		t.Errorf("unexpected output: %s", buf.String())
	}
}
