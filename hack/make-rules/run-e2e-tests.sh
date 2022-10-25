#!/usr/bin/env bash

# Copyright 2020 The OpenYurt Authors.
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

set -x
set -e
set -u

YURT_ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)
source "${YURT_ROOT}/hack/lib/init.sh"
source "${YURT_ROOT}/hack/lib/build.sh"

readonly LOCAL_ARCH=$(go env GOHOSTARCH)
readonly LOCAL_OS=$(go env GOHOSTOS)
readonly YURT_E2E_TARGETS="test/e2e/yurt-e2e-test"
readonly EDGE_AUTONOMY_NODES_NUM=3
cloudNodeContainerName="openyurt-e2e-test-control-plane"
edgeNodeContainerName="openyurt-e2e-test-worker"
edgeNodeContainer2Name="openyurt-e2e-test-worker2"
KUBECONFIG=$HOME/.kube/config


function set_flags() {
    goldflags="${GOLDFLAGS:--s -w $(project_info)}"
    gcflags="${GOGCFLAGS:-}"
    goflags=${GOFLAGS:-}

    target_bin_dir=$(get_binary_dir_with_arch ${YURT_LOCAL_BIN_DIR})
    mkdir -p ${target_bin_dir}
    cd ${target_bin_dir}
    echo "Building ${YURT_E2E_TARGETS}"
    testpkg="$(dirname ${YURT_E2E_TARGETS})"
    filename="$(basename ${YURT_E2E_TARGETS})"
#    set kubeconfig
    docker exec -d $edgeNodeContainerName /bin/bash -c 'mkdir /root/.kube/ -p'
    docker exec -d $edgeNodeContainerName /bin/bash -c  "echo 'export KUBECONFIG=/root/.kube/config' >> /etc/profile && source /etc/profile" 
    docker cp $KUBECONFIG $edgeNodeContainerName:/root/.kube/config
}

# set up flannel
function setUpFlannel() {
    local flannelYaml="https://raw.githubusercontent.com/flannel-io/flannel/master/Documentation/kube-flannel.yml"
    local flannelDs="kube-flannel-ds"
    local flannelNameSpace="kube-flannel"
    local POD_CREATE_TIMEOUT=120s
    curl -o ${YURT_ROOT}/flannel.yaml $flannelYaml
    kubectl apply -f ${YURT_ROOT}/flannel.yaml
    # check if flannel on every node is ready, if so, "daemon set "kube-flannel-ds" successfully rolled out"
    kubectl rollout status daemonset kube-flannel-ds -n kube-flannel --timeout=${POD_CREATE_TIMEOUT}

    # set up bridge cni plugins for every node
    wget -O ${YURT_ROOT}/cni.tgz https://github.com/containernetworking/plugins/releases/download/v1.1.1/cni-plugins-linux-amd64-v1.1.1.tgz

    docker cp ${YURT_ROOT}/cni.tgz $cloudNodeContainerName:/opt/cni/bin/
    docker exec -t $cloudNodeContainerName /bin/bash -c 'cd /opt/cni/bin && tar -zxf cni.tgz'

    docker cp ${YURT_ROOT}/cni.tgz $edgeNodeContainerName:/opt/cni/bin/
    docker exec -t $edgeNodeContainerName /bin/bash -c 'cd /opt/cni/bin && tar -zxf cni.tgz'

    docker cp ${YURT_ROOT}/cni.tgz $edgeNodeContainer2Name:/opt/cni/bin/
    docker exec -t $edgeNodeContainer2Name /bin/bash -c 'cd /opt/cni/bin && tar -zxf cni.tgz'    
}

# install gingko
function getGinkgo() {
    go install github.com/onsi/ginkgo/v2/ginkgo@v2.3.0
    go get github.com/onsi/gomega/...
    go mod tidy
}

# run e2e tests
function run_non_edge_autonomy_e2e_tests {
    # check kubeconfig
        if [ ! -f "${KUBECONFIG}" ]; then
            echo "kubeconfig does not exist at ${KUBECONFIG}"
            exit -1
        fi
    # run non-edge-autonomy-e2e-tests
    cd $YURT_ROOT/test/e2e/
    ginkgo --gcflags "${gcflags:-}" ${goflags} --label-filter='!edge-autonomy' -r
}

function run_e2e_edge_autonomy_tests {
     # check kubeconfig
        if [ ! -f "${KUBECONFIG}" ]; then
            echo "kubeconfig does not exist at ${KUBECONFIG}"
            exit -1
        fi
    # run edge-autonomy-e2e-tests
    cd $YURT_ROOT/test/e2e/
    ginkgo --gcflags "${gcflags:-}" ${goflags} --label-filter='edge-autonomy' -r 
}

function cpCodeToEdge {
    local edgeNodeContainerName="openyurt-e2e-test-worker"
    local openyurtCodePath=${YURT_ROOT}
    docker cp $openyurtCodePath $edgeNodeContainerName:/
}

function serviceNginx {
#   run a nginx pod as static pod on each edge node
    local nginxYamlPath="${YURT_ROOT}/test/e2e/yamls/nginx.yaml"
    local nginxServiceYamlPath="${YURT_ROOT}/test/e2e/yamls/nginxService.yaml"
    local staticPodPath="/etc/kubernetes/manifests/"
    local POD_CREATE_TIMEOUT=240s

#   create service for nginx pods
    kubectl apply -f $nginxServiceYamlPath
    docker cp $nginxYamlPath $edgeNodeContainerName:$staticPodPath
    docker cp $nginxYamlPath $edgeNodeContainer2Name:$staticPodPath
#   wait confirm that nginx is running
    kubectl wait --for=condition=Ready pod/yurt-e2e-test-nginx-openyurt-e2e-test-worker --timeout=${POD_CREATE_TIMEOUT}
    kubectl wait --for=condition=Ready pod/yurt-e2e-test-nginx-openyurt-e2e-test-worker2 --timeout=${POD_CREATE_TIMEOUT}

#   set up dig in edge node1 
    docker exec -t $edgeNodeContainerName /bin/bash -c "sed -i -r 's/([a-z]{2}.)?archive.ubuntu.com/old-releases.ubuntu.com/g' /etc/apt/sources.list"
    docker exec -t $edgeNodeContainerName /bin/bash -c "sed -i -r 's/security.ubuntu.com/old-releases.ubuntu.com/g' /etc/apt/sources.list"
    docker exec -t $edgeNodeContainerName /bin/bash -c "apt-get update && apt-get install dnsutils -y"
}

GOOS=${LOCAL_OS} GOARCH=${LOCAL_ARCH} set_flags

setUpFlannel

getGinkgo

serviceNginx

run_non_edge_autonomy_e2e_tests

run_e2e_edge_autonomy_tests