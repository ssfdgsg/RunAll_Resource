package data

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"resource/internal/biz"
	"resource/internal/conf"
	"strconv"

	"github.com/go-kratos/kratos/v2/log"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

type gpuInfo struct {
	nums uint32
	name string
}

var gpuTypeMap = map[uint32]gpuInfo{
	0: {nums: 0, name: ""},                        // 0 = no specific GPU type (any GPU)
	1: {nums: 1, name: "nvidia-tesla-v100"},       // 1 = Tesla V100
	2: {nums: 1, name: "nvidia-tesla-a100"},       // 2 = Tesla A100
	3: {nums: 1, name: "nvidia-tesla-h100"},       // 3 = Tesla H100
	4: {nums: 1, name: "nvidia-tesla-t4"},         // 4 = Tesla T4
	5: {nums: 1, name: "nvidia-geforce-rtx3090"},  // 5 = GeForce RTX 3090
	6: {nums: 1, name: "nvidia-geforce-rtx4060m"}, // 6 = GeForce RTX 4060 Mobile
}

type createRequest struct {
	pod    *corev1.Pod
	option metav1.CreateOptions
}

type k8sRepo struct {
	client    kubernetes.Interface
	log       *log.Helper
	requestCh chan *createRequest
}

func kubeconfigPath(c *conf.Data) (string, error) {
	if c != nil && c.GetKubernetes() != nil && c.GetKubernetes().GetKubeconfig() != "" {
		return c.GetKubernetes().GetKubeconfig(), nil
	}
	if env := os.Getenv("KUBECONFIG"); env != "" {
		return env, nil
	}
	home := os.Getenv("HOME")
	if home == "" {
		home = os.Getenv("USERPROFILE")
	}
	if home == "" {
		return "", errors.New("kubeconfig path not configured")
	}
	return filepath.Join(home, ".kube", "config"), nil
}

// NewK8sRepo bootstraps a Kubernetes repo with a shared kubeconfig.
func NewK8sRepo(c *conf.Data, logger log.Logger) (biz.K8sRepo, error) {
	helper := log.NewHelper(logger)
	path, err := kubeconfigPath(c)
	if err != nil {
		return nil, err
	}
	cfg, err := clientcmd.BuildConfigFromFlags("", path)
	if err != nil {
		helper.Errorf("failed to load kubeconfig %s: %v", path, err)
		return nil, err
	}
	client, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, err
	}
	repo := &k8sRepo{
		client:    client,
		log:       helper,
		requestCh: make(chan *createRequest, 100),
	}
	for i := 0; i < 10; i++ {
		go repo.CreateWorker()
	}
	return repo, nil
}

// CreateWorker pulls pod create requests from the queue.
func (r *k8sRepo) CreateWorker() {
	for req := range r.requestCh {
		if _, err := r.client.CoreV1().Pods("default").Create(context.Background(), req.pod, req.option); err != nil {
			r.log.Errorf("failed to create pod %s: %v", req.pod.Name, err)
		}
	}
}

func (r *k8sRepo) CreateInstance(ctx context.Context, spec biz.InstanceSpec) error {
	if r.client == nil {
		return fmt.Errorf("k8s client is not initialized")
	}

	resourceList := corev1.ResourceList{}
	cpuMilli := int64(spec.CPU) * 1000
	memBytes := int64(spec.Memory) * 1024 * 1024
	if cpuMilli <= 0 {
		cpuMilli = 1000
		r.log.WithContext(ctx).Warnf("spec %d cpu not provided, fallback to 1 vCPU", spec.InstanceID)
	}
	if memBytes <= 0 {
		memBytes = 512 * 1024 * 1024
		r.log.WithContext(ctx).Warnf("spec %d memory not provided, fallback to 512Mi", spec.InstanceID)
	}
	resourceList[corev1.ResourceCPU] = *resource.NewMilliQuantity(cpuMilli, resource.DecimalSI)
	resourceList[corev1.ResourceMemory] = *resource.NewQuantity(memBytes, resource.BinarySI)

	var nodeSelector map[string]string
	if spec.GPU > 0 {
		if g, ok := gpuTypeMap[spec.GPU]; ok {
			gpuCount := int64(g.nums)
			resourceList[corev1.ResourceName("nvidia.com/gpu")] = *resource.NewQuantity(gpuCount, resource.DecimalSI)
			if g.name != "" {
				nodeSelector = map[string]string{"accelerator": g.name}
			}
		}
	}

	container := corev1.Container{
		Name:  strconv.Itoa(int(spec.InstanceID)),
		Image: spec.Image,
		Resources: corev1.ResourceRequirements{
			Requests: resourceList,
			Limits:   resourceList,
		},
	}

	podSpec := corev1.PodSpec{
		Containers:    []corev1.Container{container},
		RestartPolicy: corev1.RestartPolicyNever,
	}
	if nodeSelector != nil {
		podSpec.NodeSelector = nodeSelector
	}

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      strconv.Itoa(int(spec.InstanceID)),
			Namespace: "default",
			Labels: map[string]string{
				"instance-id": strconv.Itoa(int(spec.InstanceID)),
				"app":         "instance",
			},
		},
		Spec: podSpec,
	}

	r.log.WithContext(ctx).Infof("creating pod %s in namespace %s", pod.Name, pod.Namespace)
	req := &createRequest{
		pod:    pod,
		option: metav1.CreateOptions{},
	}

	r.requestCh <- req
	return nil
}
