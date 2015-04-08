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

package config

import (
	"bytes"
	"io/ioutil"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/GoogleCloudPlatform/kubernetes/pkg/client/clientcmd"
	clientcmdapi "github.com/GoogleCloudPlatform/kubernetes/pkg/client/clientcmd/api"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/util"
)

func newRedFederalCowHammerConfig() clientcmdapi.Config {
	return clientcmdapi.Config{
		AuthInfos: map[string]clientcmdapi.AuthInfo{
			"red-user": {Token: "red-token"}},
		Clusters: map[string]clientcmdapi.Cluster{
			"cow-cluster": {Server: "http://cow.org:8080"}},
		Contexts: map[string]clientcmdapi.Context{
			"federal-context": {AuthInfo: "red-user", Cluster: "cow-cluster"}},
	}
}

type configCommandTest struct {
	args            []string
	startingConfig  clientcmdapi.Config
	expectedConfig  clientcmdapi.Config
	expectedOutputs []string
}

func TestSetCurrentContext(t *testing.T) {
	expectedConfig := newRedFederalCowHammerConfig()
	expectedConfig.CurrentContext = "the-new-context"
	test := configCommandTest{
		args:           []string{"use-context", "the-new-context"},
		startingConfig: newRedFederalCowHammerConfig(),
		expectedConfig: expectedConfig,
	}

	test.run(t)
}

func TestSetIntoExistingStruct(t *testing.T) {
	expectedConfig := newRedFederalCowHammerConfig()
	a := expectedConfig.AuthInfos["red-user"]
	authInfo := &a
	authInfo.AuthPath = "new-path-value"
	expectedConfig.AuthInfos["red-user"] = *authInfo
	test := configCommandTest{
		args:           []string{"set", "users.red-user.auth-path", "new-path-value"},
		startingConfig: newRedFederalCowHammerConfig(),
		expectedConfig: expectedConfig,
	}

	test.run(t)
}

func TestUnsetStruct(t *testing.T) {
	expectedConfig := newRedFederalCowHammerConfig()
	delete(expectedConfig.AuthInfos, "red-user")
	test := configCommandTest{
		args:           []string{"unset", "users.red-user"},
		startingConfig: newRedFederalCowHammerConfig(),
		expectedConfig: expectedConfig,
	}

	test.run(t)
}

func TestUnsetField(t *testing.T) {
	expectedConfig := newRedFederalCowHammerConfig()
	expectedConfig.AuthInfos["red-user"] = *clientcmdapi.NewAuthInfo()
	test := configCommandTest{
		args:           []string{"unset", "users.red-user.token"},
		startingConfig: newRedFederalCowHammerConfig(),
		expectedConfig: expectedConfig,
	}

	test.run(t)
}

func TestSetIntoNewStruct(t *testing.T) {
	expectedConfig := newRedFederalCowHammerConfig()
	cluster := clientcmdapi.NewCluster()
	cluster.Server = "new-server-value"
	expectedConfig.Clusters["big-cluster"] = *cluster
	test := configCommandTest{
		args:           []string{"set", "clusters.big-cluster.server", "new-server-value"},
		startingConfig: newRedFederalCowHammerConfig(),
		expectedConfig: expectedConfig,
	}

	test.run(t)
}

func TestSetBoolean(t *testing.T) {
	expectedConfig := newRedFederalCowHammerConfig()
	cluster := clientcmdapi.NewCluster()
	cluster.InsecureSkipTLSVerify = true
	expectedConfig.Clusters["big-cluster"] = *cluster
	test := configCommandTest{
		args:           []string{"set", "clusters.big-cluster.insecure-skip-tls-verify", "true"},
		startingConfig: newRedFederalCowHammerConfig(),
		expectedConfig: expectedConfig,
	}

	test.run(t)
}

func TestSetIntoNewConfig(t *testing.T) {
	expectedConfig := *clientcmdapi.NewConfig()
	context := clientcmdapi.NewContext()
	context.AuthInfo = "fake-user"
	expectedConfig.Contexts["new-context"] = *context
	test := configCommandTest{
		args:           []string{"set", "contexts.new-context.user", "fake-user"},
		startingConfig: *clientcmdapi.NewConfig(),
		expectedConfig: expectedConfig,
	}

	test.run(t)
}

func TestNewEmptyAuth(t *testing.T) {
	expectedConfig := *clientcmdapi.NewConfig()
	expectedConfig.AuthInfos["the-user-name"] = *clientcmdapi.NewAuthInfo()
	test := configCommandTest{
		args:           []string{"set-credentials", "the-user-name"},
		startingConfig: *clientcmdapi.NewConfig(),
		expectedConfig: expectedConfig,
	}

	test.run(t)
}

