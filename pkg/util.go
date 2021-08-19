/*
Copyright AppsCode Inc. and Contributors

Licensed under the AppsCode Free Trial License 1.0.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    https://github.com/appscode/licenses/raw/1.0.0/AppsCode-Free-Trial-1.0.0.md

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package pkg

import (
	"context"
	"fmt"
	"os"
	"time"

	stash "stash.appscode.dev/apimachinery/client/clientset/versioned"
	"stash.appscode.dev/apimachinery/pkg/restic"

	shell "gomodules.xyz/go-sh"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"
	appcatalog "kmodules.xyz/custom-resources/apis/appcatalog/v1alpha1"
	appcatalog_cs "kmodules.xyz/custom-resources/client/clientset/versioned"
)

const (
	NATSUser        = "username"
	NATSPassword    = "password"
	NATSCMD         = "nats"
	NATSStreamsFile = "streams.json"
	EnvNATSUser     = "NATS_USER"
	EnvNATSPassword = "NATS_PASSWORD"
)

type natsOptions struct {
	kubeClient    kubernetes.Interface
	stashClient   stash.Interface
	catalogClient appcatalog_cs.Interface

	namespace         string
	backupSessionName string
	interimDataDir    string
	streams           string
	appBindingName    string
	natsArgs          string
	waitTimeout       int32
	outputDir         string

	setupOptions   restic.SetupOptions
	backupOptions  restic.BackupOptions
	restoreOptions restic.RestoreOptions
}

type Shell interface {
	SetEnv(key, value string)
}

type SessionWrapper struct {
	*shell.Session
}

func NewSessionWrapper() *SessionWrapper {
	return &SessionWrapper{
		shell.NewSession(),
	}
}
func (wrapper *SessionWrapper) SetEnv(key, value string) {
	wrapper.Session.SetEnv(key, value)
}

func clearDir(dir string) error {
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("unable to clean datadir: %v. Reason: %v", dir, err)
	}
	return os.MkdirAll(dir, os.ModePerm)
}

func (opt *natsOptions) waitForDBReady(appBinding *appcatalog.AppBinding) error {
	klog.Infoln("Waiting for the nats server to be ready.....")
	sh := NewSessionWrapper()
	args := []interface{}{
		"server",
		"check",
		"connection",
		"--server", appBinding.Spec.ClientConfig.Service.Name,
	}

	// set access credentials
	err := opt.setCredentials(sh, appBinding)
	if err != nil {
		return err
	}

	return wait.PollImmediate(time.Second*5, time.Minute*5, func() (bool, error) {
		err := sh.Command("nats", args...).Run()
		if err != nil {
			return false, nil
		}
		return true, nil
	})
}

func (opt *natsOptions) setCredentials(sh Shell, appBinding *appcatalog.AppBinding) error {
	// if credential secret is not provided in AppBinding, then nothing to do.
	if appBinding.Spec.Secret == nil {
		return nil
	}

	// get the Secret
	secret, err := opt.kubeClient.CoreV1().Secrets(opt.namespace).Get(context.TODO(), appBinding.Spec.Secret.Name, metav1.GetOptions{})
	if err != nil {
		return err
	}

	// perform necessary transform if secretTransforms section is provided in the AppBinding
	err = appBinding.TransformSecret(opt.kubeClient, secret.Data)
	if err != nil {
		return err
	}

	// set auth env for nats
	sh.SetEnv(EnvNATSUser, string(secret.Data[NATSUser]))
	sh.SetEnv(EnvNATSPassword, string(secret.Data[NATSPassword]))

	return nil
}
