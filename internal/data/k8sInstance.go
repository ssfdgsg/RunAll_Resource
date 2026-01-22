package data

import (
	"context"
	"fmt"
	"resource/internal/biz"
	"resource/internal/conf"
	"strconv"
	"strings"

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
	client                kubernetes.Interface
	config                *rest.Config
	log                   *log.Helper
	ingressDomain         string
	ingressNginxNamespace string
	ingressNginxLBService string
	tcpUDPPortRangeStart  uint32
	tcpUDPPortRangeEnd    uint32
	nextAvailablePort     uint32 // 简单的端口分配计数器（生产环境需要更复杂的端口池管理）
}

// NewK8sRepo bootstraps a Kubernetes repo with a shared kubeconfig.
func NewK8sRepo(c *conf.Data, k8sClient *K8sClient, logger log.Logger) (biz.K8sRepo, error) {
	helper := log.NewHelper(logger)

	// 获取配置
	var ingressDomain, ingressNginxNamespace, ingressNginxLBService string
	var tcpUDPPortRangeStart, tcpUDPPortRangeEnd uint32

	if c.GetKubernetes() != nil {
		k8sConf := c.GetKubernetes()
		ingressDomain = k8sConf.GetIngressDomain()
		ingressNginxNamespace = k8sConf.GetIngressNginxNamespace()
		ingressNginxLBService = k8sConf.GetIngressNginxLbService()
		tcpUDPPortRangeStart = k8sConf.GetTcpUdpPortRangeStart()
		tcpUDPPortRangeEnd = k8sConf.GetTcpUdpPortRangeEnd()
	}

	// 设置默认值
	if ingressDomain == "" {
		ingressDomain = "demo.localtest.me"
		helper.Infof("using default ingress domain: %s", ingressDomain)
	}
	if ingressNginxNamespace == "" {
		ingressNginxNamespace = "ingress-nginx"
	}
	if ingressNginxLBService == "" {
		ingressNginxLBService = "ingress-nginx-controller"
	}
	if tcpUDPPortRangeStart == 0 {
		tcpUDPPortRangeStart = 30000
	}
	if tcpUDPPortRangeEnd == 0 {
		tcpUDPPortRangeEnd = 32767
	}

	return &k8sRepo{
		client:                k8sClient.Client,
		config:                k8sClient.Config,
		log:                   helper,
		ingressDomain:         ingressDomain,
		ingressNginxNamespace: ingressNginxNamespace,
		ingressNginxLBService: ingressNginxLBService,
		tcpUDPPortRangeStart:  tcpUDPPortRangeStart,
		tcpUDPPortRangeEnd:    tcpUDPPortRangeEnd,
		nextAvailablePort:     tcpUDPPortRangeStart, // 初始化端口计数器
	}, nil
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

// CreateServiceForTCPUDP creates a ClusterIP Service for TCP/UDP protocols and patches ingress-nginx ConfigMap.
// Returns the service name and the allocated external port.
func (r *k8sRepo) CreateServiceForTCPUDP(ctx context.Context, namespace, instanceID string, port uint32, protocol string) (string, uint32, error) {
	serviceName := generateServiceName(instanceID, port)

	r.log.WithContext(ctx).Infof("creating ClusterIP service %s in namespace %s for port %d with protocol %s", serviceName, namespace, port, protocol)

	// 验证协议
	var k8sProtocol corev1.Protocol
	var configMapName string
	switch protocol {
	case "TCP":
		k8sProtocol = corev1.ProtocolTCP
		configMapName = "tcp-services"
	case "UDP":
		k8sProtocol = corev1.ProtocolUDP
		configMapName = "udp-services"
	default:
		return "", 0, fmt.Errorf("invalid protocol %s, must be TCP or UDP", protocol)
	}

	// 1. 创建 ClusterIP Service
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
					Protocol:   k8sProtocol,
					Port:       int32(port),
					TargetPort: intstr.FromInt32(int32(port)),
				},
			},
		},
	}

	_, err := r.client.CoreV1().Services(namespace).Create(ctx, service, metav1.CreateOptions{})
	if err != nil {
		r.log.Errorf("failed to create ClusterIP service %s: %v", serviceName, err)
		return "", 0, fmt.Errorf("failed to create service: %w", err)
	}

	r.log.WithContext(ctx).Infof("ClusterIP service %s created successfully", serviceName)

	// 2. 分配外部端口（简单实现：自增）
	externalPort := r.allocateExternalPort()
	if externalPort == 0 {
		// 端口池耗尽，回滚 Service
		_ = r.client.CoreV1().Services(namespace).Delete(ctx, serviceName, metav1.DeleteOptions{})
		return "", 0, fmt.Errorf("external port pool exhausted")
	}

	// 3. Patch ingress-nginx ConfigMap
	configMapKey := fmt.Sprintf("%d", externalPort)
	configMapValue := fmt.Sprintf("%s/%s:%d", namespace, serviceName, port)

	err = r.patchIngressNginxConfigMap(ctx, configMapName, configMapKey, configMapValue)
	if err != nil {
		// ConfigMap patch 失败，回滚 Service
		_ = r.client.CoreV1().Services(namespace).Delete(ctx, serviceName, metav1.DeleteOptions{})
		return "", 0, fmt.Errorf("failed to patch ConfigMap: %w", err)
	}

	// 4. Patch ingress-nginx Service 添加端口
	err = r.patchIngressNginxServicePort(ctx, externalPort, protocol)
	if err != nil {
		// Service patch 失败，回滚 ConfigMap 和 Service
		_ = r.DeleteTCPUDPConfigMapEntry(ctx, protocol, externalPort)
		_ = r.client.CoreV1().Services(namespace).Delete(ctx, serviceName, metav1.DeleteOptions{})
		return "", 0, fmt.Errorf("failed to patch ingress-nginx Service: %w", err)
	}

	r.log.WithContext(ctx).Infof("TCP/UDP service %s exposed on external port %d", serviceName, externalPort)

	return serviceName, externalPort, nil
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

