/*
Copyright 2020 The OpenYurt Authors.

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

package yurthub

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"

	"github.com/openyurtio/openyurt/test/e2e/common/ns"
	p "github.com/openyurtio/openyurt/test/e2e/common/pod"
	"github.com/openyurtio/openyurt/test/e2e/util"
	"github.com/openyurtio/openyurt/test/e2e/util/ginkgowrapper"
	ycfg "github.com/openyurtio/openyurt/test/e2e/yurtconfig"
)

const (
	YurtE2ENamespaceName     = "yurt-e2e-test"
	YurtDefaultNamespaceName = "default"
	YurtSystemNamespaceName  = "kube-system"
	YurtCloudNodeName        = "openyurt-e2e-test-control-plane"
	YurtEdgeNodeName         = "openyurt-e2e-test-worker"
	YurtEdgeNode2Name        = "openyurt-e2e-test-worker2"
	NginxServiceName         = "yurt-e2e-test-nginx"
	CoreDNSServiceName       = "kube-dns"
)

var (
	c                clientset.Interface
	err              error
	Edge2NginxPodIP  string
	NginxServiceIP   string
	CoreDNSServiceIP string

	flannelContainerID   string
	yurthubContainerID   string
	kubeProxyContainerID string
	coreDnsContainerID   string
	coreDnsNodeName      string
	nginxContainerID     string
)

var _ = ginkgo.Describe("edge-autonomy"+YurtE2ENamespaceName, ginkgo.Ordered, ginkgo.Label("edge-autonomy"), func() {
	defer ginkgo.GinkgoRecover()
	var _ = ginkgo.Describe("kubelet"+YurtE2ENamespaceName, func() {
		gomega.RegisterFailHandler(ginkgowrapper.Fail)
		ginkgo.It("kubelet edge-autonomy test", ginkgo.Label("edge-autonomy"), func() {
			// restart kubelet using systemctl restart kubelet in edge nodes；check if kubelet restarted periodically
			gomega.Eventually(func() bool {
				_, err := exec.Command("/bin/bash", "-c", "docker exec -t openyurt-e2e-test-worker /bin/bash -c 'systemctl restart kubelet'").CombinedOutput()
				if err != nil {
					return false
				}
				return true
			}).Should(gomega.BeTrue(), "fail to restart kubelet")

			gomega.Eventually(func() bool {
				opBytes, err := exec.Command("/bin/bash", "-c", "docker exec -t openyurt-e2e-test-worker /bin/bash -c 'curl http://127.0.0.1:10248/healthz'").CombinedOutput()
				if err != nil {
					return false
				}
				if string(opBytes) == "ok" {
					return true
				} else {
					return false
				}
			}).WithTimeout(10*time.Second).WithPolling(1*time.Second).Should(gomega.BeTrue(), "fail to check kubelet health")

			//check if nginx restarted successfully
			gomega.Eventually(func() string {
				opBytes, err := exec.Command("/bin/bash", "-c", "docker exec -t openyurt-e2e-test-worker /bin/bash -c 'curl http://127.0.0.1:80'").CombinedOutput()
				if err != nil {
					return ""
				}
				return string(opBytes)
			}).Should(gomega.ContainSubstring("nginx"), "nginx pod not running")
		})
	})

	var _ = ginkgo.Describe("flannel"+YurtE2ENamespaceName, func() {
		ginkgo.It("flannel edge-autonomy test", ginkgo.Label("edge-autonomy"), func() {
			// obtain flannel containerID with crictl
			gomega.Eventually(func() bool {
				cmd := `docker exec -t openyurt-e2e-test-worker /bin/bash -c "crictl ps | grep kube-flannel | awk '{print \$1}'"`
				opBytes, err := exec.Command("/bin/bash", "-c", cmd).CombinedOutput()
				if err != nil {
					return false
				}
				flannelContainerID = strings.Trim(string(opBytes), "\r\n")
				return true
			}).Should(gomega.BeTrue(), "fail to get flannel container ID")
			// restart flannel
			gomega.Eventually(func() bool {
				_, err := exec.Command("/bin/bash", "-c", "docker exec -t openyurt-e2e-test-worker /bin/bash -c 'crictl stop "+flannelContainerID+"'").CombinedOutput()
				if err != nil {
					return false
				}
				return true
			}).Should(gomega.BeTrue(), "fail to stop flannel")
			// obtain nginx containerID with crictl
			gomega.Eventually(func() bool {
				cmd := `docker exec -t openyurt-e2e-test-worker /bin/bash -c "crictl ps | grep yurt-e2e-test-nginx | awk '{print \$1}'"`
				opBytes, err := exec.Command("/bin/bash", "-c", cmd).CombinedOutput()
				if err != nil {
					return false
				}
				nginxContainerID = strings.Trim(string(opBytes), "\r\n")
				return true
			}).Should(gomega.BeTrue(), "fail to get nginx container ID")

			// curl pod on another edge node using podIP, periodically
			gomega.Eventually(func() string {
				curlCmd := "curl " + Edge2NginxPodIP
				crictlCmd := "crictl exec -it " + nginxContainerID + " " + curlCmd
				dockerCmd := `docker exec -t openyurt-e2e-test-worker /bin/bash -c ` + "'" + crictlCmd + "'"
				opBytes, err := exec.Command("/bin/bash", "-c", dockerCmd).CombinedOutput()
				if err != nil {
					return ""
				}
				return string(opBytes)
			}).WithTimeout(10*time.Second).WithPolling(1*time.Second).Should(gomega.ContainSubstring("nginx"), "fail to curl worker2 nginx PodIP from nginx on worker1")
		})
	})

	var _ = ginkgo.Describe("yurthub"+YurtE2ENamespaceName, func() {
		ginkgo.It("yurthub edge-autonomy test", ginkgo.Label("edge-autonomy"), func() {
			// obtain yurthub containerID with crictl
			gomega.Eventually(func() bool {
				cmd := `docker exec -t openyurt-e2e-test-worker /bin/bash -c "crictl ps | grep yurt-hub | awk '{print \$1}'"`
				opBytes, err := exec.Command("/bin/bash", "-c", cmd).CombinedOutput()
				if err != nil {
					return false
				}
				yurthubContainerID = strings.Trim(string(opBytes), "\r\n")
				return true
			}).Should(gomega.BeTrue(), "fail to get yurthub container ID")
			// restart flannel
			gomega.Eventually(func() bool {
				_, err := exec.Command("/bin/bash", "-c", "docker exec -t openyurt-e2e-test-worker /bin/bash -c 'crictl stop "+yurthubContainerID+"'").CombinedOutput()
				if err != nil {
					return false
				}
				return true
			}).Should(gomega.BeTrue(), "fail to stop yurthub")

			gomega.Eventually(func() bool {
				opBytes, err := exec.Command("/bin/bash", "-c", "docker exec -t openyurt-e2e-test-worker /bin/bash -c 'curl http://127.0.0.1:10267/v1/healthz'").CombinedOutput()
				if err != nil {
					return false
				}
				if strings.Contains(string(opBytes), "OK") {
					return true
				} else {
					return false
				}
			}).WithTimeout(120*time.Second).WithPolling(1*time.Second).Should(gomega.BeTrue(), "fail to check yurthub health")
		})
	})

	var _ = ginkgo.Describe("kube-proxy"+YurtE2ENamespaceName, func() {
		ginkgo.It("kube-proxy edge-autonomy test", ginkgo.Label("edge-autonomy"), func() {
			// obtain kube-proxy containerID with crictl
			gomega.Eventually(func() bool {
				cmd := `docker exec -t openyurt-e2e-test-worker /bin/bash -c "crictl ps | grep kube-proxy | awk '{print \$1}'"`
				opBytes, err := exec.Command("/bin/bash", "-c", cmd).CombinedOutput()
				if err != nil {
					return false
				}
				kubeProxyContainerID = strings.Trim(string(opBytes), "\r\n")
				return true
			}).Should(gomega.BeTrue(), "fail to get kube-proxy container ID")
			// restart kube-proxy
			gomega.Eventually(func() bool {
				_, err := exec.Command("/bin/bash", "-c", "docker exec -t openyurt-e2e-test-worker /bin/bash -c 'crictl stop "+kubeProxyContainerID+"'").CombinedOutput()
				if err != nil {
					return false
				}
				return true
			}).Should(gomega.BeTrue(), "fail to stop kube-proxy")

			gomega.Eventually(func() bool {
				_, err := exec.Command("/bin/bash", "-c", "docker exec -t openyurt-e2e-test-worker /bin/bash -c 'iptables -F'").CombinedOutput()
				if err != nil {
					return false
				}
				return true
			}).Should(gomega.BeTrue(), "fail to remove iptables on node "+YurtEdgeNode2Name)

			gomega.Eventually(func() string {
				opBytes, err := exec.Command("/bin/bash", "-c", "docker exec -t openyurt-e2e-test-worker /bin/bash -c 'curl "+NginxServiceIP+"'").CombinedOutput()
				if err != nil {
					return ""
				}
				return string(opBytes)
			}).WithTimeout(10*time.Second).WithPolling(1*time.Second).Should(gomega.ContainSubstring("nginx"), "fail to read curl response from service: "+NginxServiceName)
		})
	})

	var _ = ginkgo.Describe("coredns"+YurtE2ENamespaceName, func() {
		ginkgo.It("coredns edge-autonomy test", ginkgo.Label("edge-autonomy"), func() {
			ginkgo.Skip("current coredns does not support edge-autonomy, coredns-edge-autonomy tests will be skipped.")
			// obtain coredns containerID with crictl
			gomega.Eventually(func() bool {
				cmd := ` /bin/bash -c "crictl ps | grep coredns | awk '{print \$1}'"`
				opBytes, err := exec.Command("/bin/bash", "-c", "docker exec -t "+coreDnsNodeName+cmd).CombinedOutput()
				if err != nil {
					return false
				}
				coreDnsContainerID = strings.Trim(string(opBytes), "\r\n")
				return true
			}).Should(gomega.BeTrue(), "fail to get coredns container ID")
			// restart kube-proxy
			gomega.Eventually(func() bool {
				cmd := "docker exec -t " + coreDnsNodeName + " /bin/bash -c 'crictl stop " + coreDnsContainerID + "'"
				_, err := exec.Command("/bin/bash", "-c", cmd).CombinedOutput()
				if err != nil {
					return false
				}
				return true
			}).Should(gomega.BeTrue(), "fail to stop coredns")
			gomega.Eventually(func() string {
				cmd := "docker exec -t openyurt-e2e-test-worker /bin/bash -c 'dig " + "@" + CoreDNSServiceIP + " " + NginxServiceName + "." + YurtDefaultNamespaceName + ".svc.cluster.local'"
				opBytes, err := exec.Command("/bin/bash", "-c", cmd).CombinedOutput()
				if err != nil {
					return ""
				}
				return string(opBytes)
			}).WithTimeout(10*time.Second).WithPolling(1*time.Second).Should(gomega.ContainSubstring("NOERROR"), "DNS resolution contains error, coreDNS dig failed")
		})
	})
})

func TestEdgeAutonomy(t *testing.T) {
	ginkgo.BeforeSuite(func() {
		gomega.RegisterFailHandler(ginkgowrapper.Fail)

		error := util.SetYurtE2eCfg()
		gomega.Expect(error).NotTo(gomega.HaveOccurred(), "fail set Yurt E2E Config")

		c = ycfg.YurtE2eCfg.KubeClient
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "fail to get client set")

		err = ns.DeleteNameSpace(c, YurtE2ENamespaceName)
		util.ExpectNoError(err)
		ginkgo.By("create e2e-test namespace")
		_, err = ns.CreateNameSpace(c, YurtE2ENamespaceName)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "fail to create namespaces")

		// get Ningx podIP on edge node worker2
		cs := c
		podName := "yurt-e2e-test-nginx-openyurt-e2e-test-worker2"
		ginkgo.By("get pod info:" + podName)
		pod, err := p.GetPod(cs, YurtDefaultNamespaceName, podName)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "fail to get pod nginx on edge node 2")

		Edge2NginxPodIP = pod.Status.PodIP
		klog.Infof("get PodIP of Nginx on edge node 2: %s", Edge2NginxPodIP)

		// get Ningx serviceIP
		ginkgo.By("get service info" + NginxServiceName)
		nginxSvc, err := c.CoreV1().Services(YurtDefaultNamespaceName).Get(context.Background(), NginxServiceName, metav1.GetOptions{})
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "fail to get service : "+NginxServiceName)

		NginxServiceIP = nginxSvc.Spec.ClusterIP
		klog.Infof("get ServiceIP of service : " + NginxServiceName + " IP: " + NginxServiceIP)

		//get coredns serviceIP
		ginkgo.By("get service info" + CoreDNSServiceName)
		coreDNSSvc, error := c.CoreV1().Services(YurtSystemNamespaceName).Get(context.Background(), CoreDNSServiceName, metav1.GetOptions{})
		gomega.Expect(error).NotTo(gomega.HaveOccurred(), "fail to get service : "+CoreDNSServiceName)

		CoreDNSServiceIP = coreDNSSvc.Spec.ClusterIP
		klog.Infof("get ServiceIP of service : " + CoreDNSServiceName + " IP: " + CoreDNSServiceIP)

		//get coredns NodeName
		gomega.Eventually(func() bool {
			opBytes, err := exec.Command("/bin/bash", "-c", "kubectl get po -l k8s-app=kube-dns -n kube-system -o wide | grep worker").CombinedOutput()
			if err != nil {
				return false
			}
			str := string(opBytes)
			if strings.Contains(str, "worker2") {
				coreDnsNodeName = YurtEdgeNode2Name
			} else {
				coreDnsNodeName = YurtEdgeNodeName
			}
			return true
		}).Should(gomega.BeTrue(), "fail to get core dns node name")

		// disconnect cloud node
		cmd := exec.Command("/bin/bash", "-c", "docker network disconnect kind "+YurtCloudNodeName)
		error = cmd.Run()
		gomega.Expect(error).NotTo(gomega.HaveOccurred(), "fail to disconnect cloud node to kind bridge: docker network disconnect kind %s", YurtCloudNodeName)
		klog.Infof("successfully disconnected cloud node")
	})
	ginkgo.AfterSuite(func() {
		// reconnect cloud node to docker network
		cmd := exec.Command("/bin/bash", "-c", "docker network connect kind "+YurtCloudNodeName)
		error := cmd.Run()
		gomega.Expect(error).NotTo(gomega.HaveOccurred(), "fail to reconnect cloud node to kind bridge")
		klog.Infof("successfully reconnected cloud node")

		ginkgo.By("delete namespace:" + YurtE2ENamespaceName)
		err = ns.DeleteNameSpace(c, YurtE2ENamespaceName)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "fail to delete created namespaces")
	})
	ginkgo.RunSpecs(t, "yurt-edge-autonomy")
}