func TestAdditionalAuth(t *testing.T) {
	expectedConfig := newRedFederalCowHammerConfig()
	authInfo := clientcmdapi.NewAuthInfo()
	authInfo.AuthPath = "auth-path"
	authInfo.Token = "token"
	expectedConfig.AuthInfos["another-user"] = *authInfo
	test := configCommandTest{
		args:           []string{"set-credentials", "another-user", "--" + clientcmd.FlagAuthPath + "=auth-path", "--" + clientcmd.FlagBearerToken + "=token"},
		startingConfig: newRedFederalCowHammerConfig(),
		expectedConfig: expectedConfig,
	}

	test.run(t)
}

func TestEmbedClientCert(t *testing.T) {
	fakeCertFile, _ := ioutil.TempFile("", "")
	defer os.Remove(fakeCertFile.Name())
	fakeData := []byte("fake-data")
	ioutil.WriteFile(fakeCertFile.Name(), fakeData, 0600)
	expectedConfig := newRedFederalCowHammerConfig()
	authInfo := clientcmdapi.NewAuthInfo()
	authInfo.ClientCertificateData = fakeData
	expectedConfig.AuthInfos["another-user"] = *authInfo

	test := configCommandTest{
		args:           []string{"set-credentials", "another-user", "--" + clientcmd.FlagCertFile + "=" + fakeCertFile.Name(), "--" + clientcmd.FlagEmbedCerts + "=true"},
		startingConfig: newRedFederalCowHammerConfig(),
		expectedConfig: expectedConfig,
	}

	test.run(t)
}

func TestEmbedClientKey(t *testing.T) {
	fakeKeyFile, _ := ioutil.TempFile("", "")
	defer os.Remove(fakeKeyFile.Name())
	fakeData := []byte("fake-data")
	ioutil.WriteFile(fakeKeyFile.Name(), fakeData, 0600)
	expectedConfig := newRedFederalCowHammerConfig()
	authInfo := clientcmdapi.NewAuthInfo()
	authInfo.ClientKeyData = fakeData
	expectedConfig.AuthInfos["another-user"] = *authInfo

	test := configCommandTest{
		args:           []string{"set-credentials", "another-user", "--" + clientcmd.FlagKeyFile + "=" + fakeKeyFile.Name(), "--" + clientcmd.FlagEmbedCerts + "=true"},
		startingConfig: newRedFederalCowHammerConfig(),
		expectedConfig: expectedConfig,
	}

	test.run(t)
}

func TestEmbedNoKeyOrCertDisallowed(t *testing.T) {
	expectedConfig := newRedFederalCowHammerConfig()
	test := configCommandTest{
		args:            []string{"set-credentials", "another-user", "--" + clientcmd.FlagEmbedCerts + "=true"},
		startingConfig:  newRedFederalCowHammerConfig(),
		expectedConfig:  expectedConfig,
		expectedOutputs: []string{"--client-certificate", "--client-key", "embed"},
	}

	test.run(t)
}

func TestEmptyTokenAndCertAllowed(t *testing.T) {
	expectedConfig := newRedFederalCowHammerConfig()
	authInfo := clientcmdapi.NewAuthInfo()
	authInfo.ClientCertificate = "cert-file"
	expectedConfig.AuthInfos["another-user"] = *authInfo

	test := configCommandTest{
		args:           []string{"set-credentials", "another-user", "--" + clientcmd.FlagCertFile + "=cert-file", "--" + clientcmd.FlagBearerToken + "="},
		startingConfig: newRedFederalCowHammerConfig(),
		expectedConfig: expectedConfig,
	}

	test.run(t)
}

func TestTokenAndCertAllowed(t *testing.T) {
	expectedConfig := newRedFederalCowHammerConfig()
	authInfo := clientcmdapi.NewAuthInfo()
	authInfo.Token = "token"
	authInfo.ClientCertificate = "cert-file"
	expectedConfig.AuthInfos["another-user"] = *authInfo
	test := configCommandTest{
		args:           []string{"set-credentials", "another-user", "--" + clientcmd.FlagCertFile + "=cert-file", "--" + clientcmd.FlagBearerToken + "=token"},
		startingConfig: newRedFederalCowHammerConfig(),
		expectedConfig: expectedConfig,
	}

	test.run(t)
}

