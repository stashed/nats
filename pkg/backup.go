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

	api_v1beta1 "stash.appscode.dev/apimachinery/apis/stash/v1beta1"
	stash "stash.appscode.dev/apimachinery/client/clientset/versioned"
	"stash.appscode.dev/apimachinery/pkg/restic"
	api_util "stash.appscode.dev/apimachinery/pkg/util"

	"github.com/spf13/cobra"
	license "go.bytebuilders.dev/license-verifier/kubernetes"
	"gomodules.xyz/flags"
	shell "gomodules.xyz/go-sh"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog/v2"
	appcatalog "kmodules.xyz/custom-resources/apis/appcatalog/v1alpha1"
	appcatalog_cs "kmodules.xyz/custom-resources/client/clientset/versioned"
	v1 "kmodules.xyz/offshoot-api/api/v1"
)

func NewCmdBackup() *cobra.Command {
	var (
		masterURL      string
		kubeconfigPath string
		opt            = natsOptions{
			waitTimeout:      300,
			warningThreshold: "30s",
			setupOptions: restic.SetupOptions{
				ScratchDir:  restic.DefaultScratchDir,
				EnableCache: false,
			},
			backupOptions: restic.BackupOptions{
				Host: restic.DefaultHost,
			},
		}
	)

	cmd := &cobra.Command{
		Use:               "backup-nats",
		Short:             "Takes a backup of NATS streams",
		DisableAutoGenTag: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			flags.EnsureRequiredFlags(cmd, "appbinding", "provider", "storage-secret-name", "storage-secret-namespace")

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
			opt.stashClient, err = stash.NewForConfig(config)
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
				Namespace:  opt.appBindingNamespace,
			}
			var backupOutput *restic.BackupOutput
			backupOutput, err = opt.backupNATS(targetRef)
			if err != nil {
				backupOutput = &restic.BackupOutput{
					BackupTargetStatus: api_v1beta1.BackupTargetStatus{
						Ref: targetRef,
						Stats: []api_v1beta1.HostBackupStats{
							{
								Hostname: opt.backupOptions.Host,
								Phase:    api_v1beta1.HostBackupFailed,
								Error:    err.Error(),
							},
						},
					},
				}
			}
			// If output directory specified, then write the output in "output.json" file in the specified directory
			if opt.outputDir != "" {
				return backupOutput.WriteOutput(filepath.Join(opt.outputDir, restic.DefaultOutputFileName))
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&opt.natsArgs, "nats-args", opt.natsArgs, "Additional arguments")
	cmd.Flags().Int32Var(&opt.waitTimeout, "wait-timeout", opt.waitTimeout, "Time limit to wait for the database to be ready")
	cmd.Flags().StringVar(&opt.warningThreshold, "warning-threshold", opt.warningThreshold, "Warning threshold to allow for establishing connections")

	cmd.Flags().StringVar(&masterURL, "master", masterURL, "The address of the Kubernetes API server (overrides any value in kubeconfig)")
	cmd.Flags().StringVar(&kubeconfigPath, "kubeconfig", kubeconfigPath, "Path to kubeconfig file with authorization information (the master location is set by the master flag)")
	cmd.Flags().StringVar(&opt.namespace, "namespace", "default", "Namespace of Backup/Restore Session")
	cmd.Flags().StringVar(&opt.backupSessionName, "backupsession", opt.backupSessionName, "Name of the Backup Session")
	cmd.Flags().StringVar(&opt.appBindingName, "appbinding", opt.appBindingName, "Name of the app binding")
	cmd.Flags().StringVar(&opt.appBindingNamespace, "appbinding-namespace", opt.appBindingNamespace, "Namespace of the app binding")
	cmd.Flags().StringVar(&opt.storageSecret.Name, "storage-secret-name", opt.storageSecret.Name, "Name of the storage secret")
	cmd.Flags().StringVar(&opt.storageSecret.Namespace, "storage-secret-namespace", opt.storageSecret.Namespace, "Namespace of the storage secret")

	cmd.Flags().StringVar(&opt.setupOptions.Provider, "provider", opt.setupOptions.Provider, "Backend provider (i.e. gcs, s3, azure etc)")
	cmd.Flags().StringVar(&opt.setupOptions.Bucket, "bucket", opt.setupOptions.Bucket, "Name of the cloud bucket/container (keep empty for local backend)")
	cmd.Flags().StringVar(&opt.setupOptions.Endpoint, "endpoint", opt.setupOptions.Endpoint, "Endpoint for s3/s3 compatible backend or REST backend URL")
	cmd.Flags().StringVar(&opt.setupOptions.Region, "region", opt.setupOptions.Region, "Region for s3/s3 compatible backend")
	cmd.Flags().StringVar(&opt.setupOptions.Path, "path", opt.setupOptions.Path, "Directory inside the bucket where backup will be stored")
	cmd.Flags().StringVar(&opt.setupOptions.ScratchDir, "scratch-dir", opt.setupOptions.ScratchDir, "Temporary directory")
	cmd.Flags().BoolVar(&opt.setupOptions.EnableCache, "enable-cache", opt.setupOptions.EnableCache, "Specify whether to enable caching for restic")
	cmd.Flags().Int64Var(&opt.setupOptions.MaxConnections, "max-connections", opt.setupOptions.MaxConnections, "Specify maximum concurrent connections for GCS, Azure and B2 backend")

	cmd.Flags().StringVar(&opt.backupOptions.Host, "hostname", opt.backupOptions.Host, "Name of the host machine")
	cmd.Flags().Int64Var(&opt.backupOptions.RetentionPolicy.KeepLast, "retention-keep-last", opt.backupOptions.RetentionPolicy.KeepLast, "Specify value for retention strategy")
	cmd.Flags().Int64Var(&opt.backupOptions.RetentionPolicy.KeepHourly, "retention-keep-hourly", opt.backupOptions.RetentionPolicy.KeepHourly, "Specify value for retention strategy")
	cmd.Flags().Int64Var(&opt.backupOptions.RetentionPolicy.KeepDaily, "retention-keep-daily", opt.backupOptions.RetentionPolicy.KeepDaily, "Specify value for retention strategy")
	cmd.Flags().Int64Var(&opt.backupOptions.RetentionPolicy.KeepWeekly, "retention-keep-weekly", opt.backupOptions.RetentionPolicy.KeepWeekly, "Specify value for retention strategy")
	cmd.Flags().Int64Var(&opt.backupOptions.RetentionPolicy.KeepMonthly, "retention-keep-monthly", opt.backupOptions.RetentionPolicy.KeepMonthly, "Specify value for retention strategy")
	cmd.Flags().Int64Var(&opt.backupOptions.RetentionPolicy.KeepYearly, "retention-keep-yearly", opt.backupOptions.RetentionPolicy.KeepYearly, "Specify value for retention strategy")
	cmd.Flags().StringSliceVar(&opt.backupOptions.RetentionPolicy.KeepTags, "retention-keep-tags", opt.backupOptions.RetentionPolicy.KeepTags, "Specify value for retention strategy")
	cmd.Flags().BoolVar(&opt.backupOptions.RetentionPolicy.Prune, "retention-prune", opt.backupOptions.RetentionPolicy.Prune, "Specify whether to prune old snapshot data")
	cmd.Flags().BoolVar(&opt.backupOptions.RetentionPolicy.DryRun, "retention-dry-run", opt.backupOptions.RetentionPolicy.DryRun, "Specify whether to test retention policy without deleting actual data")

	cmd.Flags().StringVar(&opt.interimDataDir, "interim-data-dir", opt.interimDataDir, "Directory where the targeted data will be stored temporarily before uploading to the backend")
	cmd.Flags().StringVar(&opt.outputDir, "output-dir", opt.outputDir, "Directory where output.json file will be written (keep empty if you don't need to write output in file)")
	cmd.Flags().StringSliceVar(&opt.streams, "streams", opt.streams, "List of streams to backup. Keep empty to backup all streams")
	return cmd
}

