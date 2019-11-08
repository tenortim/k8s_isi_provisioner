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

If you need to create exports in an access zone other than the System zone, set the ISI\_ZONE environment variable in pod.yaml.

___
Create the isilon storage class using the class.yaml file

`oc create -f class.yaml`

or

`kubectl create -f class.yaml`

Note, the NFS mount options that the pod will use are specified at the time that the storage class is created.
If additional volume mount options (e.g. forcing NFS version 3) are needed, they
can be specified in the class file:

`oc create -f class-with-mount-options.yaml`

or

`kubectl create -f class-with-mount-options.yaml`

___
Example code to create a persistent volume claim named isilon-pvc:

`oc create -f claim.yaml`

or

`kubectl create -f claim.yaml`

___
Tested against:
<https://www.emc.com/products-solutions/trial-software-download/isilon.htm>

This provisioner has support for Isilon Storage Quotas. When enabled, hard, enforcing directory
quotas will be created based on the requested size of the volume. The container flag is not
currently set so the reported size from 'df' will not reflect the limit, but it will be enforced.
This will a require a revision of the goisilon library to support the functionality.

## Parameters

**Param**|**Description**|**Example**
:-----:|:-----:|:-----:
ISI\_SERVER|The DNS name (or IP address) of the Isilon to use for mount requests| isilon.somedomain.com
ISI\_API\_SERVER|The DNS name (or IP address) of the Isilon to use for API access to create the volume (defaults to ISI\_SERVER)| isilon-mgmt.somedomain.com
ISI\_PATH|The root path for all exports to be created in| \/ifs\/ose\_exports
ISI\_ZONE|The access zone for all exports to be created in (defaults to System)|System
ISI\_USER|The user to connect to the isilon as|admin
ISI\_PASS|Password for the user account|password
ISI\_GROUP|The default group to assign to the share|users
ISI\_QUOTA\_ENABLE|Enable the use of quotas.  Defaults to disabled. | FALSE or TRUE
ISI\_CLIENTS|List of client CIDRs allowed in the export. space-separated |null
PROVISIONER\_NAME|Alternate name to allow registering multiple providers with different parameters. Defaults to "isilon"| isilon

## Thanks

Thanks to the developers of the Kubernetes external storage provisioner code and the docs that are making this possible to do.
Thanks to Dell EMC {code} for the great Isilon library.
