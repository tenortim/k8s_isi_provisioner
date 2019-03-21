# k8s_isi_provisioner

[![Build Status](https://travis-ci.org/tenortim/k8s_isi_provisioner.svg?branch=master)](https://travis-ci.org/tenortim/k8s_isi_provisioner.svg?branch=master)
[![Go Report Card](https://goreportcard.com/badge/github.com/tenortim/k8s_isi_provisioner)](https://goreportcard.com/report/github.com/tenortim/k8s_isi_provisioner)
[![Docker Pulls](https://img.shields.io/docker/pulls/tenortim/k8s_isi_provisioner.svg)](https://hub.docker.com/r/tenortim/k8s_isi_provisioner/)

Kubernetes external storage provisioner for Dell Isilon

Based on the following:

* <https://github.com/kubernetes-sigs/sig-storage-lib-external-provisioner>
* <https://github.com/tenortim/goisilon>

Instructions:
In order to use this external provisioner, you can use the image pushed to docker hub "tenortim/k8s\_isi\_provisioner", or build it yourself.

## Building

To build this provisioner, ensure you have [Go](https://golang.org/dl/) installed.
This code requires a minimum of Go 1.11.\
To build the software, run make.

## Deploying

The provisioner requires various permissions whether you are running it in raw Kubernetes or in OpenShift.
The persistent-volume-provisioner cluster role in OpenShift 3.11 is missing the needed endpoints permissions and so permissions are supplied by auth.yaml for both OpenShift and pure Kubernetes:

`oc create -f auth.yaml`

or

`kubectl create -f auth.yaml`

___
To deploy the provisioner, run

`oc create -f pod.yaml`

or

`kubectl create -f pod.yaml`

In version 2 of the provisioner, the pod comfiguration is generic and all configuration (cluster, accounts, access zone, quotas etc.) are defined at the storage class level, so a single provisioner instance can configure volumes in multiple different access zones on multiple clusters.

___
Create at least one isilon storage class using the class.yaml file as a template for the class

`oc create -f class.yaml`

or

`kubectl create -f class.yaml`

Note, the NFS mount options that the pod will use are specified at the time that the storage class is created.
If additional volume mount options (e.g. forcing NFS version 3) are needed, they
can be specified in the class file:

`oc create -f class-with-mount-options.yaml`

or

`kubectl create -f class-with-mount-options.yaml`

The cluster password is now stored as a k8s/OpenShift secret. Create an entry that matches the secretName/secretNamespace in the storage class definition e.g.:

`oc create secret generic cluster1-password --type="isilon.com/isilon" --from-literal=password=sekr3t --namespace=default`

or

`kubectl create secret generic cluster1-password --type="isilon.com/isilon" --from-literal=password=sekr3t --namespace=default`

___
Example code to create a persistent volume claim named isilon-pvc:

`oc create -f claim.yaml`

or

`kubectl create -f claim.yaml`

___
Tested against:
<https://www.emc.com/products-solutions/trial-software-download/isilon.htm>

This provisioner has support for Isilon Storage Quotas. When enabled, hard, enforcing directory
quotas will be created based on the requested size of the volume. The container flag is set to true for the quota so
the reported size from 'df' will correctly reflect the remaining free space against the limit.

## Parameters

The following parameters are defined at the storage class level:

**Param**|**Description**|**Example**
:-----:|:-----:|:-----:
server|The DNS name (or IP address) of the Isilon to use for mount requests| isilon.somedomain.com
apiserver|The DNS name (or IP address) of the Isilon to use for API access to create the volume (defaults to the server value)| isilon-mgmt.somedomain.com
basepath|The base path within \/ifs where exports will be created| \/ifs\/ose\_exports
apiuser|The user to connect to the isilon as|admin
secretName|Name of the k8s secret in which the API password is stored|cluster1-password
secretNamespace|Name of the k8s namespace in which the secret is stored|default
group|The default group to assign to the share|Users
quotas|Enable the use of quotas.  Defaults to disabled. | "false" or "true"
zone|The access zone for in which exports for this storage class are to be created. Defaults to "System"|System

## Thanks

Thanks to the developers of the Kubernetes external storage provisioner code and the docs that are making this possible to do.
Thanks to Dell EMC {code} for the Isilon API binding library.