func TestTokenAndBasicDisallowed(t *testing.T) {
	expectedConfig := newRedFederalCowHammerConfig()
	test := configCommandTest{
		args:            []string{"set-credentials", "another-user", "--" + clientcmd.FlagUsername + "=myuser", "--" + clientcmd.FlagBearerToken + "=token"},
		startingConfig:  newRedFederalCowHammerConfig(),
		expectedConfig:  expectedConfig,
		expectedOutputs: []string{"--token", "--username"},
	}

	test.run(t)
}

func TestBasicClearsToken(t *testing.T) {
	authInfoWithToken := clientcmdapi.NewAuthInfo()
	authInfoWithToken.Token = "token"

	authInfoWithBasic := clientcmdapi.NewAuthInfo()
	authInfoWithBasic.Username = "myuser"
	authInfoWithBasic.Password = "mypass"

	startingConfig := newRedFederalCowHammerConfig()
	startingConfig.AuthInfos["another-user"] = *authInfoWithToken

	expectedConfig := newRedFederalCowHammerConfig()
	expectedConfig.AuthInfos["another-user"] = *authInfoWithBasic

	test := configCommandTest{
		args:           []string{"set-credentials", "another-user", "--" + clientcmd.FlagUsername + "=myuser", "--" + clientcmd.FlagPassword + "=mypass"},
		startingConfig: startingConfig,
		expectedConfig: expectedConfig,
	}

	test.run(t)
}

func TestTokenClearsBasic(t *testing.T) {
	authInfoWithBasic := clientcmdapi.NewAuthInfo()
	authInfoWithBasic.Username = "myuser"
	authInfoWithBasic.Password = "mypass"

	authInfoWithToken := clientcmdapi.NewAuthInfo()
	authInfoWithToken.Token = "token"

	startingConfig := newRedFederalCowHammerConfig()
	startingConfig.AuthInfos["another-user"] = *authInfoWithBasic

	expectedConfig := newRedFederalCowHammerConfig()
	expectedConfig.AuthInfos["another-user"] = *authInfoWithToken

	test := configCommandTest{
		args:           []string{"set-credentials", "another-user", "--" + clientcmd.FlagBearerToken + "=token"},
		startingConfig: startingConfig,
		expectedConfig: expectedConfig,
	}

	test.run(t)
}

func TestTokenLeavesCert(t *testing.T) {
	authInfoWithCerts := clientcmdapi.NewAuthInfo()
	authInfoWithCerts.ClientCertificate = "cert"
	authInfoWithCerts.ClientCertificateData = []byte("certdata")
	authInfoWithCerts.ClientKey = "key"
	authInfoWithCerts.ClientKeyData = []byte("keydata")

	authInfoWithTokenAndCerts := clientcmdapi.NewAuthInfo()
	authInfoWithTokenAndCerts.Token = "token"
	authInfoWithTokenAndCerts.ClientCertificate = "cert"
	authInfoWithTokenAndCerts.ClientCertificateData = []byte("certdata")
	authInfoWithTokenAndCerts.ClientKey = "key"
	authInfoWithTokenAndCerts.ClientKeyData = []byte("keydata")

	startingConfig := newRedFederalCowHammerConfig()
	startingConfig.AuthInfos["another-user"] = *authInfoWithCerts

	expectedConfig := newRedFederalCowHammerConfig()
	expectedConfig.AuthInfos["another-user"] = *authInfoWithTokenAndCerts

	test := configCommandTest{
		args:           []string{"set-credentials", "another-user", "--" + clientcmd.FlagBearerToken + "=token"},
		startingConfig: startingConfig,
		expectedConfig: expectedConfig,
	}

	test.run(t)
}

func TestCertLeavesToken(t *testing.T) {
	authInfoWithToken := clientcmdapi.NewAuthInfo()
	authInfoWithToken.Token = "token"

	authInfoWithTokenAndCerts := clientcmdapi.NewAuthInfo()
	authInfoWithTokenAndCerts.Token = "token"
	authInfoWithTokenAndCerts.ClientCertificate = "cert"
	authInfoWithTokenAndCerts.ClientKey = "key"

	startingConfig := newRedFederalCowHammerConfig()
	startingConfig.AuthInfos["another-user"] = *authInfoWithToken

	expectedConfig := newRedFederalCowHammerConfig()
	expectedConfig.AuthInfos["another-user"] = *authInfoWithTokenAndCerts

	test := configCommandTest{
		args:           []string{"set-credentials", "another-user", "--" + clientcmd.FlagCertFile + "=cert", "--" + clientcmd.FlagKeyFile + "=key"},
		startingConfig: startingConfig,
		expectedConfig: expectedConfig,
	}

	test.run(t)
}

