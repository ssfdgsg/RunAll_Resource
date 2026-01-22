package data

import (
	"context"
	"errors"
	"fmt"
	"resource/internal/biz"
	"resource/internal/conf"
	"strconv"

	"github.com/go-kratos/kratos/v2/log"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
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

type k8sRepo struct {
	client        kubernetes.Interface
	config        *rest.Config
	log           *log.Helper
	ingressDomain string
	nodeIP        string
}

// NewK8sRepo bootstraps a Kubernetes repo with a shared kubeconfig.
func NewK8sRepo(c *conf.Data, k8sClient *K8sClient, logger log.Logger) (biz.K8sRepo, error) {
	helper := log.NewHelper(logger)

	// 获取配置
	var ingressDomain, nodeIP string
	if c.GetKubernetes() != nil {
		ingressDomain = c.GetKubernetes().GetIngressDomain()
		nodeIP = c.GetKubernetes().GetNodeIp()
	}

	// 如果未配置，尝试自动获取（适用于 minikube 等本地环境）
	if nodeIP == "" {
		var err error
		nodeIP, err = getMinikubeIP(k8sClient.Client)
		if err != nil {
			helper.Warnf("failed to auto-detect node IP: %v, using localhost", err)
			nodeIP = "127.0.0.1"
		} else {
			helper.Infof("auto-detected node IP: %s", nodeIP)
		}
	}

	// 如果未配置 ingress domain，使用 nip.io 魔法域名
	if ingressDomain == "" && nodeIP != "" {
		ingressDomain = fmt.Sprintf("%s.nip.io", nodeIP)
		helper.Infof("using nip.io domain: %s", ingressDomain)
	}

	return &k8sRepo{
		client:        k8sClient.Client,
		config:        k8sClient.Config,
		log:           helper,
		ingressDomain: ingressDomain,
		nodeIP:        nodeIP,
	}, nil
}

// getMinikubeIP attempts to get the first node's external or internal IP.
func getMinikubeIP(client kubernetes.Interface) (string, error) {
	nodes, err := client.CoreV1().Nodes().List(context.Background(), metav1.ListOptions{})
	if err != nil {
		return "", err
	}
	if len(nodes.Items) == 0 {
		return "", errors.New("no nodes found")
	}

	// 尝试获取第一个节点的 IP
	node := nodes.Items[0]
	for _, addr := range node.Status.Addresses {
		// 优先使用 ExternalIP，其次 InternalIP
		if addr.Type == corev1.NodeExternalIP && addr.Address != "" {
			return addr.Address, nil
		}
	}
	for _, addr := range node.Status.Addresses {
		if addr.Type == corev1.NodeInternalIP && addr.Address != "" {
			return addr.Address, nil
		}
	}

	return "", errors.New("no valid IP address found for node")
}

// ensureNamespace 确保指定的 namespace 存在，如果不存在则创建
func (r *k8sRepo) ensureNamespace(ctx context.Context, namespace string) error {
	_, err := r.client.CoreV1().Namespaces().Get(ctx, namespace, metav1.GetOptions{})
	if err == nil {
		return nil
	}

	// 使用标准 K8s 错误判断
	if !k8serrors.IsNotFound(err) {
		return err
	}

	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: namespace,
			Labels: map[string]string{
				"user-namespace": "true",
				"managed-by":     "resource-service",
			},
		},
	}

	_, err = r.client.CoreV1().Namespaces().Create(ctx, ns, metav1.CreateOptions{})
	if err != nil && !k8serrors.IsAlreadyExists(err) {
		return err
	}

	r.log.Infof("created namespace %s", namespace)
	return nil
}

func (r *k8sRepo) CreateInstance(ctx context.Context, spec biz.InstanceSpec) error {
	if r.client == nil {
		return fmt.Errorf("k8s client is not initialized")
	}

	// 确保 namespace 存在
	if err := r.ensureNamespace(ctx, spec.UserID); err != nil {
		return fmt.Errorf("failed to ensure namespace %s: %w", spec.UserID, err)
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

	nameStr := strconv.Itoa(int(spec.InstanceID))
	labels := map[string]string{
		"instance-id": nameStr,
		"app":         "instance",
		"user-id":     spec.UserID,
	}

	// 使用 Deployment 替代 Pod，提高稳定性
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      nameStr,
			Namespace: spec.UserID,
			Labels:    labels,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: int32Ptr(1),
			Selector: &metav1.LabelSelector{
				MatchLabels: labels,
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: labels,
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  nameStr,
							Image: spec.Image,
							// 添加启动命令，保持容器运行
							Command: []string{"/bin/sh"},
							Args:    []string{"-c", "trap 'exit 0' TERM; sleep infinity & wait"},
							Resources: corev1.ResourceRequirements{
								Requests: resourceList,
								Limits:   resourceList,
							},
						},
					},
					NodeSelector: nodeSelector,
				},
			},
		},
	}

	r.log.WithContext(ctx).Infof("creating deployment %s in namespace %s", nameStr, spec.UserID)

	// 同步创建，直接返回错误给上层
	_, err := r.client.AppsV1().Deployments(spec.UserID).Create(ctx, deployment, metav1.CreateOptions{})
	if err != nil {
		r.log.Errorf("failed to create deployment %s: %v", nameStr, err)
		return fmt.Errorf("k8s create failed: %w", err)
	}

	return nil
}

