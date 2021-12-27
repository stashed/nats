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
	"encoding/json"
	"io/ioutil"
	"path/filepath"
	"strings"

	api_v1beta1 "stash.appscode.dev/apimachinery/apis/stash/v1beta1"
	"stash.appscode.dev/apimachinery/pkg/restic"

	"github.com/spf13/cobra"
	license "go.bytebuilders.dev/license-verifier/kubernetes"
	"gomodules.xyz/flags"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog/v2"
	appcatalog "kmodules.xyz/custom-resources/apis/appcatalog/v1alpha1"
	appcatalog_cs "kmodules.xyz/custom-resources/client/clientset/versioned"
	v1 "kmodules.xyz/offshoot-api/api/v1"
)

func NewCmdRestore() *cobra.Command {
	var (
		masterURL      string
		kubeconfigPath string
		opt            = natsOptions{
			setupOptions: restic.SetupOptions{
				ScratchDir:  restic.DefaultScratchDir,
				EnableCache: false,
			},
			waitTimeout: 300,
			restoreOptions: restic.RestoreOptions{
				Host: restic.DefaultHost,
			},
		}
	)

	cmd := &cobra.Command{
		Use:               "restore-nats",
		Short:             "Restores NATS DB Backup",
		DisableAutoGenTag: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			flags.EnsureRequiredFlags(cmd, "appbinding", "provider")

			// prepare client
			config, err := clientcmd.BuildConfigFromFlags(masterURL, kubeconfigPath)
			if err != nil {
				return err
			}
			err = license.CheckLicenseEndpoint(config, licenseApiService, SupportedProducts)
			if err != nil {
				return err
			}
			opt.kubeClient, err = kubernetes.NewForConfig(config)
			if err != nil {
				return err
			}
			opt.catalogClient, err = appcatalog_cs.NewForConfig(config)
			if err != nil {
				return err
			}

			targetRef := api_v1beta1.TargetRef{
				APIVersion: appcatalog.SchemeGroupVersion.String(),
				Kind:       appcatalog.ResourceKindApp,
				Name:       opt.appBindingName,
			}

			var restoreOutput *restic.RestoreOutput
			restoreOutput, err = opt.restoreNATS(targetRef)
			if err != nil {
				restoreOutput = &restic.RestoreOutput{
					RestoreTargetStatus: api_v1beta1.RestoreMemberStatus{
						Ref: targetRef,
						Stats: []api_v1beta1.HostRestoreStats{
							{
								Hostname: opt.restoreOptions.Host,
								Phase:    api_v1beta1.HostRestoreFailed,
								Error:    err.Error(),
							},
						},
					},
				}
			}
			// If output directory specified, then write the output in "output.json" file in the specified directory
			if opt.outputDir != "" {
				return restoreOutput.WriteOutput(filepath.Join(opt.outputDir, restic.DefaultOutputFileName))
			}

			return nil
		},
	}

	cmd.Flags().StringVar(&opt.natsArgs, "nats-args", opt.natsArgs, "Additional arguments")
	cmd.Flags().Int32Var(&opt.waitTimeout, "wait-timeout", opt.waitTimeout, "Time limit to wait for the database to be ready")

	cmd.Flags().StringVar(&masterURL, "master", masterURL, "The address of the Kubernetes API server (overrides any value in kubeconfig)")
	cmd.Flags().StringVar(&kubeconfigPath, "kubeconfig", kubeconfigPath, "Path to kubeconfig file with authorization information (the master location is set by the master flag).")
	cmd.Flags().StringVar(&opt.namespace, "namespace", "default", "Namespace of Backup/Restore Session")
	cmd.Flags().StringVar(&opt.appBindingName, "appbinding", opt.appBindingName, "Name of the app binding")
	cmd.Flags().StringVar(&opt.storageInfo.name, "secret-name", opt.storageInfo.name, "Name of the storage secret")
	cmd.Flags().StringVar(&opt.storageInfo.namespace, "secret-namespace", opt.storageInfo.namespace, "Namespace of the storage secret")

	cmd.Flags().StringVar(&opt.setupOptions.Provider, "provider", opt.setupOptions.Provider, "Backend provider (i.e. gcs, s3, azure etc)")
	cmd.Flags().StringVar(&opt.setupOptions.Bucket, "bucket", opt.setupOptions.Bucket, "Name of the cloud bucket/container (keep empty for local backend)")
	cmd.Flags().StringVar(&opt.setupOptions.Endpoint, "endpoint", opt.setupOptions.Endpoint, "Endpoint for s3/s3 compatible backend or REST backend URL")
	cmd.Flags().StringVar(&opt.setupOptions.Region, "region", opt.setupOptions.Region, "Region for s3/s3 compatible backend")
	cmd.Flags().StringVar(&opt.setupOptions.Path, "path", opt.setupOptions.Path, "Directory inside the bucket where backup will be stored")
	cmd.Flags().StringVar(&opt.setupOptions.ScratchDir, "scratch-dir", opt.setupOptions.ScratchDir, "Temporary directory")
	cmd.Flags().BoolVar(&opt.setupOptions.EnableCache, "enable-cache", opt.setupOptions.EnableCache, "Specify whether to enable caching for restic")
	cmd.Flags().Int64Var(&opt.setupOptions.MaxConnections, "max-connections", opt.setupOptions.MaxConnections, "Specify maximum concurrent connections for GCS, Azure and B2 backend")

	cmd.Flags().StringVar(&opt.restoreOptions.Host, "hostname", opt.restoreOptions.Host, "Name of the host machine")
	cmd.Flags().StringVar(&opt.restoreOptions.SourceHost, "source-hostname", opt.restoreOptions.SourceHost, "Name of the host from where data will be restored")
	cmd.Flags().StringSliceVar(&opt.restoreOptions.Snapshots, "snapshot", opt.restoreOptions.Snapshots, "Snapshot to restore")

	cmd.Flags().StringVar(&opt.interimDataDir, "interim-data-dir", opt.interimDataDir, "Directory where the restored data will be stored temporarily before injecting into the desired NATS Server")
	cmd.Flags().StringVar(&opt.outputDir, "output-dir", opt.outputDir, "Directory where output.json file will be written (keep empty if you don't need to write output in file)")
	cmd.Flags().StringSliceVar(&opt.streams, "streams", opt.streams, "List of streams to restore. Keep empty to restore all the backed up streams")
	cmd.Flags().BoolVar(&opt.overwrite, "overwrite", opt.overwrite, "Specify whether to delete a stream before restoring if it already exist")
	return cmd
}