func (opt *natsOptions) backupNATS(targetRef api_v1beta1.TargetRef) (*restic.BackupOutput, error) {
	var err error
	opt.setupOptions.StorageSecret, err = opt.kubeClient.CoreV1().Secrets(opt.storageSecret.Namespace).Get(context.TODO(), opt.storageSecret.Name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	// if any pre-backup actions has been assigned to it, execute them
	actionOptions := api_util.ActionOptions{
		StashClient:       opt.stashClient,
		TargetRef:         targetRef,
		SetupOptions:      opt.setupOptions,
		BackupSessionName: opt.backupSessionName,
		Namespace:         opt.namespace,
	}

	err = api_util.ExecutePreBackupActions(actionOptions)
	if err != nil {
		return nil, err
	}

	// wait until the backend repository has been initialized.
	err = api_util.WaitForBackendRepository(actionOptions)
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

	appBinding, err := opt.catalogClient.AppcatalogV1alpha1().AppBindings(opt.appBindingNamespace).Get(context.TODO(), opt.appBindingName, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	klog.Infoln("Cleaning up temporary data directory directory: ", opt.interimDataDir)
	if err := clearDir(opt.interimDataDir); err != nil {
		return nil, err
	}

	session := opt.newSessionWrapper(NATSCMD)

	err = opt.setNATSCredentials(session.sh, appBinding)
	if err != nil {
		return nil, err
	}

	err = session.setNATSConnectionParameters(appBinding)
	if err != nil {
		return nil, err
	}

	err = session.setTLSParameters(appBinding, opt.setupOptions.ScratchDir)
	if err != nil {
		return nil, err
	}

	err = session.waitForNATSReady(opt.warningThreshold)
	if err != nil {
		return nil, err
	}

	session.cmd.Args = append(session.cmd.Args, "stream", "backup")
	session.setUserArgs(opt.natsArgs)

	streams, err := opt.getStreams(session.sh)
	if err != nil {
		return nil, err
	}

	for i := range streams {
		args := append(session.cmd.Args, streams[i], filepath.Join(opt.interimDataDir, streams[i]))
		session.sh.Command(NATSCMD, args...)
		if err := session.sh.Run(); err != nil {
			return nil, err
		}
	}

	// data snapshot has been stored in the interim data dir. Now, we will backup this directory using Stash.
	opt.backupOptions.BackupPaths = []string{opt.interimDataDir}

	resticWrapper, err := restic.NewResticWrapper(opt.setupOptions)
	if err != nil {
		return nil, err
	}

	return resticWrapper.RunBackup(opt.backupOptions, targetRef)
}

func (opt *natsOptions) getStreams(sh *shell.Session) ([]string, error) {
	if len(opt.streams) == 0 {
		args := []interface{}{
			"stream",
			"ls",
			"--json",
		}

		sh.Command(NATSCMD, args...)

		err := sh.WriteStdout(filepath.Join(opt.interimDataDir, NATSStreamsFile))
		if err != nil {
			return nil, err
		}

		byteStreams, err := ioutil.ReadFile(filepath.Join(opt.interimDataDir, NATSStreamsFile))
		if err != nil {
			return nil, err
		}

		var streams []string
		err = json.Unmarshal(byteStreams, &streams)
		if err != nil {
			return nil, err
		}

		return streams, nil

	} else {
		byteStreams, err := json.Marshal(opt.streams)
		if err != nil {
			return nil, err
		}

		err = ioutil.WriteFile(filepath.Join(opt.interimDataDir, NATSStreamsFile), byteStreams, 0o644)
		if err != nil {
			return nil, err
		}

		return opt.streams, nil
	}
}