// CreateServiceForTCPUDP creates a NodePort Service for TCP/UDP protocols.
// Returns the service name and the allocated NodePort.
func (r *k8sRepo) CreateServiceForTCPUDP(ctx context.Context, namespace, instanceID string, port uint32, protocol string) (string, uint32, error) {
	serviceName := generateServiceName(instanceID, port)

	r.log.WithContext(ctx).Infof("creating NodePort service %s in namespace %s for port %d with protocol %s", serviceName, namespace, port, protocol)

	// 验证协议
	var k8sProtocol corev1.Protocol
	switch protocol {
	case "TCP":
		k8sProtocol = corev1.ProtocolTCP
	case "UDP":
		k8sProtocol = corev1.ProtocolUDP
	default:
		return "", 0, fmt.Errorf("invalid protocol %s, must be TCP or UDP", protocol)
	}

	// 构建 Service 对象
	service := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      serviceName,
			Namespace: namespace,
			Labels: map[string]string{
				"instance-id": instanceID,
				"managed-by":  "resource-service",
			},
		},
		Spec: corev1.ServiceSpec{
			Type:     corev1.ServiceTypeNodePort,
			Selector: getPodSelector(instanceID),
			Ports: []corev1.ServicePort{
				{
					Name:       fmt.Sprintf("port-%d", port),
					Protocol:   k8sProtocol,
					Port:       int32(port),
					TargetPort: intstr.FromInt32(int32(port)),
					// NodePort 不指定，让 K8s 自动分配
				},
			},
		},
	}

	// 调用 K8s API 创建 Service
	createdService, err := r.client.CoreV1().Services(namespace).Create(ctx, service, metav1.CreateOptions{})
	if err != nil {
		r.log.Errorf("failed to create NodePort service %s: %v", serviceName, err)
		return "", 0, fmt.Errorf("failed to create service: %w", err)
	}

	// 获取 K8s 分配的 NodePort
	if len(createdService.Spec.Ports) == 0 {
		return "", 0, fmt.Errorf("service created but no ports found")
	}

	allocatedNodePort := createdService.Spec.Ports[0].NodePort
	if allocatedNodePort == 0 {
		return "", 0, fmt.Errorf("service created but NodePort not allocated")
	}

	r.log.WithContext(ctx).Infof("NodePort service %s created successfully, allocated NodePort: %d", serviceName, allocatedNodePort)

	return serviceName, uint32(allocatedNodePort), nil
}

// CreateServiceForHTTP creates a ClusterIP Service for HTTP protocol.
// Returns the service name.
func (r *k8sRepo) CreateServiceForHTTP(ctx context.Context, namespace, instanceID string, port uint32) (string, error) {
	serviceName := generateServiceName(instanceID, port)

	r.log.WithContext(ctx).Infof("creating ClusterIP service %s in namespace %s for port %d", serviceName, namespace, port)

	// 构建 Service 对象
	service := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      serviceName,
			Namespace: namespace,
			Labels: map[string]string{
				"instance-id": instanceID,
				"managed-by":  "resource-service",
			},
		},
		Spec: corev1.ServiceSpec{
			Type:     corev1.ServiceTypeClusterIP,
			Selector: getPodSelector(instanceID),
			Ports: []corev1.ServicePort{
				{
					Name:       fmt.Sprintf("port-%d", port),
					Protocol:   corev1.ProtocolTCP,
					Port:       int32(port),
					TargetPort: intstr.FromInt32(int32(port)),
				},
			},
		},
	}

	// 调用 K8s API 创建 Service
	_, err := r.client.CoreV1().Services(namespace).Create(ctx, service, metav1.CreateOptions{})
	if err != nil {
		r.log.Errorf("failed to create ClusterIP service %s: %v", serviceName, err)
		return "", fmt.Errorf("failed to create service: %w", err)
	}

	r.log.WithContext(ctx).Infof("ClusterIP service %s created successfully", serviceName)

	return serviceName, nil
}

// DeleteService deletes a Service by name.
// Returns nil if the service doesn't exist (idempotent).
func (r *k8sRepo) DeleteService(ctx context.Context, namespace, serviceName string) error {
	r.log.WithContext(ctx).Infof("deleting service %s in namespace %s", serviceName, namespace)

	err := r.client.CoreV1().Services(namespace).Delete(ctx, serviceName, metav1.DeleteOptions{})
	if err != nil {
		// 使用标准 K8s 错误判断
		if k8serrors.IsNotFound(err) {
			r.log.WithContext(ctx).Infof("service %s not found, already deleted", serviceName)
			return nil
		}
		r.log.Errorf("failed to delete service %s: %v", serviceName, err)
		return fmt.Errorf("failed to delete service: %w", err)
	}

	r.log.WithContext(ctx).Infof("service %s deleted successfully", serviceName)
	return nil
}

