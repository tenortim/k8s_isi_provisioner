# Copyright 2017 Mark DeNeve.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

.PHONY: image

IMAGE?=tenortim/k8s_isi_provisioner
GIT_VERSION := $(shell git describe --abbrev=4 --dirty --always --tags)

image: isi-provisioner
	docker build -t $(IMAGE) -f Dockerfile.scratch .

isi-provisioner: $(shell find . -name "*.go")
	GOOS=linux CGO_ENABLED=0 go build -a -ldflags '-extldflags "-static" -X main.version=$(GIT_VERSION)' -o k8s_isi_provisioner .

.PHONY: clean
clean:
	rm -rf vendor
	rm k8s_isi_provisioner
