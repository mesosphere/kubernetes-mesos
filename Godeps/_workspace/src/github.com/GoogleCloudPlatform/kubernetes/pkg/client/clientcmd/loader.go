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

package clientcmd

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"

	"github.com/ghodss/yaml"
	"github.com/imdario/mergo"

	clientcmdapi "github.com/GoogleCloudPlatform/kubernetes/pkg/client/clientcmd/api"
	clientcmdlatest "github.com/GoogleCloudPlatform/kubernetes/pkg/client/clientcmd/api/latest"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/util/errors"
)

const (
	RecommendedConfigPathFlag   = "kubeconfig"
	RecommendedConfigPathEnvVar = "KUBECONFIG"

	DefaultEnvVarIndex     = 0
	DefaultCurrentDirIndex = 1
	DefaultHomeDirIndex    = 2
)

// ClientConfigLoadingRules is an ExplicitPath and string slice of specific locations that are used for merging together a Config
// Callers can put the chain together however they want, but we'd recommend:
// [0] = EnvVarPath
// [1] = CurrentDirectoryPath
// [2] = HomeDirectoryPath
// ExplicitPath is special, because if a user specifically requests a certain file be used and error is reported if thie file is not present
type ClientConfigLoadingRules struct {
	ExplicitPath string
	Precedence   []string
}

// NewDefaultClientConfigLoadingRules returns a ClientConfigLoadingRules object with default fields filled in.  You are not required to
// use this constructor
func NewDefaultClientConfigLoadingRules() *ClientConfigLoadingRules {
	return &ClientConfigLoadingRules{
		Precedence: []string{os.Getenv(RecommendedConfigPathEnvVar), ".kubeconfig", os.Getenv("HOME") + "/.kube/.kubeconfig"},
	}
}

// Load takes the loading rules and merges together a Config object based on following order.
//   1.  ExplicitPath
//   2.  Precedence slice
// A missing ExplicitPath file produces an error. Empty filenames or other missing files are ignored.
// Read errors or files with non-deserializable content produce errors.
// The first file to set a particular map key wins and map key's value is never changed.
// BUT, if you set a struct value that is NOT contained inside of map, the value WILL be changed.
// This results in some odd looking logic to merge in one direction, merge in the other, and then merge the two.
// It also means that if two files specify a "red-user", only values from the first file's red-user are used.  Even
// non-conflicting entries from the second file's "red-user" are discarded.
// Relative paths inside of the .kubeconfig files are resolved against the .kubeconfig file's parent folder
// and only absolute file paths are returned.
func (rules *ClientConfigLoadingRules) Load() (*clientcmdapi.Config, error) {

	errlist := []error{}

	// Make sure a file we were explicitly told to use exists
	if len(rules.ExplicitPath) > 0 {
		if _, err := os.Stat(rules.ExplicitPath); os.IsNotExist(err) {
			errlist = append(errlist, fmt.Errorf("The config file %v does not exist", rules.ExplicitPath))
		}
	}

	kubeConfigFiles := []string{rules.ExplicitPath}
	kubeConfigFiles = append(kubeConfigFiles, rules.Precedence...)

	// first merge all of our maps
	mapConfig := clientcmdapi.NewConfig()
	for _, file := range kubeConfigFiles {
		if err := mergeConfigWithFile(mapConfig, file); err != nil {
			errlist = append(errlist, err)
		}
		if err := ResolveLocalPaths(file, mapConfig); err != nil {
			errlist = append(errlist, err)
		}
	}

	// merge all of the struct values in the reverse order so that priority is given correctly
	// errors are not added to the list the second time
	nonMapConfig := clientcmdapi.NewConfig()
	for i := len(kubeConfigFiles) - 1; i >= 0; i-- {
		file := kubeConfigFiles[i]
		mergeConfigWithFile(nonMapConfig, file)
		ResolveLocalPaths(file, nonMapConfig)
	}

	// since values are overwritten, but maps values are not, we can merge the non-map config on top of the map config and
	// get the values we expect.
	config := clientcmdapi.NewConfig()
	mergo.Merge(config, mapConfig)
	mergo.Merge(config, nonMapConfig)

	return config, errors.NewAggregate(errlist)
}

