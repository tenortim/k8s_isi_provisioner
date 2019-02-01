# k8s_isi_provisioner
[![Build Status](https://travis-ci.org/tenortim/k8s_isi_provisioner.svg?branch=master)](https://travis-ci.org/tenortim/k8s_isi_provisioner.svg?branch=master)
[![Go Report Card](https://goreportcard.com/badge/github.com/tenortim/k8s_isi_provisioner)](https://goreportcard.com/report/github.com/tenortim/k8s_isi_provisioner)
[![Docker Pulls](https://img.shields.io/docker/pulls/tenortim/k8s_isi_provisioner.svg)](https://hub.docker.com/r/tenortim/k8s_isi_provisioner/)

Kubernetes external storage provisioner for Dell Isilon

Based on the following:
https://github.com/kubernetes-incubator/external-storage
https://github.com/tenortim/goisilon

Instructions:
In order to use this external provisioner, you can use the image pushed to docker hub "tenortim/k8s\_isi\_provisioner", or build it yourself.

Building
--------
To build this provisioner, ensure you have go, and glide installed.  This code has been tested with Go 1.8 and higher.
To build the software, run make.

The provisioner requires permissions if you are running it in OpenShift.
The persistent-volume-provisioner cluster role in OpenShift 3.11 is missing
the needed endpoints permissions and so permissions are supplied by auth.yaml
for both OpenShift and pure Kubernetes:
```
oc adm create -f auth.yaml
```
vs
```
kubectl create -f auth.yaml
```

To deploy the provisioner in OpenShift, run
```
oc create -f pod.yaml
```
Create a storage class using the class.yaml file
```
oc create -f class.yaml
```

Or in Kubernetes, run:
```
kubectl create -f pod.yaml
kubectl create -f class.yaml
```

If it is required to use NFSv3, use the alternate yaml file:
```
kubectl create -f class-with-mount-options.yaml
```

Example code to create a persistent volume named isilon-pvc:
```
oc create -f claim.yaml
```
or on Kubernetes:
```
kubectl create -f claim.yaml
```


Tested against: 
https://www.emc.com/products-solutions/trial-software-download/isilon.htm

This provisioner has support for Isilon Storage Quotas, but this has not yet been tested.

## Parameters
**Param**|**Description**|**Example**
:-----:|:-----:|:-----:
ISI\_SERVER|The DNS name (or IP address) of the Isilon to use for mount requests| isilon.somedomain.com
ISI\_API\_SERVER|The DNS name (or IP address) of the Isilon to use for API access to create the volume (defaults to ISI\_SERVER)| isilon-mgmt.somedomain.com
ISI\_PATH|The root path for all exports to be created in| \/ifs\/ose\_exports
ISI\_USER|The user to connect to the isilon as|admin
ISI\_PASS|Password for the user account|password
ISI\_GROUP|The default group to assign to the share|users
ISI\_QUOTA\_ENABLE|Enable the use of quotas.  Defaults to disabled. | FALSE or TRUE

## Thanks

Thanks to the developers of the Kubernetes external storage provisioner code and the docs that are making this possible to do.
Thanks to Dell EMC {code} for the great Isilon library.
