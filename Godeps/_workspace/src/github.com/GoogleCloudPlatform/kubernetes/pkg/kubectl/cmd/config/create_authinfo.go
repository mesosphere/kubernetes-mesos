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
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"strings"

	"github.com/spf13/cobra"

	"github.com/GoogleCloudPlatform/kubernetes/pkg/client/clientcmd"
	clientcmdapi "github.com/GoogleCloudPlatform/kubernetes/pkg/client/clientcmd/api"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/util"
)

type createAuthInfoOptions struct {
	pathOptions       *pathOptions
	name              string
	authPath          util.StringFlag
	clientCertificate util.StringFlag
	clientKey         util.StringFlag
	token             util.StringFlag
	username          util.StringFlag
	password          util.StringFlag
	embedCertData     util.BoolFlag
}

var create_authinfo_long = fmt.Sprintf(`Sets a user entry in .kubeconfig
Specifying a name that already exists will merge new fields on top of existing values.

  Client-certificate flags:
    --%v=certfile --%v=keyfile

  Bearer token flags:
    --%v=bearer_token

  Basic auth flags:
    --%v=basic_user --%v=basic_password

  Bearer token and basic auth are mutually exclusive.
`, clientcmd.FlagCertFile, clientcmd.FlagKeyFile, clientcmd.FlagBearerToken, clientcmd.FlagUsername, clientcmd.FlagPassword)

const create_authinfo_example = `// Set only the "client-key" field on the "cluster-admin"
// entry, without touching other values:
$ kubectl set-credentials cluster-admin --client-key=~/.kube/admin.key

// Set basic auth for the "cluster-admin" entry
$ kubectl set-credentials cluster-admin --username=admin --password=uXFGweU9l35qcif

// Embed client certificate data in the "cluster-admin" entry
$ kubectl set-credentials cluster-admin --client-certificate=~/.kube/admin.crt --embed-certs=true`

func NewCmdConfigSetAuthInfo(out io.Writer, pathOptions *pathOptions) *cobra.Command {
	options := &createAuthInfoOptions{pathOptions: pathOptions}

	cmd := &cobra.Command{
		Use:     fmt.Sprintf("set-credentials NAME [--%v=/path/to/authfile] [--%v=path/to/certfile] [--%v=path/to/keyfile] [--%v=bearer_token] [--%v=basic_user] [--%v=basic_password]", clientcmd.FlagAuthPath, clientcmd.FlagCertFile, clientcmd.FlagKeyFile, clientcmd.FlagBearerToken, clientcmd.FlagUsername, clientcmd.FlagPassword),
		Short:   "Sets a user entry in .kubeconfig",
		Long:    create_authinfo_long,
		Example: create_authinfo_example,
		Run: func(cmd *cobra.Command, args []string) {
			if !options.complete(cmd) {
				return
			}

			err := options.run()
			if err != nil {
				fmt.Fprintf(out, "%v\n", err)
			}
		},
	}

	cmd.Flags().Var(&options.authPath, clientcmd.FlagAuthPath, clientcmd.FlagAuthPath+" for the user entry in .kubeconfig")
	cmd.Flags().Var(&options.clientCertificate, clientcmd.FlagCertFile, "path to "+clientcmd.FlagCertFile+" for the user entry in .kubeconfig")
	cmd.Flags().Var(&options.clientKey, clientcmd.FlagKeyFile, "path to "+clientcmd.FlagKeyFile+" for the user entry in .kubeconfig")
	cmd.Flags().Var(&options.token, clientcmd.FlagBearerToken, clientcmd.FlagBearerToken+" for the user entry in .kubeconfig")
	cmd.Flags().Var(&options.username, clientcmd.FlagUsername, clientcmd.FlagUsername+" for the user entry in .kubeconfig")
	cmd.Flags().Var(&options.password, clientcmd.FlagPassword, clientcmd.FlagPassword+" for the user entry in .kubeconfig")
	cmd.Flags().Var(&options.embedCertData, clientcmd.FlagEmbedCerts, "embed client cert/key for the user entry in .kubeconfig")

	return cmd
}

