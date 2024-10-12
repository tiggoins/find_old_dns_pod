package main

import (
	"bytes"
	"context"
	"os"
	"strings"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/remotecommand"

	"github.com/olekukonko/tablewriter"
)

var (
	timeout    time.Duration = time.Second * 10
	command                  = []string{"cat", "/etc/resolv.conf"}
	dnsServers               = []string{"20.46.0.1", "20.46.1.1"}
	// dnsServers = []string{"114.114.114.114"}
)

type K8s struct {
	restConfig *rest.Config
	client     *kubernetes.Clientset
	logger     *log.Logger
	ctx        context.Context

	hostpods map[string][]corev1.Pod
}

type result struct {
	podName   string
	namespace string
	stdout    string
	stderr    string
	err       error
}

func NewK8s() (*K8s, error) {
	k := &K8s{}

	config, err := clientcmd.BuildConfigFromFlags("", clientcmd.RecommendedHomeFile)
	if err != nil {
		return nil, err
	}
	k.restConfig = config

	// 初始化clientset
	k.client, err = kubernetes.NewForConfig(k.restConfig)
	if err != nil {
		return nil, err
	}

	// 初始化logger
	k.logger = &log.Logger{
		Out:       os.Stdout,
		Formatter: &log.TextFormatter{FullTimestamp: true},
		Hooks:     make(log.LevelHooks),
		Level:     log.InfoLevel,
	}

	return k, nil
}

// 返回hostNetwork类型的Pod
func (k *K8s) getHostNetworkPods() (*K8s, error) {
	k.hostpods = make(map[string][]corev1.Pod, 0)
	k.logger.Infoln("开始抓取hostNetwork类型的Pod,注意仅计算Running状态的Pod")
	pods, err := k.client.CoreV1().Pods("").List(k.ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	for _, pod := range pods.Items {
		if pod.Spec.HostNetwork && pod.Status.Phase == corev1.PodRunning {
			k.hostpods[pod.Namespace] = append(k.hostpods[pod.Namespace], pod)
		}
	}

	k.logger.Infoln("成功取回hostNetwork类型的Pod")
	return k, nil
}

// 找出仍在使用旧DNS的Pod
func (k *K8s) Exec(pod corev1.Pod, namespace string, results chan<- result) {
	req := k.client.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(pod.Name).
		Namespace(namespace).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: pod.Spec.Containers[0].Name, // 默认取第一次container作为执行目标
			Command:   command,
			Stdin:     false,
			Stdout:    true,
			Stderr:    true,
			TTY:       false,
		}, scheme.ParameterCodec)

	exec, err := remotecommand.NewSPDYExecutor(k.restConfig, "POST", req.URL())
	if err != nil {
		results <- result{podName: pod.Name, namespace: namespace, err: err}
		return
	}

	var stdout, stderr bytes.Buffer
	err = exec.StreamWithContext(k.ctx, remotecommand.StreamOptions{
		Stdout: &stdout,
		Stderr: &stderr,
	})

	results <- result{podName: pod.Name, namespace: namespace, stdout: stdout.String(), stderr: stderr.String(), err: err}
}

func (k *K8s) CheckPods() {
	results := make(chan result)
	var wg sync.WaitGroup

	for namespace, pods := range k.hostpods {
		for _, pod := range pods {
			wg.Add(1)
			go func(pod corev1.Pod, namespace string) {
				defer wg.Done()
				k.Exec(pod, namespace, results)
			}(pod, namespace)
		}
	}

	go func() {
		wg.Wait()
		close(results)
	}()

	matchingPods := make(map[string][]string)
	for res := range results {
		if res.err != nil {
			k.logger.Errorf("Error executing command in pod: %s/%s, err = %s\n", res.namespace, res.podName, res.err.Error())
			continue
		}

		if strings.Contains(res.stdout, dnsServers[0]) || strings.Contains(res.stdout, dnsServers[1]) {
			matchingPods[res.namespace] = append(matchingPods[res.namespace], res.podName)
		}
	}

	if len(matchingPods) == 0 {
		k.logger.Infoln("未发现使用 20.46.0.1/20.46.1.1 的pod")
		os.Exit(0)
	} else {
		k.logger.Infoln("仍在使用20.46.0.1/20.46.1.1的pod如下表:")
	}

	table := tablewriter.NewWriter(os.Stdout)
	table.SetHeader([]string{"Namespace", "Pods"})
	table.SetRowLine(true) // 设置为每个命名空间添加分割线

	for namespace, pods := range matchingPods {
		table.Append([]string{namespace, strings.Join(pods, "\n")})
	}

	table.Render()
}

func main() {
	k, err := NewK8s()
	if err != nil {
		k.logger.Fatalln(err.Error())
	}

	// 初始化context
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	k.ctx = ctx
	defer cancel()

	pods, err := k.getHostNetworkPods()
	if err != nil {
		k.logger.Fatalln(err.Error())
	}
	k.hostpods = pods.hostpods

	if len(k.hostpods) == 0 {
		k.logger.Infoln("未发现hostNetwork类型的Pod")
		os.Exit(0)
	}

	k.CheckPods()
}
