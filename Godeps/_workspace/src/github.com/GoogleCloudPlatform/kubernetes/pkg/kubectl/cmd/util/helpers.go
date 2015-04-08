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
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/GoogleCloudPlatform/kubernetes/pkg/api/errors"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/api/latest"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/client"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/runtime"
	"github.com/evanphx/json-patch"

	"github.com/golang/glog"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func CheckErr(err error) {
	if err != nil {
		if errors.IsStatusError(err) {
			glog.FatalDepth(1, fmt.Sprintf("Error received from API: %s", err.Error()))
		}
		if errors.IsUnexpectedObjectError(err) {
			glog.FatalDepth(1, fmt.Sprintf("Unexpected object received from server: %s", err.Error()))
		}
		if client.IsUnexpectedStatusError(err) {
			glog.FatalDepth(1, fmt.Sprintf("Unexpected status received from server: %s", err.Error()))
		}
		glog.FatalDepth(1, fmt.Sprintf("Client error: %s", err.Error()))
	}
}

func UsageError(cmd *cobra.Command, format string, args ...interface{}) error {
	msg := fmt.Sprintf(format, args...)
	return fmt.Errorf("%s\nsee '%s -h' for help.", msg, cmd.CommandPath())
}

func getFlag(cmd *cobra.Command, flag string) *pflag.Flag {
	f := cmd.Flags().Lookup(flag)
	if f == nil {
		glog.Fatalf("flag accessed but not defined for command %s: %s", cmd.Name(), flag)
	}
	return f
}

func GetFlagString(cmd *cobra.Command, flag string) string {
	f := getFlag(cmd, flag)
	return f.Value.String()
}

func GetFlagBool(cmd *cobra.Command, flag string) bool {
	f := getFlag(cmd, flag)
	result, err := strconv.ParseBool(f.Value.String())
	if err != nil {
		glog.Fatalf("Invalid value for a boolean flag: %s", f.Value.String())
	}
	return result
}

// Assumes the flag has a default value.
func GetFlagInt(cmd *cobra.Command, flag string) int {
	f := getFlag(cmd, flag)
	v, err := strconv.Atoi(f.Value.String())
	// This is likely not a sufficiently friendly error message, but cobra
	// should prevent non-integer values from reaching here.
	if err != nil {
		glog.Fatalf("unable to convert flag value to int: %v", err)
	}
	return v
}

func GetFlagDuration(cmd *cobra.Command, flag string) time.Duration {
	f := getFlag(cmd, flag)
	v, err := time.ParseDuration(f.Value.String())
	if err != nil {
		glog.Fatalf("unable to convert flag value to Duration: %v", err)
	}
	return v
}

func ReadConfigDataFromReader(reader io.Reader, source string) ([]byte, error) {
	data, err := ioutil.ReadAll(reader)
	if err != nil {
		return nil, err
	}

	if len(data) == 0 {
		return nil, fmt.Errorf(`Read from %s but no data found`, source)
	}

	return data, nil
}

// ReadConfigData reads the bytes from the specified filesytem or network
// location or from stdin if location == "-".
// TODO: replace with resource.Builder
func ReadConfigData(location string) ([]byte, error) {
	if len(location) == 0 {
		return nil, fmt.Errorf("location given but empty")
	}

	if location == "-" {
		// Read from stdin.
		return ReadConfigDataFromReader(os.Stdin, "stdin ('-')")
	}

	// Use the location as a file path or URL.
	return ReadConfigDataFromLocation(location)
}

// TODO: replace with resource.Builder
func ReadConfigDataFromLocation(location string) ([]byte, error) {
	// we look for http:// or https:// to determine if valid URL, otherwise do normal file IO
	if strings.Index(location, "http://") == 0 || strings.Index(location, "https://") == 0 {
		resp, err := http.Get(location)
		if err != nil {
			return nil, fmt.Errorf("unable to access URL %s: %v\n", location, err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			return nil, fmt.Errorf("unable to read URL, server reported %d %s", resp.StatusCode, resp.Status)
		}
		return ReadConfigDataFromReader(resp.Body, location)
	} else {
		file, err := os.Open(location)
		if err != nil {
			return nil, fmt.Errorf("unable to read %s: %v\n", location, err)
		}
		return ReadConfigDataFromReader(file, location)
	}
}

func Merge(dst runtime.Object, fragment, kind string) (runtime.Object, error) {
	// Ok, this is a little hairy, we'd rather not force the user to specify a kind for their JSON
	// So we pull it into a map, add the Kind field, and then reserialize.
	// We also pull the apiVersion for proper parsing
	var intermediate interface{}
	if err := json.Unmarshal([]byte(fragment), &intermediate); err != nil {
		return nil, err
	}
	dataMap, ok := intermediate.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("Expected a map, found something else: %s", fragment)
	}
	version, found := dataMap["apiVersion"]
	if !found {
		return nil, fmt.Errorf("Inline JSON requires an apiVersion field")
	}
	versionString, ok := version.(string)
	if !ok {
		return nil, fmt.Errorf("apiVersion must be a string")
	}
	i, err := latest.InterfacesFor(versionString)
	if err != nil {
		return nil, err
	}

	// encode dst into versioned json and apply fragment directly too it
	target, err := i.Codec.Encode(dst)
	if err != nil {
		return nil, err
	}
	patched, err := jsonpatch.MergePatch(target, []byte(fragment))
	if err != nil {
		return nil, err
	}
	out, err := i.Codec.Decode(patched)
	if err != nil {
		return nil, err
	}
	return out, nil
}