// ============================================================================
// Ingress 操作
// ============================================================================

// CreateIngress creates an Ingress for HTTP access.
// Returns the ingress name and access URL.
func (r *k8sRepo) CreateIngress(ctx context.Context, namespace, instanceID string, port uint32, serviceName, ingressDomain string) (string, string, error) {
	ingressName := generateIngressName(instanceID, port)
	accessURL := generateAccessURL(ingressDomain, namespace, instanceID, port)

	r.log.WithContext(ctx).Infof("creating ingress %s in namespace %s for service %s", ingressName, namespace, serviceName)

	// 构建 Path
	pathType := networkingv1.PathTypePrefix
	path := fmt.Sprintf("/%s/%s/%d", namespace, instanceID, port)

	// 构建 Ingress 对象
	ingress := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ingressName,
			Namespace: namespace,
			Labels: map[string]string{
				"instance-id": instanceID,
				"managed-by":  "resource-service",
			},
			Annotations: map[string]string{
				"nginx.ingress.kubernetes.io/rewrite-target": "/",
			},
		},
		Spec: networkingv1.IngressSpec{
			IngressClassName: stringPtr("nginx"),
			Rules: []networkingv1.IngressRule{
				{
					Host: ingressDomain,
					IngressRuleValue: networkingv1.IngressRuleValue{
						HTTP: &networkingv1.HTTPIngressRuleValue{
							Paths: []networkingv1.HTTPIngressPath{
								{
									Path:     path,
									PathType: &pathType,
									Backend: networkingv1.IngressBackend{
										Service: &networkingv1.IngressServiceBackend{
											Name: serviceName,
											Port: networkingv1.ServiceBackendPort{
												Number: int32(port),
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	// 调用 K8s API 创建 Ingress
	_, err := r.client.NetworkingV1().Ingresses(namespace).Create(ctx, ingress, metav1.CreateOptions{})
	if err != nil {
		r.log.Errorf("failed to create ingress %s: %v", ingressName, err)
		return "", "", fmt.Errorf("failed to create ingress: %w", err)
	}

	r.log.WithContext(ctx).Infof("ingress %s created successfully, access URL: %s", ingressName, accessURL)

	return ingressName, accessURL, nil
}

// DeleteIngress deletes an Ingress by name.
// Returns nil if the ingress doesn't exist (idempotent).
func (r *k8sRepo) DeleteIngress(ctx context.Context, namespace, ingressName string) error {
	r.log.WithContext(ctx).Infof("deleting ingress %s in namespace %s", ingressName, namespace)

	err := r.client.NetworkingV1().Ingresses(namespace).Delete(ctx, ingressName, metav1.DeleteOptions{})
	if err != nil {
		// 使用标准 K8s 错误判断
		if k8serrors.IsNotFound(err) {
			r.log.WithContext(ctx).Infof("ingress %s not found, already deleted", ingressName)
			return nil
		}
		r.log.Errorf("failed to delete ingress %s: %v", ingressName, err)
		return fmt.Errorf("failed to delete ingress: %w", err)
	}

	r.log.WithContext(ctx).Infof("ingress %s deleted successfully", ingressName)
	return nil
}

// ============================================================================
// 辅助函数
// ============================================================================

// generateServiceName generates a Service name based on instance ID and port.
// Format: instance-{instanceID}-{port}
func generateServiceName(instanceID string, port uint32) string {
	return fmt.Sprintf("instance-%s-%d", instanceID, port)
}

// generateIngressName generates an Ingress name based on instance ID and port.
// Format: ingress-{instanceID}-{port}
func generateIngressName(instanceID string, port uint32) string {
	return fmt.Sprintf("ingress-%s-%d", instanceID, port)
}

// generateAccessURL generates the access URL for HTTP Ingress.
// Format: http(s)://{domain}/{namespace}/{instanceID}/{port}
func generateAccessURL(ingressDomain, namespace, instanceID string, port uint32) string {
	return fmt.Sprintf("http://%s/%s/%s/%d", ingressDomain, namespace, instanceID, port)
}

// generateTCPUDPAccessURL generates the access URL for TCP/UDP NodePort.
// Format: {nodeIP}:{nodePort}
func generateTCPUDPAccessURL(nodeIP string, nodePort uint32) string {
	return fmt.Sprintf("%s:%d", nodeIP, nodePort)
}

// getPodSelector returns the label selector for matching Pods.
func getPodSelector(instanceID string) map[string]string {
	return map[string]string{
		"instance-id": instanceID,
		"app":         "instance",
	}
}

// GetIngressDomain returns the configured ingress domain.
func (r *k8sRepo) GetIngressDomain() string {
	return r.ingressDomain
}

// GetNodeIP returns the configured node IP.
func (r *k8sRepo) GetNodeIP() string {
	return r.nodeIP
}

// stringPtr returns a pointer to the string value.
func stringPtr(s string) *string {
	return &s
}

// int32Ptr returns a pointer to the int32 value.
func int32Ptr(i int32) *int32 {
	return &i
}