// GetIngressNginxLBIP returns the ingress-nginx LoadBalancer External IP.
func (r *k8sRepo) GetIngressNginxLBIP(ctx context.Context) (string, error) {
	svc, err := r.client.CoreV1().Services(r.ingressNginxNamespace).Get(ctx, r.ingressNginxLBService, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("failed to get ingress-nginx service: %w", err)
	}

	// 获取 LoadBalancer 的 External IP
	if len(svc.Status.LoadBalancer.Ingress) > 0 {
		if svc.Status.LoadBalancer.Ingress[0].IP != "" {
			return svc.Status.LoadBalancer.Ingress[0].IP, nil
		}
		if svc.Status.LoadBalancer.Ingress[0].Hostname != "" {
			return svc.Status.LoadBalancer.Ingress[0].Hostname, nil
		}
	}

	return "", fmt.Errorf("ingress-nginx LoadBalancer has no external IP")
}

// allocateExternalPort allocates an external port from the port pool.
// 简单实现：自增分配，不考虑回收和重用。
func (r *k8sRepo) allocateExternalPort() uint32 {
	if r.nextAvailablePort > r.tcpUDPPortRangeEnd {
		r.log.Errorf("external port pool exhausted: %d > %d", r.nextAvailablePort, r.tcpUDPPortRangeEnd)
		return 0
	}
	port := r.nextAvailablePort
	r.nextAvailablePort++
	return port
}

// patchIngressNginxConfigMap patches the ingress-nginx tcp-services or udp-services ConfigMap.
func (r *k8sRepo) patchIngressNginxConfigMap(ctx context.Context, configMapName, key, value string) error {
	r.log.WithContext(ctx).Infof("patching ConfigMap %s/%s: %s=%s", r.ingressNginxNamespace, configMapName, key, value)

	// 获取 ConfigMap
	cm, err := r.client.CoreV1().ConfigMaps(r.ingressNginxNamespace).Get(ctx, configMapName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get ConfigMap %s: %w", configMapName, err)
	}

	// 初始化 Data 字段（如果为空）
	if cm.Data == nil {
		cm.Data = make(map[string]string)
	}

	// 添加或更新条目
	cm.Data[key] = value

	// 更新 ConfigMap
	_, err = r.client.CoreV1().ConfigMaps(r.ingressNginxNamespace).Update(ctx, cm, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to update ConfigMap %s: %w", configMapName, err)
	}

	r.log.WithContext(ctx).Infof("ConfigMap %s/%s patched successfully", r.ingressNginxNamespace, configMapName)
	return nil
}

