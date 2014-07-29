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
	"fmt"
	"strings"
)

type StringList []string

func (sl *StringList) String() string {
	return fmt.Sprint(*sl)
}

func (sl *StringList) Set(value string) error {
	for _, s := range strings.Split(value, ",") {
		if len(s) == 0 {
			return fmt.Errorf("value should not be an empty string")
		}
		*sl = append(*sl, s)
	}
	return nil
}