func TestCAClearsInsecure(t *testing.T) {
	clusterInfoWithInsecure := clientcmdapi.NewCluster()
	clusterInfoWithInsecure.InsecureSkipTLSVerify = true

	clusterInfoWithCA := clientcmdapi.NewCluster()
	clusterInfoWithCA.CertificateAuthority = "cafile"

	startingConfig := newRedFederalCowHammerConfig()
	startingConfig.Clusters["another-cluster"] = *clusterInfoWithInsecure

	expectedConfig := newRedFederalCowHammerConfig()
	expectedConfig.Clusters["another-cluster"] = *clusterInfoWithCA

	test := configCommandTest{
		args:           []string{"set-cluster", "another-cluster", "--" + clientcmd.FlagCAFile + "=cafile"},
		startingConfig: startingConfig,
		expectedConfig: expectedConfig,
	}

	test.run(t)
}

func TestCAClearsCAData(t *testing.T) {
	clusterInfoWithCAData := clientcmdapi.NewCluster()
	clusterInfoWithCAData.CertificateAuthorityData = []byte("cadata")

	clusterInfoWithCA := clientcmdapi.NewCluster()
	clusterInfoWithCA.CertificateAuthority = "cafile"

	startingConfig := newRedFederalCowHammerConfig()
	startingConfig.Clusters["another-cluster"] = *clusterInfoWithCAData

	expectedConfig := newRedFederalCowHammerConfig()
	expectedConfig.Clusters["another-cluster"] = *clusterInfoWithCA

	test := configCommandTest{
		args:           []string{"set-cluster", "another-cluster", "--" + clientcmd.FlagCAFile + "=cafile", "--" + clientcmd.FlagInsecure + "=false"},
		startingConfig: startingConfig,
		expectedConfig: expectedConfig,
	}

	test.run(t)
}

func TestInsecureClearsCA(t *testing.T) {
	clusterInfoWithInsecure := clientcmdapi.NewCluster()
	clusterInfoWithInsecure.InsecureSkipTLSVerify = true

	clusterInfoWithCA := clientcmdapi.NewCluster()
	clusterInfoWithCA.CertificateAuthority = "cafile"
	clusterInfoWithCA.CertificateAuthorityData = []byte("cadata")

	startingConfig := newRedFederalCowHammerConfig()
	startingConfig.Clusters["another-cluster"] = *clusterInfoWithCA

	expectedConfig := newRedFederalCowHammerConfig()
	expectedConfig.Clusters["another-cluster"] = *clusterInfoWithInsecure

	test := configCommandTest{
		args:           []string{"set-cluster", "another-cluster", "--" + clientcmd.FlagInsecure + "=true"},
		startingConfig: startingConfig,
		expectedConfig: expectedConfig,
	}

	test.run(t)
}

func TestCADataClearsCA(t *testing.T) {
	fakeCAFile, _ := ioutil.TempFile("", "")
	defer os.Remove(fakeCAFile.Name())
	fakeData := []byte("cadata")
	ioutil.WriteFile(fakeCAFile.Name(), fakeData, 0600)

	clusterInfoWithCAData := clientcmdapi.NewCluster()
	clusterInfoWithCAData.CertificateAuthorityData = fakeData

	clusterInfoWithCA := clientcmdapi.NewCluster()
	clusterInfoWithCA.CertificateAuthority = "cafile"

	startingConfig := newRedFederalCowHammerConfig()
	startingConfig.Clusters["another-cluster"] = *clusterInfoWithCA

	expectedConfig := newRedFederalCowHammerConfig()
	expectedConfig.Clusters["another-cluster"] = *clusterInfoWithCAData

	test := configCommandTest{
		args:           []string{"set-cluster", "another-cluster", "--" + clientcmd.FlagCAFile + "=" + fakeCAFile.Name(), "--" + clientcmd.FlagEmbedCerts + "=true"},
		startingConfig: startingConfig,
		expectedConfig: expectedConfig,
	}

	test.run(t)
}

func TestEmbedNoCADisallowed(t *testing.T) {
	expectedConfig := newRedFederalCowHammerConfig()
	test := configCommandTest{
		args:            []string{"set-cluster", "another-cluster", "--" + clientcmd.FlagEmbedCerts + "=true"},
		startingConfig:  newRedFederalCowHammerConfig(),
		expectedConfig:  expectedConfig,
		expectedOutputs: []string{"--certificate-authority", "embed"},
	}

	test.run(t)
}