func (o createAuthInfoOptions) run() error {
	err := o.validate()
	if err != nil {
		return err
	}

	config, filename, err := o.pathOptions.getStartingConfig()
	if err != nil {
		return err
	}

	authInfo := o.modifyAuthInfo(config.AuthInfos[o.name])
	config.AuthInfos[o.name] = authInfo

	err = clientcmd.WriteToFile(*config, filename)
	if err != nil {
		return err
	}

	return nil
}

// authInfo builds an AuthInfo object from the options
func (o *createAuthInfoOptions) modifyAuthInfo(existingAuthInfo clientcmdapi.AuthInfo) clientcmdapi.AuthInfo {
	modifiedAuthInfo := existingAuthInfo

	var setToken, setBasic bool

	if o.authPath.Provided() {
		modifiedAuthInfo.AuthPath = o.authPath.Value()
	}

	if o.clientCertificate.Provided() {
		certPath := o.clientCertificate.Value()
		if o.embedCertData.Value() {
			modifiedAuthInfo.ClientCertificateData, _ = ioutil.ReadFile(certPath)
			modifiedAuthInfo.ClientCertificate = ""
		} else {
			modifiedAuthInfo.ClientCertificate = certPath
			if len(modifiedAuthInfo.ClientCertificate) > 0 {
				modifiedAuthInfo.ClientCertificateData = nil
			}
		}
	}
	if o.clientKey.Provided() {
		keyPath := o.clientKey.Value()
		if o.embedCertData.Value() {
			modifiedAuthInfo.ClientKeyData, _ = ioutil.ReadFile(keyPath)
			modifiedAuthInfo.ClientKey = ""
		} else {
			modifiedAuthInfo.ClientKey = keyPath
			if len(modifiedAuthInfo.ClientKey) > 0 {
				modifiedAuthInfo.ClientKeyData = nil
			}
		}
	}

	if o.token.Provided() {
		modifiedAuthInfo.Token = o.token.Value()
		setToken = len(modifiedAuthInfo.Token) > 0
	}

	if o.username.Provided() {
		modifiedAuthInfo.Username = o.username.Value()
		setBasic = setBasic || len(modifiedAuthInfo.Username) > 0
	}
	if o.password.Provided() {
		modifiedAuthInfo.Password = o.password.Value()
		setBasic = setBasic || len(modifiedAuthInfo.Password) > 0
	}

	// If any auth info was set, make sure any other existing auth types are cleared
	if setToken || setBasic {
		if !setToken {
			modifiedAuthInfo.Token = ""
		}
		if !setBasic {
			modifiedAuthInfo.Username = ""
			modifiedAuthInfo.Password = ""
		}
	}

	return modifiedAuthInfo
}

func (o *createAuthInfoOptions) complete(cmd *cobra.Command) bool {
	args := cmd.Flags().Args()
	if len(args) != 1 {
		cmd.Help()
		return false
	}

	o.name = args[0]
	return true
}

func (o createAuthInfoOptions) validate() error {
	if len(o.name) == 0 {
		return errors.New("You must specify a non-empty user name")
	}
	methods := []string{}
	if len(o.token.Value()) > 0 {
		methods = append(methods, fmt.Sprintf("--%v", clientcmd.FlagBearerToken))
	}
	if len(o.username.Value()) > 0 || len(o.password.Value()) > 0 {
		methods = append(methods, fmt.Sprintf("--%v/--%v", clientcmd.FlagUsername, clientcmd.FlagPassword))
	}
	if len(methods) > 1 {
		return fmt.Errorf("You cannot specify more than one authentication method at the same time: %v", strings.Join(methods, ", "))
	}
	if o.embedCertData.Value() {
		certPath := o.clientCertificate.Value()
		keyPath := o.clientKey.Value()
		if certPath == "" && keyPath == "" {
			return fmt.Errorf("You must specify a --%s or --%s to embed", clientcmd.FlagCertFile, clientcmd.FlagKeyFile)
		}
		if certPath != "" {
			if _, err := ioutil.ReadFile(certPath); err != nil {
				return fmt.Errorf("Error reading %s data from %s: %v", clientcmd.FlagCertFile, certPath, err)
			}
		}
		if keyPath != "" {
			if _, err := ioutil.ReadFile(keyPath); err != nil {
				return fmt.Errorf("Error reading %s data from %s: %v", clientcmd.FlagKeyFile, keyPath, err)
			}
		}
	}

	return nil
}
