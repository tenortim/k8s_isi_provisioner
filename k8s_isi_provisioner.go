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
	"os"
	"path"
	"strings"
	"time"

	"syscall"

	isi "github.com/tenortim/goisilon"

	"github.com/golang/glog"
	"github.com/kubernetes-sigs/sig-storage-lib-external-provisioner/controller"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/klog"
)

const (
	provisionerName = "isilon.com/isilon"
	serverEnvVar    = "ISI_SERVER"
)

type isilonProvisioner struct {
	// Identity of this isilonProvisioner, set to node's name. Used to identify
	// "this" provisioner's PVs.
	identity string

	isiClient *isi.Client
	// The directory to create the new volume in, as well as the
	// username, password and server to connect to
	volumeDir string
	// The access zone in which to create new exports
	accessZone string
	// useName    string
	serverName string
	// apply/enfoce quotas to volumes
	quotaEnable bool
}

var _ controller.Provisioner = &isilonProvisioner{}
var version = "Version not set"

// Provision creates a storage asset and returns a PV object representing it.
func (p *isilonProvisioner) Provision(options controller.VolumeOptions) (*v1.PersistentVolume, error) {
	pvcNamespace := options.PVC.Namespace
	pvcName := options.PVC.Name
	capacity := options.PVC.Spec.Resources.Requests[v1.ResourceName(v1.ResourceStorage)]
	pvcSize := capacity.Value()

	glog.Infof("Got namespace: %s, name: %s, pvName: %s, size: %v", pvcNamespace, pvcName, options.PVName, pvcSize)

	// Create a unique volume name based on the namespace requesting the pv
	pvName := strings.Join([]string{pvcNamespace, pvcName, options.PVName}, "-")
	path := path.Join(p.volumeDir, pvName)

	// Create the mount point directory (k8s volume == isi directory)
	rcVolume, err := p.isiClient.CreateVolume(context.Background(), pvName)
	glog.Infof("Created volume mount point directory: %s", rcVolume)
	if err != nil {
		return nil, err
	}

	// if quotas are enabled, we need to set a quota on the volume
	if p.quotaEnable {
		// need to set the quota based on the requested pv size
		// if a size isnt requested, and quotas are enabled we should fail
		if pvcSize <= 0 {
			return nil, errors.New("No storage size requested and quotas enabled")
		}
		// create quota with container set to true
		err := p.isiClient.CreateQuota(context.Background(), pvName, true, pvcSize)
		if err != nil {
			glog.Infof("Quota set to: %v on directory: %s", pvcSize, pvName)
		}
	}
	glog.Infof("Creating Isilon export '%s' in zone %s", pvName, p.accessZone)
	rcExport, err := p.isiClient.ExportVolumeWithZone(context.Background(), pvName, p.accessZone)
	if err != nil {
		return nil, err
	}
	glog.Infof("Created Isilon export id: %v", rcExport)

	if err := os.MkdirAll(path, 0777); err != nil {
		return nil, err
	}

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
					Server:   p.serverName,
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
	if p.quotaEnable {
		quota, _ := p.isiClient.GetQuota(context.Background(), isiVolume)
		if quota != nil {
			if err := p.isiClient.ClearQuota(context.Background(), isiVolume); err != nil {
				return fmt.Errorf("failed to remove quota from %v: %v", isiVolume, err)
			}
		}
	}

	// if we get here we can destroy the volume
	if err := p.isiClient.Unexport(context.Background(), isiVolume); err != nil {
		return fmt.Errorf("failed to unexport volume directory %v: %v", isiVolume, err)
	}

	// if we get here we can destroy the volume
	if err := p.isiClient.DeleteVolume(context.Background(), isiVolume); err != nil {
		return fmt.Errorf("failed to delete volume directory %v: %v", isiVolume, err)
	}

	return nil
}