func TestCAAndInsecureDisallowed(t *testing.T) {
	test := configCommandTest{
		args:            []string{"set-cluster", "another-cluster", "--" + clientcmd.FlagCAFile + "=cafile", "--" + clientcmd.FlagInsecure + "=true"},
		startingConfig:  newRedFederalCowHammerConfig(),
		expectedConfig:  newRedFederalCowHammerConfig(),
		expectedOutputs: []string{"certificate", "insecure"},
	}

	test.run(t)
}

func TestMergeExistingAuth(t *testing.T) {
	expectedConfig := newRedFederalCowHammerConfig()
	authInfo := expectedConfig.AuthInfos["red-user"]
	authInfo.AuthPath = "auth-path"
	expectedConfig.AuthInfos["red-user"] = authInfo
	test := configCommandTest{
		args:           []string{"set-credentials", "red-user", "--" + clientcmd.FlagAuthPath + "=auth-path"},
		startingConfig: newRedFederalCowHammerConfig(),
		expectedConfig: expectedConfig,
	}

	test.run(t)
}

func TestNewEmptyCluster(t *testing.T) {
	expectedConfig := *clientcmdapi.NewConfig()
	expectedConfig.Clusters["new-cluster"] = *clientcmdapi.NewCluster()
	test := configCommandTest{
		args:           []string{"set-cluster", "new-cluster"},
		startingConfig: *clientcmdapi.NewConfig(),
		expectedConfig: expectedConfig,
	}

	test.run(t)
}

func TestAdditionalCluster(t *testing.T) {
	expectedConfig := newRedFederalCowHammerConfig()
	cluster := *clientcmdapi.NewCluster()
	cluster.APIVersion = "v1beta1"
	cluster.CertificateAuthority = "ca-location"
	cluster.InsecureSkipTLSVerify = false
	cluster.Server = "serverlocation"
	expectedConfig.Clusters["different-cluster"] = cluster
	test := configCommandTest{
		args:           []string{"set-cluster", "different-cluster", "--" + clientcmd.FlagAPIServer + "=serverlocation", "--" + clientcmd.FlagInsecure + "=false", "--" + clientcmd.FlagCAFile + "=ca-location", "--" + clientcmd.FlagAPIVersion + "=v1beta1"},
		startingConfig: newRedFederalCowHammerConfig(),
		expectedConfig: expectedConfig,
	}

	test.run(t)
}

func TestOverwriteExistingCluster(t *testing.T) {
	expectedConfig := newRedFederalCowHammerConfig()
	cluster := *clientcmdapi.NewCluster()
	cluster.Server = "serverlocation"
	expectedConfig.Clusters["cow-cluster"] = cluster

	test := configCommandTest{
		args:           []string{"set-cluster", "cow-cluster", "--" + clientcmd.FlagAPIServer + "=serverlocation"},
		startingConfig: newRedFederalCowHammerConfig(),
		expectedConfig: expectedConfig,
	}

	test.run(t)
}

func TestNewEmptyContext(t *testing.T) {
	expectedConfig := *clientcmdapi.NewConfig()
	expectedConfig.Contexts["new-context"] = *clientcmdapi.NewContext()
	test := configCommandTest{
		args:           []string{"set-context", "new-context"},
		startingConfig: *clientcmdapi.NewConfig(),
		expectedConfig: expectedConfig,
	}

	test.run(t)
}

func TestAdditionalContext(t *testing.T) {
	expectedConfig := newRedFederalCowHammerConfig()
	context := *clientcmdapi.NewContext()
	context.Cluster = "some-cluster"
	context.AuthInfo = "some-user"
	context.Namespace = "different-namespace"
	expectedConfig.Contexts["different-context"] = context
	test := configCommandTest{
		args:           []string{"set-context", "different-context", "--" + clientcmd.FlagClusterName + "=some-cluster", "--" + clientcmd.FlagAuthInfoName + "=some-user", "--" + clientcmd.FlagNamespace + "=different-namespace"},
		startingConfig: newRedFederalCowHammerConfig(),
		expectedConfig: expectedConfig,
	}

	test.run(t)
}

