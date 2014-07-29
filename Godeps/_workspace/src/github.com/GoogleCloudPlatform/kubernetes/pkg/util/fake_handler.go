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

package util

import (
	"io/ioutil"
	"net/http"
)

// TestInterface is a simple interface providing Errorf, to make injection for
// testing easier (insert 'yo dawg' meme here)
type TestInterface interface {
	Errorf(format string, args ...interface{})
}

// LogInterface is a simple interface to allow injection of Logf to report serving errors.
type LogInterface interface {
	Logf(format string, args ...interface{})
}

// FakeHandler is to assist in testing HTTP requests.
type FakeHandler struct {
	RequestReceived *http.Request
	RequestBody     string
	StatusCode      int
	ResponseBody    string
	// For logging - you can use a *testing.T
	// This will keep log messages associated with the test.
	T LogInterface
}

func (f *FakeHandler) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	f.RequestReceived = request
	response.WriteHeader(f.StatusCode)
	response.Write([]byte(f.ResponseBody))

	bodyReceived, err := ioutil.ReadAll(request.Body)
	if err != nil && f.T != nil {
		f.T.Logf("Received read error: %#v", err)
	}
	f.RequestBody = string(bodyReceived)
}

// ValidateRequest verifies that FakeHandler received a request with expected path, method, and body.
func (f FakeHandler) ValidateRequest(t TestInterface, expectedPath, expectedMethod string, body *string) {
	if f.RequestReceived.URL.Path != expectedPath {
		t.Errorf("Unexpected request path for request %#v, received: %q, expected: %q", f.RequestReceived, f.RequestReceived.URL.Path, expectedPath)
	}
	if f.RequestReceived.Method != expectedMethod {
		t.Errorf("Unexpected method: %q, expected: %q", f.RequestReceived.Method, expectedMethod)
	}
	if body != nil {
		if *body != f.RequestBody {
			t.Errorf("Received body:\n%s\n Doesn't match expected body:\n%s", f.RequestBody, *body)
		}
	}
}
