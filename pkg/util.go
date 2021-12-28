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
	"io/ioutil"
	"os"
	"path/filepath"
	"time"

	stash "stash.appscode.dev/apimachinery/client/clientset/versioned"
	"stash.appscode.dev/apimachinery/pkg/restic"

	shell "gomodules.xyz/go-sh"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"
	kmapi "kmodules.xyz/client-go/api/v1"
	appcatalog "kmodules.xyz/custom-resources/apis/appcatalog/v1alpha1"
	appcatalog_cs "kmodules.xyz/custom-resources/client/clientset/versioned"
)

const (
	NATSUser        = "username"
	NATSCreds       = "creds"
	NATSPassword    = "password"
	NATSToken       = "token"
	NATSNkey        = "nkey"
	NATSCMD         = "nats"
	NATSCert        = "tls.crt"
	NATSKey         = "tls.key"
	NATSStreamsFile = "streams.json"
	NATSCredsFile   = "user.creds"
	NATSCACertFile  = "ca.crt"
	NATSNkeyFile    = "user.nk"
	NATSCertFile    = "tls.crt"
	NATSKeyFile     = "tls.key"
	EnvNATSUrl      = "NATS_URL"
	EnvNATSUser     = "NATS_USER"
	EnvNATSPassword = "NATS_PASSWORD"
	EnvNATSCreds    = "NATS_CREDS"
	EnvNATSCA       = "NATS_CA"
	EnvNATSNkey     = "NATS_NKEY"
	EnvNATSCert     = "NATS_CERT"
	EnvNATSKey      = "NATS_KEY"
)

type natsOptions struct {
	kubeClient    kubernetes.Interface
	stashClient   stash.Interface
	catalogClient appcatalog_cs.Interface

	namespace         string
	backupSessionName string
	interimDataDir    string
	streams           []string
	overwrite         bool
	appBindingName    string
	natsArgs          string
	waitTimeout       int32
	warnThreshold     string
	outputDir         string
	storageSecret     kmapi.ObjectReference
	setupOptions      restic.SetupOptions
	backupOptions     restic.BackupOptions
	restoreOptions    restic.RestoreOptions
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

func (opt *natsOptions) waitForNATSReady(appBinding *appcatalog.AppBinding) error {
	klog.Infoln("Waiting for the nats server to be ready.....")
	sh := NewSessionWrapper()

	host, err := appBinding.Host()
	if err != nil {
		return err
	}
	args := []interface{}{
		"server",
		"check",
		"connection",
		"--connect-warn", opt.warnThreshold,
	}

	opt.setServerUrl(sh, host)
	err = opt.setTLS(sh, appBinding)
	if err != nil {
		return err
	}

	// set access credentials
	err = opt.setCredentials(sh, appBinding)
	if err != nil {
		return err
	}

	return wait.PollImmediate(time.Second*5, time.Minute*5, func() (bool, error) {
		err := sh.Command(NATSCMD, args...).Run()
		if err != nil {
			return false, nil
		}
		return true, nil
	})
}

func (opt *natsOptions) setServerUrl(sh Shell, host string) {
	sh.SetEnv(EnvNATSUrl, host)
}

func (opt *natsOptions) setTLS(sh Shell, appBinding *appcatalog.AppBinding) error {
	if appBinding.Spec.ClientConfig.CABundle == nil {
		return nil
	}

	if err := ioutil.WriteFile(filepath.Join(opt.setupOptions.ScratchDir, NATSCACertFile), appBinding.Spec.ClientConfig.CABundle, os.ModePerm); err != nil {
		return err
	}
	sh.SetEnv(EnvNATSCA, filepath.Join(opt.setupOptions.ScratchDir, NATSCACertFile))
	return nil
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
	// Token authentication
	if token, ok := secret.Data[NATSToken]; ok {
		sh.SetEnv(EnvNATSUser, string(token))
	}

	// Basic authentication
	user, userExist := secret.Data[NATSUser]
	password, passwordExist := secret.Data[NATSPassword]

	if userExist && passwordExist {
		sh.SetEnv(EnvNATSUser, string(user))
		sh.SetEnv(EnvNATSPassword, string(password))
	}

	//Nkey Authentication
	if nkey, ok := secret.Data[NATSNkey]; ok {
		if err := ioutil.WriteFile(filepath.Join(opt.setupOptions.ScratchDir, NATSNkeyFile), nkey, os.ModePerm); err != nil {
			return err
		}
		sh.SetEnv(EnvNATSNkey, filepath.Join(opt.setupOptions.ScratchDir, NATSNkeyFile))
	}

	// TLS Authentication
	cert, certExist := secret.Data[NATSCert]
	key, keyExist := secret.Data[NATSKey]

	if certExist && keyExist {
		if err := ioutil.WriteFile(filepath.Join(opt.setupOptions.ScratchDir, NATSCertFile), cert, os.ModePerm); err != nil {
			return err
		}
		sh.SetEnv(EnvNATSCert, filepath.Join(opt.setupOptions.ScratchDir, NATSCertFile))
		if err := ioutil.WriteFile(filepath.Join(opt.setupOptions.ScratchDir, NATSKeyFile), key, os.ModePerm); err != nil {
			return err
		}
		sh.SetEnv(EnvNATSKey, filepath.Join(opt.setupOptions.ScratchDir, NATSKeyFile))
	}
	//JWT Authentication
	if creds, ok := secret.Data[NATSCreds]; ok {
		if err := ioutil.WriteFile(filepath.Join(opt.setupOptions.ScratchDir, NATSCredsFile), creds, os.ModePerm); err != nil {
			return err
		}
		sh.SetEnv(EnvNATSCreds, filepath.Join(opt.setupOptions.ScratchDir, NATSCredsFile))
	}

	return nil
}
