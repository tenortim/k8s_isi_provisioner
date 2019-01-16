/*
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
	"github.com/kubernetes-incubator/external-storage/lib/controller"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/pkg/api/v1"
	"k8s.io/client-go/rest"
)

const (
	provisionerName           = "example.com/isilon"
	exponentialBackOffOnError = false
	failedRetryThreshold      = 5
	serverEnvVar              = "ISI_SERVER"
	resyncPeriod              = 15 * time.Second
	leasePeriod               = controller.DefaultLeaseDuration
	retryPeriod               = controller.DefaultRetryPeriod
	renewDeadline             = controller.DefaultRenewDeadline
	termLimit                 = controller.DefaultTermLimit
)

type isilonProvisioner struct {
	// Identity of this isilonProvisioner, set to node's name. Used to identify
	// "this" provisioner's PVs.
	identity string

	isiClient *isi.Client
	// The directory to create the new volume in, as well as the
	// username, password and server to connect to
	volumeDir string
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

	// path will be required to create a working pv
	path := path.Join(p.volumeDir, pvName)

	// time to create the volume and export it
	// as of right now I dont think we need the volume info it returns
	rcVolume, err := p.isiClient.CreateVolume(context.Background(), pvName)
	glog.Infof("Created volume: %s", rcVolume)
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
		err := p.isiClient.SetQuotaSize(context.Background(), pvName, pvcSize)
		if err != nil {
			glog.Infof("Quota set to: %v on volume: %s", pvcSize, pvName)
		}
	}
	rcExport, err := p.isiClient.ExportVolume(context.Background(), pvName)
	glog.Infof("Created Isilon export: %v", rcExport)
	if err != nil {
		panic(err)
	}

	if err := os.MkdirAll(path, 0777); err != nil {
		return nil, err
	}

	// Get the mount options of the storage class
	mountOptions := ""
	for k, v := range options.Parameters {
		switch strings.ToLower(k) {
		case "mountoptions":
			mountOptions = v
		default:
			return nil, fmt.Errorf("invalid parameter: %q", k)
		}
	}

	pv := &v1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{
			Name: options.PVName,
			Annotations: map[string]string{
				"isilonProvisionerIdentity":               p.identity,
				"isilonVolume":                            pvName,
				"volume.beta.kubernetes.io/mount-options": mountOptions,
			},
		},
		Spec: v1.PersistentVolumeSpec{
			PersistentVolumeReclaimPolicy: options.PersistentVolumeReclaimPolicy,
			AccessModes:                   options.PVC.Spec.AccessModes,
			Capacity: v1.ResourceList{
				v1.ResourceName(v1.ResourceStorage): options.PVC.Spec.Resources.Requests[v1.ResourceName(v1.ResourceStorage)],
			},
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
	// Back out the quota settings first

	if p.quotaEnable {
		quota, _ := p.isiClient.GetQuota(context.Background(), isiVolume)
		if quota != nil {
			if err := p.isiClient.ClearQuota(context.Background(), isiVolume); err != nil {
				panic(err)
			}
		}
	}

	// if we get here we can destroy the volume
	if err := p.isiClient.Unexport(context.Background(), isiVolume); err != nil {
		panic(err)
	}

	// if we get here we can destroy the volume
	if err := p.isiClient.DeleteVolume(context.Background(), isiVolume); err != nil {
		panic(err)
	}

	return nil
}

func main() {
	syscall.Umask(0)

	flag.Parse()
	flag.Set("logtostderr", "true")

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
	isiPath := os.Getenv("ISI_PATH")
	if isiPath == "" {
		glog.Fatal("ISI_PATH not set")
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

	isiEndpoint := "https://" + isiServer + ":8080"
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
		panic(err)
	}

	glog.Info("Successfully connected to: " + isiEndpoint)

	// Create the provisioner: it implements the Provisioner interface expected by
	// the controller
	isilonProvisioner := &isilonProvisioner{
		identity:    isiServer,
		isiClient:   i,
		volumeDir:   isiPath,
		serverName:  isiServer,
		quotaEnable: isiQuota,
	}

	// Start the provision controller which will dynamically provision isilon
	// PVs
	pc := controller.NewProvisionController(clientset, resyncPeriod, provisionerName, isilonProvisioner, serverVersion.GitVersion, exponentialBackOffOnError, failedRetryThreshold, leasePeriod, renewDeadline, retryPeriod, termLimit)
	pc.Run(wait.NeverStop)
}