func mergeConfigWithFile(startingConfig *clientcmdapi.Config, filename string) error {
	if len(filename) == 0 {
		// no work to do
		return nil
	}

	config, err := LoadFromFile(filename)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("Error loading config file \"%s\": %v", filename, err)
	}

	mergo.Merge(startingConfig, config)

	return nil
}

// ResolveLocalPaths resolves all relative paths in the config object with respect to the parent directory of the filename
// this cannot be done directly inside of LoadFromFile because doing so there would make it impossible to load a file without
// modification of its contents.
func ResolveLocalPaths(filename string, config *clientcmdapi.Config) error {
	if len(filename) == 0 {
		return nil
	}

	configDir, err := filepath.Abs(filepath.Dir(filename))
	if err != nil {
		return fmt.Errorf("Could not determine the absolute path of config file %s: %v", filename, err)
	}

	resolvedClusters := make(map[string]clientcmdapi.Cluster)
	for key, cluster := range config.Clusters {
		cluster.CertificateAuthority = resolveLocalPath(configDir, cluster.CertificateAuthority)
		resolvedClusters[key] = cluster
	}
	config.Clusters = resolvedClusters

	resolvedAuthInfos := make(map[string]clientcmdapi.AuthInfo)
	for key, authInfo := range config.AuthInfos {
		authInfo.AuthPath = resolveLocalPath(configDir, authInfo.AuthPath)
		authInfo.ClientCertificate = resolveLocalPath(configDir, authInfo.ClientCertificate)
		authInfo.ClientKey = resolveLocalPath(configDir, authInfo.ClientKey)
		resolvedAuthInfos[key] = authInfo
	}
	config.AuthInfos = resolvedAuthInfos

	return nil
}

// resolveLocalPath makes the path absolute with respect to the startingDir
func resolveLocalPath(startingDir, path string) string {
	if len(path) == 0 {
		return path
	}
	if filepath.IsAbs(path) {
		return path
	}

	return filepath.Join(startingDir, path)
}

// LoadFromFile takes a filename and deserializes the contents into Config object
func LoadFromFile(filename string) (*clientcmdapi.Config, error) {
	kubeconfigBytes, err := ioutil.ReadFile(filename)
	if err != nil {
		return nil, err
	}
	return Load(kubeconfigBytes)
}

// Load takes a byte slice and deserializes the contents into Config object.
// Encapsulates deserialization without assuming the source is a file.
func Load(data []byte) (*clientcmdapi.Config, error) {
	config := clientcmdapi.NewConfig()
	// if there's no data in a file, return the default object instead of failing (DecodeInto reject empty input)
	if len(data) == 0 {
		return config, nil
	}

	if err := clientcmdlatest.Codec.DecodeInto(data, config); err != nil {
		return nil, err
	}
	return config, nil
}

// WriteToFile serializes the config to yaml and writes it out to a file.  If not present, it creates the file with the mode 0600.  If it is present
// it stomps the contents
func WriteToFile(config clientcmdapi.Config, filename string) error {
	content, err := Write(config)
	if err != nil {
		return err
	}
	if err := ioutil.WriteFile(filename, content, 0600); err != nil {
		return err
	}
	return nil
}

// Write serializes the config to yaml.
// Encapsulates serialization without assuming the destination is a file.
func Write(config clientcmdapi.Config) ([]byte, error) {
	json, err := clientcmdlatest.Codec.Encode(&config)
	if err != nil {
		return nil, err
	}
	content, err := yaml.JSONToYAML(json)
	if err != nil {
		return nil, err
	}
	return content, nil
}