func main() {
	syscall.Umask(0)

	flag.Parse()
	flag.Set("logtostderr", "true")

	// The leaderelection code called by the provisioner uses klog instead of glog,
	// and fails to correctly initialize the logging to stderr. Fix that here.
	klogFlags := flag.NewFlagSet("klog", flag.ExitOnError)
	klog.InitFlags(klogFlags)
	// Sync the glog and klog flags.
	flag.CommandLine.VisitAll(func(f1 *flag.Flag) {
		f2 := klogFlags.Lookup(f1.Name)
		if f2 != nil {
			value := f1.Value.String()
			f2.Value.Set(value)
		}
	})

	glog.Info("Starting Isilon Dynamic Provisioner version: " + version)
	// Create an InClusterConfig and use it to create a client for the controller
	// to use to communicate with Kubernetes
	config, err := rest.InClusterConfig()
	if err != nil {
		glog.Fatalf("Failed to create config: %v", err)
	}
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		glog.Fatalf("Failed to create client: %v", err)
	}

	// The controller needs to know what the server version is because out-of-tree
	// provisioners aren't officially supported until 1.5
	serverVersion, err := clientset.Discovery().ServerVersion()
	if err != nil {
		glog.Fatalf("Error getting server version: %v", err)
	}

	// Get server name and NFS root path from environment
	isiServer := os.Getenv("ISI_SERVER")
	if isiServer == "" {
		glog.Fatal("ISI_SERVER not set")
	}
	isiAPIServer := os.Getenv("ISI_API_SERVER")
	if isiServer == "" {
		glog.Info("No API server variable, reverting to ISI_SERVER")
		isiAPIServer = isiServer
	}
	isiPath := os.Getenv("ISI_PATH")
	if isiPath == "" {
		glog.Fatal("ISI_PATH not set")
	}
	isiZone := os.Getenv("ISI_ZONE")
	if isiZone == "" {
		glog.Info("No access zone variable, defaulting to System")
		isiZone = "System"
	}
	isiUser := os.Getenv("ISI_USER")
	if isiUser == "" {
		glog.Fatal("ISI_USER not set")
	}
	isiPass := os.Getenv("ISI_PASS")
	if isiPass == "" {
		glog.Fatal("ISI_PASS not set")
	}
	isiGroup := os.Getenv("ISI_GROUP")
	if isiPass == "" {
		glog.Fatal("ISI_GROUP not set")
	}

	// set isiquota to false by default
	isiQuota := false
	isiQuotaEnable := strings.ToUpper(os.Getenv("ISI_QUOTA_ENABLE"))

	if isiQuotaEnable == "TRUE" {
		glog.Info("Isilon quotas enabled")
		isiQuota = true
	} else {
		glog.Info("ISI_QUOTA_ENABLED not set.  Quota support disabled")
	}

	isiEndpoint := "https://" + isiAPIServer + ":8080"
	glog.Info("Connecting to Isilon at: " + isiEndpoint)
	glog.Info("Creating exports at: " + isiPath)

	i, err := isi.NewClientWithArgs(
		context.Background(),
		isiEndpoint,
		true,
		isiUser,
		isiGroup,
		isiPass,
		isiPath,
	)
	if err != nil {
		glog.Fatalf("Unable to connect to isilon API: %v", err)
	}

	glog.Info("Successfully connected to: " + isiEndpoint)

	// Create the provisioner: it implements the Provisioner interface expected by
	// the controller
	isilonProvisioner := &isilonProvisioner{
		identity:    isiServer,
		isiClient:   i,
		volumeDir:   isiPath,
		accessZone:  isiZone,
		serverName:  isiServer,
		quotaEnable: isiQuota,
	}

	// Start the provision controller which will dynamically provision isilon
	// PVs
	pc := controller.NewProvisionController(clientset, provisionerName, isilonProvisioner, serverVersion.GitVersion, controller.ExponentialBackOffOnError(false), controller.FailedProvisionThreshold(5), controller.ResyncPeriod(15*time.Second))
	pc.Run(wait.NeverStop)
}