// patchIngressNginxServicePort patches the ingress-nginx Service to add a TCP/UDP port.
func (r *k8sRepo) patchIngressNginxServicePort(ctx context.Context, externalPort uint32, protocol string) error {
	r.log.WithContext(ctx).Infof("patching ingress-nginx Service to add port %d (%s)", externalPort, protocol)

	// 获取 ingress-nginx Service
	svc, err := r.client.CoreV1().Services(r.ingressNginxNamespace).Get(ctx, r.ingressNginxLBService, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get ingress-nginx Service: %w", err)
	}

	// 检查端口是否已存在
	portName := fmt.Sprintf("%s-%d", strings.ToLower(protocol), externalPort)
	for _, p := range svc.Spec.Ports {
		if p.Port == int32(externalPort) && string(p.Protocol) == protocol {
			r.log.WithContext(ctx).Infof("port %d already exists in ingress-nginx Service", externalPort)
			return nil
		}
	}

	// 添加新端口
	var k8sProtocol corev1.Protocol
	if protocol == "TCP" {
		k8sProtocol = corev1.ProtocolTCP
	} else {
		k8sProtocol = corev1.ProtocolUDP
	}

	newPort := corev1.ServicePort{
		Name:       portName,
		Protocol:   k8sProtocol,
		Port:       int32(externalPort),
		TargetPort: intstr.FromInt32(int32(externalPort)),
	}

	svc.Spec.Ports = append(svc.Spec.Ports, newPort)

	// 更新 Service
	_, err = r.client.CoreV1().Services(r.ingressNginxNamespace).Update(ctx, svc, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to update ingress-nginx Service: %w", err)
	}

	r.log.WithContext(ctx).Infof("ingress-nginx Service patched successfully, added port %d", externalPort)
	return nil
}

// DeleteTCPUDPConfigMapEntry deletes a TCP/UDP ConfigMap entry and removes the port from ingress-nginx Service.
func (r *k8sRepo) DeleteTCPUDPConfigMapEntry(ctx context.Context, protocol string, externalPort uint32) error {
	var configMapName string
	switch protocol {
	case "TCP":
		configMapName = "tcp-services"
	case "UDP":
		configMapName = "udp-services"
	default:
		return fmt.Errorf("invalid protocol %s, must be TCP or UDP", protocol)
	}

	r.log.WithContext(ctx).Infof("deleting ConfigMap entry %s/%s: %d", r.ingressNginxNamespace, configMapName, externalPort)

	// 1. 删除 ConfigMap 条目
	cm, err := r.client.CoreV1().ConfigMaps(r.ingressNginxNamespace).Get(ctx, configMapName, metav1.GetOptions{})
	if err != nil {
		if k8serrors.IsNotFound(err) {
			r.log.WithContext(ctx).Infof("ConfigMap %s not found, already deleted", configMapName)
			return nil
		}
		return fmt.Errorf("failed to get ConfigMap %s: %w", configMapName, err)
	}

	// 删除条目
	if cm.Data != nil {
		key := fmt.Sprintf("%d", externalPort)
		delete(cm.Data, key)

		// 更新 ConfigMap
		_, err = r.client.CoreV1().ConfigMaps(r.ingressNginxNamespace).Update(ctx, cm, metav1.UpdateOptions{})
		if err != nil {
			return fmt.Errorf("failed to update ConfigMap %s: %w", configMapName, err)
		}

		r.log.WithContext(ctx).Infof("ConfigMap entry deleted successfully")
	}

	// 2. 从 ingress-nginx Service 删除端口
	err = r.removeIngressNginxServicePort(ctx, externalPort, protocol)
	if err != nil {
		r.log.Warnf("failed to remove port from ingress-nginx Service: %v", err)
		// 不返回错误，因为 ConfigMap 已经删除了
	}

	return nil
}

// removeIngressNginxServicePort removes a TCP/UDP port from the ingress-nginx Service.
func (r *k8sRepo) removeIngressNginxServicePort(ctx context.Context, externalPort uint32, protocol string) error {
	r.log.WithContext(ctx).Infof("removing port %d (%s) from ingress-nginx Service", externalPort, protocol)

	// 获取 ingress-nginx Service
	svc, err := r.client.CoreV1().Services(r.ingressNginxNamespace).Get(ctx, r.ingressNginxLBService, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get ingress-nginx Service: %w", err)
	}

	// 查找并删除端口
	newPorts := []corev1.ServicePort{}
	found := false
	for _, p := range svc.Spec.Ports {
		if p.Port == int32(externalPort) && string(p.Protocol) == protocol {
			found = true
			r.log.WithContext(ctx).Infof("found port %d to remove", externalPort)
			continue // 跳过这个端口，不添加到 newPorts
		}
		newPorts = append(newPorts, p)
	}

	if !found {
		r.log.WithContext(ctx).Infof("port %d not found in ingress-nginx Service", externalPort)
		return nil
	}

	// 更新 Service
	svc.Spec.Ports = newPorts
	_, err = r.client.CoreV1().Services(r.ingressNginxNamespace).Update(ctx, svc, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to update ingress-nginx Service: %w", err)
	}

	r.log.WithContext(ctx).Infof("port %d removed from ingress-nginx Service successfully", externalPort)
	return nil
}

// stringPtr returns a pointer to the string value.
func stringPtr(s string) *string {
	return &s
}

// int32Ptr returns a pointer to the int32 value.
func int32Ptr(i int32) *int32 {
	return &i
}