func TestMergeExistingContext(t *testing.T) {
	expectedConfig := newRedFederalCowHammerConfig()
	context := expectedConfig.Contexts["federal-context"]
	context.Namespace = "hammer"
	expectedConfig.Contexts["federal-context"] = context

	test := configCommandTest{
		args:           []string{"set-context", "federal-context", "--" + clientcmd.FlagNamespace + "=hammer"},
		startingConfig: newRedFederalCowHammerConfig(),
		expectedConfig: expectedConfig,
	}

	test.run(t)
}

func TestToBool(t *testing.T) {
	type test struct {
		in  string
		out bool
		err string
	}

	tests := []test{
		{"", false, ""},
		{"true", true, ""},
		{"on", false, `strconv.ParseBool: parsing "on": invalid syntax`},
	}

	for _, curr := range tests {
		b, err := toBool(curr.in)
		if (len(curr.err) != 0) && err == nil {
			t.Errorf("Expected error: %v, but got nil", curr.err)
		}
		if (len(curr.err) == 0) && err != nil {
			t.Errorf("Unexpected error: %v", err)
		}
		if (err != nil) && (err.Error() != curr.err) {
			t.Errorf("Expected %v, got %v", curr.err, err)

		}
		if b != curr.out {
			t.Errorf("Expected %v, got %v", curr.out, b)
		}
	}

}

func testConfigCommand(args []string, startingConfig clientcmdapi.Config) (string, clientcmdapi.Config) {
	fakeKubeFile, _ := ioutil.TempFile("", "")
	defer os.Remove(fakeKubeFile.Name())
	clientcmd.WriteToFile(startingConfig, fakeKubeFile.Name())

	argsToUse := make([]string, 0, 2+len(args))
	argsToUse = append(argsToUse, "--kubeconfig="+fakeKubeFile.Name())
	argsToUse = append(argsToUse, args...)

	buf := bytes.NewBuffer([]byte{})

	cmd := NewCmdConfig(buf)
	cmd.SetArgs(argsToUse)
	cmd.Execute()

	// outBytes, _ := ioutil.ReadFile(fakeKubeFile.Name())
	config := getConfigFromFileOrDie(fakeKubeFile.Name())

	return buf.String(), *config
}

func (test configCommandTest) run(t *testing.T) {
	out, actualConfig := testConfigCommand(test.args, test.startingConfig)

	testSetNilMapsToEmpties(reflect.ValueOf(&test.expectedConfig))
	testSetNilMapsToEmpties(reflect.ValueOf(&actualConfig))

	if !reflect.DeepEqual(test.expectedConfig, actualConfig) {
		t.Errorf("diff: %v", util.ObjectDiff(test.expectedConfig, actualConfig))
		t.Errorf("expected: %#v\n actual:   %#v", test.expectedConfig, actualConfig)
	}

	for _, expectedOutput := range test.expectedOutputs {
		if !strings.Contains(out, expectedOutput) {
			t.Errorf("expected '%s' in output, got '%s'", expectedOutput, out)
		}
	}
}
func testSetNilMapsToEmpties(curr reflect.Value) {
	actualCurrValue := curr
	if curr.Kind() == reflect.Ptr {
		actualCurrValue = curr.Elem()
	}

	switch actualCurrValue.Kind() {
	case reflect.Map:
		for _, mapKey := range actualCurrValue.MapKeys() {
			currMapValue := actualCurrValue.MapIndex(mapKey)

			// our maps do not hold pointers to structs, they hold the structs themselves.  This means that MapIndex returns the struct itself
			// That in turn means that they have kinds of type.Struct, which is not a settable type.  Because of this, we need to make new struct of that type
			// copy all the data from the old value into the new value, then take the .addr of the new value to modify it in the next recursion.
			// clear as mud
			modifiableMapValue := reflect.New(currMapValue.Type()).Elem()
			modifiableMapValue.Set(currMapValue)

			if modifiableMapValue.Kind() == reflect.Struct {
				modifiableMapValue = modifiableMapValue.Addr()
			}

			testSetNilMapsToEmpties(modifiableMapValue)
			actualCurrValue.SetMapIndex(mapKey, reflect.Indirect(modifiableMapValue))
		}

	case reflect.Struct:
		for fieldIndex := 0; fieldIndex < actualCurrValue.NumField(); fieldIndex++ {
			currFieldValue := actualCurrValue.Field(fieldIndex)

			if currFieldValue.Kind() == reflect.Map && currFieldValue.IsNil() {
				newValue := reflect.MakeMap(currFieldValue.Type())
				currFieldValue.Set(newValue)
			} else {
				testSetNilMapsToEmpties(currFieldValue.Addr())
			}
		}

	}

}
