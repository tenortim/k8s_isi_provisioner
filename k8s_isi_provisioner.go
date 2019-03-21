/*
Copyright 2019 Tim Wright.
Copyright 2017 Mark DeNeve.

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

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"path"
	"strings"
	"time"

	"syscall"

	isi "github.com/tenortim/goisilon"

	"github.com/kubernetes-sigs/sig-storage-lib-external-provisioner/controller"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/klog"
	volutil "k8s.io/kubernetes/pkg/volume/util"
)

const (
	provisionerName = "isilon.com/isilon"
	onefsPluginName = "isilon.com/isilon"
	secretKeyName   = "password"
)

type provisionerConfig struct {
	server      string
	apiServer   string
	volumeDir   string
	apiuser     string
	password    string
	group       string
	quotaEnable bool
	accessZone  string
}

type isilonProvisioner struct {
	// Identity of this isilonProvisioner Used to identify
	// "this" provisioner's PVs.
	// since we only need one provisioner instance, this may be unnecessary
	identity  string
	k8sClient *kubernetes.Clientset
}

var _ controller.Provisioner = &isilonProvisioner{}
var version = "Version not set"

// Provision creates a storage asset and returns a PV object representing it.
func (p *isilonProvisioner) Provision(options controller.VolumeOptions) (*v1.PersistentVolume, error) {
	// parse storageclass Parameters
	config, err := parseClassParameters(options.Parameters, p.k8sClient)
	if err != nil {
		return nil, err
	}
	// connect to the cluster
	isiClient, err := getIsiClient(config)
	if err != nil {
		return nil, err
	}

	pvcNamespace := options.PVC.Namespace
	pvcName := options.PVC.Name
	capacity := options.PVC.Spec.Resources.Requests[v1.ResourceName(v1.ResourceStorage)]
	pvcSize := capacity.Value()

	klog.Infof("Got namespace: %s, name: %s, pvName: %s, size: %v", pvcNamespace, pvcName, options.PVName, pvcSize)

	// Create a unique volume name based on the namespace requesting the pv
	pvName := strings.Join([]string{pvcNamespace, pvcName, options.PVName}, "-")
	path := path.Join(config.volumeDir, pvName)

	// Create the mount point directory (k8s volume == isi directory)
	rcVolume, err := isiClient.CreateVolumeNoACL(context.Background(), pvName)
	if err != nil {
		return nil, err
	}
	klog.Infof("Created volume mount point directory: %s", rcVolume)

	err = isiClient.SetVolumeMode(context.Background(), pvName, 0777)
	if err != nil {
		return nil, err
	}
	klog.Infof("Set permissions on volume %s to mode 0777", pvName)

	// if quotas are enabled, we need to set a quota on the volume
	if config.quotaEnable {
		// need to set the quota based on the requested pv size
		// if a size isnt requested, and quotas are enabled we should fail
		if pvcSize <= 0 {
			return nil, errors.New("No storage size requested and quotas enabled")
		}
		// create quota with container set to true
		err := isiClient.CreateQuota(context.Background(), pvName, true, pvcSize)
		if err != nil {
			klog.Infof("Quota set to: %v on directory: %s", pvcSize, pvName)
		}
	}
	klog.Infof("Creating Isilon export '%s' in zone %s", pvName, config.accessZone)
	rcExport, err := isiClient.ExportVolumeWithZone(context.Background(), pvName, config.accessZone)
	if err != nil {
		return nil, err
	}
	klog.Infof("Created Isilon export id: %v", rcExport)

	pv := &v1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{
			Name: options.PVName,
			Annotations: map[string]string{
				"isilonProvisionerIdentity": p.identity,
				"isilonVolume":              pvName,
			},
		},
		Spec: v1.PersistentVolumeSpec{
			PersistentVolumeReclaimPolicy: options.PersistentVolumeReclaimPolicy,
			AccessModes:                   options.PVC.Spec.AccessModes,
			Capacity: v1.ResourceList{
				v1.ResourceName(v1.ResourceStorage): options.PVC.Spec.Resources.Requests[v1.ResourceName(v1.ResourceStorage)],
			},
			MountOptions: options.MountOptions,
			PersistentVolumeSource: v1.PersistentVolumeSource{
				NFS: &v1.NFSVolumeSource{
					Server:   config.server,
					Path:     path,
					ReadOnly: false,
				},
			},
		},
	}

	return pv, nil
}

// Delete removes the storage asset that was created by Provision represented
// by the given PV.
func (p *isilonProvisioner) Delete(volume *v1.PersistentVolume) error {
	// Get details of StorageClass.
	class, err := volutil.GetClassForVolume(p.k8sClient, volume)

	// parse storageclass Parameters
	config, err := parseClassParameters(class.Parameters, p.k8sClient)
	if err != nil {
		return err
	}
	// connect to the cluster
	isiClient, err := getIsiClient(config)
	if err != nil {
		return err
	}

	ann, ok := volume.Annotations["isilonProvisionerIdentity"]
	if !ok {
		return errors.New("identity annotation not found on PV")
	}
	if ann != p.identity {
		return &controller.IgnoredError{Reason: "identity annotation on PV does not match ours"}
	}
	isiVolume, ok := volume.Annotations["isilonVolume"]
	if !ok {
		return &controller.IgnoredError{Reason: "No isilon volume defined"}
	}
	// Remove quota if enabled
	if config.quotaEnable {
		quota, _ := isiClient.GetQuota(context.Background(), isiVolume)
		if quota != nil {
			if err := isiClient.ClearQuota(context.Background(), isiVolume); err != nil {
				return fmt.Errorf("failed to remove quota from %v: %v", isiVolume, err)
			}
		}
	}

	// if we get here we can destroy the volume
	if err := isiClient.UnexportWithZone(context.Background(), isiVolume, config.accessZone); err != nil {
		return fmt.Errorf("failed to unexport volume directory %v: %v", isiVolume, err)
	}

	// if we get here we can destroy the volume
	if err := isiClient.DeleteVolume(context.Background(), isiVolume); err != nil {
		return fmt.Errorf("failed to delete volume directory %v: %v", isiVolume, err)
	}

	return nil
}

func parseClassParameters(params map[string]string, kubeClient *kubernetes.Clientset) (*provisionerConfig, error) {
	var cfg provisionerConfig
	server, ok := params["server"]
	if !ok {
		return nil, fmt.Errorf("storageclass is missing required parameter 'server'")
	}
	cfg.server = server
	cfg.apiServer = server
	if apiServer, ok := params["apiserver"]; ok {
		cfg.apiServer = apiServer
	}
	volumeDir, ok := params["basepath"]
	if !ok {
		return nil, fmt.Errorf("storageclass is missing required parameter 'basepath'")
	}
	cfg.volumeDir = volumeDir
	apiuser, ok := params["apiuser"]
	if !ok {
		return nil, fmt.Errorf("storageclass is missing required parameter 'apiuser'")
	}
	cfg.apiuser = apiuser
	group, ok := params["group"]
	if !ok {
		return nil, fmt.Errorf("storageclass is missing required parameter 'group'")
	}
	cfg.group = group
	cfg.quotaEnable = false
	if quotaEnable, ok := params["quotas"]; ok {
		if quotaEnable == "true" {
			cfg.quotaEnable = true
		}
	}
	cfg.accessZone = "System"
	if accessZone, ok := params["zone"]; ok {
		cfg.accessZone = accessZone
	}

	// obtain password from secret store
	secretName, ok := params["secretName"]
	if !ok {
		return nil, fmt.Errorf("storageclass is missing required parameter 'secretName'")
	}
	secretNamespace, ok := params["secretNamespace"]
	if !ok {
		return nil, fmt.Errorf("storageclass is missing required parameter 'secretNamespace'")
	}
	var err error
	cfg.password, err = parseSecret(secretNamespace, secretName, kubeClient)
	if err != nil {
		return nil, err
	}

	return &cfg, nil
}

func getIsiClient(config *provisionerConfig) (*isi.Client, error) {
	isiEndpoint := "https://" + config.apiServer + ":8080"
	klog.Info("Connecting to Isilon at: " + isiEndpoint)

	i, err := isi.NewClientWithArgs(
		context.Background(),
		isiEndpoint,
		true,
		config.apiuser,
		config.group,
		config.password,
		config.volumeDir,
	)
	if err != nil {
		return nil, fmt.Errorf("Unable to connect to isilon API: %v", err)
	}
	return i, nil
}

// parseSecret finds a given Secret instance and reads apiuser password from it.
func parseSecret(namespace, secretName string, kubeClient *kubernetes.Clientset) (string, error) {
	secretMap, err := volutil.GetSecretForPV(namespace, secretName, onefsPluginName, kubeClient)
	if err != nil {
		klog.Errorf("failed to get secret: %s/%s: %v", namespace, secretName, err)
		return "", fmt.Errorf("failed to get secret %s/%s: %v", namespace, secretName, err)
	}
	if len(secretMap) == 0 {
		return "", fmt.Errorf("empty secret map")
	}
	for k, v := range secretMap {
		if k == secretKeyName {
			return v, nil
		}
	}

	return "", fmt.Errorf("secret key %q not found in namespace %q", secretKeyName, namespace)
}

func main() {
	syscall.Umask(0)

	flag.Parse()
	flag.Set("logtostderr", "true")

	// Initialize klog
	klogFlags := flag.NewFlagSet("klog", flag.ExitOnError)
	klog.InitFlags(klogFlags)

	klog.Info("Starting Isilon Dynamic Provisioner version: " + version)
	// Create an InClusterConfig and use it to create a client for the controller
	// to use to communicate with Kubernetes
	config, err := rest.InClusterConfig()
	if err != nil {
		klog.Fatalf("Failed to create config: %v", err)
	}
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		klog.Fatalf("Failed to create client: %v", err)
	}

	// The controller needs to know what the server version is because out-of-tree
	// provisioners aren't officially supported until 1.5
	serverVersion, err := clientset.Discovery().ServerVersion()
	if err != nil {
		klog.Fatalf("Error getting server version: %v", err)
	}

	// Create the provisioner: it implements the Provisioner interface expected by
	// the controller
	isilonProvisioner := &isilonProvisioner{
		identity:  provisionerName,
		k8sClient: clientset,
	}

	// Start the provision controller which will dynamically provision isilon
	// PVs
	klog.Infof("registering provisioner under name %q", provisionerName)
	pc := controller.NewProvisionController(clientset, provisionerName, isilonProvisioner, serverVersion.GitVersion, controller.ExponentialBackOffOnError(false), controller.FailedProvisionThreshold(5), controller.ResyncPeriod(15*time.Second))
	pc.Run(wait.NeverStop)
}
