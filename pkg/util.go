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
	"path/filepath"
	"strings"
	"time"

	stash "stash.appscode.dev/apimachinery/client/clientset/versioned"
	"stash.appscode.dev/apimachinery/pkg/restic"

	shell "gomodules.xyz/go-sh"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	restclient "k8s.io/client-go/rest"
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

	namespace           string
	backupSessionName   string
	interimDataDir      string
	streams             []string
	overwrite           bool
	appBindingName      string
	appBindingNamespace string
	natsArgs            string
	waitTimeout         int32
	warningThreshold    string
	outputDir           string
	storageSecret       kmapi.ObjectReference
	setupOptions        restic.SetupOptions
	backupOptions       restic.BackupOptions
	restoreOptions      restic.RestoreOptions
	config              *restclient.Config
}

type sessionWrapper struct {
	sh  *shell.Session
	cmd *restic.Command
}

func (opt *natsOptions) newSessionWrapper(cmd string) *sessionWrapper {
	return &sessionWrapper{
		sh: shell.NewSession(),
		cmd: &restic.Command{
			Name: cmd,
		},
	}
}

func (opt *natsOptions) setNATSCredentials(sh *shell.Session, appBinding *appcatalog.AppBinding) error {
	// if credential secret is not provided in AppBinding, then nothing to do.
	if appBinding.Spec.Secret == nil {
		return nil
	}

	appBindingSecret, err := opt.kubeClient.CoreV1().Secrets(appBinding.Namespace).Get(context.TODO(), appBinding.Spec.Secret.Name, metav1.GetOptions{})
	if err != nil {
		return err
	}

	err = appBinding.TransformSecret(opt.kubeClient, appBindingSecret.Data)
	if err != nil {
		return err
	}

	// Token authentication
	if token, ok := appBindingSecret.Data[NATSToken]; ok {
		sh.SetEnv(EnvNATSUser, string(token))
	}

	// Basic authentication
	user, userExist := appBindingSecret.Data[NATSUser]
	password, passwordExist := appBindingSecret.Data[NATSPassword]

	if userExist && passwordExist {
		sh.SetEnv(EnvNATSUser, string(user))
		sh.SetEnv(EnvNATSPassword, string(password))
	}

	// Nkey Authentication
	if nkey, ok := appBindingSecret.Data[NATSNkey]; ok {
		if err := os.WriteFile(filepath.Join(opt.setupOptions.ScratchDir, NATSNkeyFile), nkey, os.ModePerm); err != nil {
			return err
		}
		sh.SetEnv(EnvNATSNkey, filepath.Join(opt.setupOptions.ScratchDir, NATSNkeyFile))
	}

	// TLS Authentication
	cert, certExist := appBindingSecret.Data[NATSCert]
	key, keyExist := appBindingSecret.Data[NATSKey]

	if certExist && keyExist {
		if err := os.WriteFile(filepath.Join(opt.setupOptions.ScratchDir, NATSCertFile), cert, os.ModePerm); err != nil {
			return err
		}
		sh.SetEnv(EnvNATSCert, filepath.Join(opt.setupOptions.ScratchDir, NATSCertFile))
		if err := os.WriteFile(filepath.Join(opt.setupOptions.ScratchDir, NATSKeyFile), key, os.ModePerm); err != nil {
			return err
		}
		sh.SetEnv(EnvNATSKey, filepath.Join(opt.setupOptions.ScratchDir, NATSKeyFile))
	}

	// JWT Authentication
	if creds, ok := appBindingSecret.Data[NATSCreds]; ok {
		if err := os.WriteFile(filepath.Join(opt.setupOptions.ScratchDir, NATSCredsFile), creds, os.ModePerm); err != nil {
			return err
		}
		sh.SetEnv(EnvNATSCreds, filepath.Join(opt.setupOptions.ScratchDir, NATSCredsFile))
	}
	return nil
}

func (session *sessionWrapper) setTLSParameters(appBinding *appcatalog.AppBinding, scratchDir string) error {
	if appBinding.Spec.ClientConfig.CABundle == nil {
		return nil
	}

	if err := os.WriteFile(filepath.Join(scratchDir, NATSCACertFile), appBinding.Spec.ClientConfig.CABundle, os.ModePerm); err != nil {
		return err
	}

	session.sh.SetEnv(EnvNATSCA, filepath.Join(scratchDir, NATSCACertFile))
	return nil
}

func (session *sessionWrapper) setNATSConnectionParameters(appBinding *appcatalog.AppBinding) error {
	host, err := appBinding.Host()
	if err != nil {
		return err
	}
	session.sh.SetEnv(EnvNATSUrl, host)
	return nil
}

func (session *sessionWrapper) setUserArgs(args string) {
	for _, arg := range strings.Fields(args) {
		session.cmd.Args = append(session.cmd.Args, arg)
	}
}

func clearDir(dir string) error {
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("unable to clean datadir: %v. Reason: %v", dir, err)
	}
	return os.MkdirAll(dir, os.ModePerm)
}

func (session sessionWrapper) waitForNATSReady(warningThreshold string) error {
	klog.Infoln("Waiting for the nats server to be ready...")

	args := []interface{}{
		"server",
		"check",
		"connection",
		"--connect-warn", warningThreshold,
	}

	args = append(session.cmd.Args, args...)

	return wait.PollImmediate(time.Second*5, time.Minute*5, func() (bool, error) {
		err := session.sh.Command(NATSCMD, args...).Run()
		if err != nil {
			return false, nil
		}
		return true, nil
	})
}