func (opt *natsOptions) restoreNATS(targetRef api_v1beta1.TargetRef) (*restic.RestoreOutput, error) {
	// get storage secret
	var err error
	opt.setupOptions.StorageSecret, err = opt.kubeClient.CoreV1().Secrets(opt.storageInfo.namespace).Get(context.TODO(), opt.storageInfo.name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	// apply nice, ionice settings from env
	opt.setupOptions.Nice, err = v1.NiceSettingsFromEnv()
	if err != nil {
		return nil, err
	}
	opt.setupOptions.IONice, err = v1.IONiceSettingsFromEnv()
	if err != nil {
		return nil, err
	}

	// get app binding
	appBinding, err := opt.catalogClient.AppcatalogV1alpha1().AppBindings(opt.namespace).Get(context.TODO(), opt.appBindingName, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	// clear directory
	klog.Infoln("Cleaning up temporary data directory: ", opt.interimDataDir)
	if err := clearDir(opt.interimDataDir); err != nil {
		return nil, err
	}

	// wait for NATS server to be ready
	err = opt.waitForNATSReady(appBinding)
	if err != nil {
		return nil, err
	}

	// we will restore the desired data into interim data dir before restoring the streams
	opt.restoreOptions.RestorePaths = []string{opt.interimDataDir}

	// init restic wrapper
	resticWrapper, err := restic.NewResticWrapper(opt.setupOptions)
	if err != nil {
		return nil, err
	}
	// Run restore
	restoreOutput, err := resticWrapper.RunRestore(opt.restoreOptions, targetRef)
	if err != nil {
		return nil, err
	}

	// run separate shell to perform restore
	restoreShell := NewSessionWrapper()
	restoreShell.ShowCMD = true
	// set access credentials
	err = opt.setCredentials(restoreShell, appBinding)
	if err != nil {
		return nil, err
	}
	// set TLS
	err = opt.setTLS(restoreShell, appBinding)
	if err != nil {
		return nil, err
	}

	host, err := appBinding.Host()
	if err != nil {
		return nil, err
	}

	restoreArgs := []interface{}{
		"stream",
		"restore",
		"--server", host,
	}
	for _, arg := range strings.Fields(opt.natsArgs) {
		restoreArgs = append(restoreArgs, arg)
	}

	var streams []string
	if len(opt.streams) != 0 {
		streams = opt.streams
	} else {
		byteStreams, err := ioutil.ReadFile(filepath.Join(opt.interimDataDir, NATSStreamsFile))
		if err != nil {
			return nil, err
		}
		err = json.Unmarshal(byteStreams, &streams)
		if err != nil {
			return nil, err
		}
	}
	if opt.overwrite {
		err := removeMatchedStreams(restoreShell, host, streams)
		if err != nil {
			return nil, err
		}
	}
	for i := range streams {
		args := append(restoreArgs, streams[i], filepath.Join(opt.interimDataDir, streams[i]))
		restoreShell.Command(NATSCMD, args...)
		if err := restoreShell.Run(); err != nil {
			return nil, err
		}
	}

	return restoreOutput, nil
}

func removeMatchedStreams(sh *SessionWrapper, host string, streams []string) error {
	lsArgs := []interface{}{
		"stream",
		"ls",
		"--json",
		"--server", host,
	}
	byteStreams, err := sh.Command(NATSCMD, lsArgs...).Output()
	if err != nil {
		return err
	}
	var currStreams []string
	if err := json.Unmarshal(byteStreams, &currStreams); err != nil {
		return err
	}
	rmArgs := []interface{}{
		"stream",
		"rm",
		"--server", host,
		"-f",
	}
	for i := range streams {
		if streamExists(streams[i], currStreams) {
			args := append(rmArgs, streams[i])
			sh.Command(NATSCMD, args...)
			if err := sh.Run(); err != nil {
				return err
			}
		}
	}
	return nil
}

func streamExists(s1 string, list []string) bool {
	for _, s2 := range list {
		if s2 == s1 {
			return true
		}
	}
	return false
}
