package biz

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/go-kratos/kratos/v2/log"
)

var ErrInstanceAlreadyExists = errors.New("instance already exists")

type ResourceUsecase struct {
	InstanceSpec InstanceRepo
	AuditRepo    AuditRepo
	K8sRepo      K8sRepo
	NetworkRepo  NetworkRepo
	ExecRepo     ExecRepo
	log          *log.Helper
}

type AuditInformation struct {
	InstanceID int64
	LogType    string
	Message    string
	DataJson   json.RawMessage
	CreatedAt  time.Time
}

type AuditRepo interface {
	CreateAudit(ctx context.Context, information AuditInformation) error
}

type InstanceSpec struct {
	InstanceID int64
	UserID     string
	Name       string
	CPU        uint32
	Memory     uint32
	GPU        uint32
	Image      string
	ConfigJSON json.RawMessage
}

type InstanceRepo interface {
	CreateInstance(ctx context.Context, spec InstanceSpec) error
	ListResources(ctx context.Context, filter ListResourcesFilter) ([]Resource, error)
	// GetResource returns a single resource by instance ID
	GetResource(ctx context.Context, instanceID int64) (*Resource, error)
	// ListResourceSpecs returns resource specs keyed by instance ID.
	ListResourceSpecs(ctx context.Context, instanceIDs []int64) (map[int64]InstanceSpec, error)
}

type K8sRepo interface {
	CreateInstance(ctx context.Context, spec InstanceSpec) error

	// CreateServiceForTCPUDP creates a ClusterIP Service for TCP/UDP protocols and patches ingress-nginx ConfigMap
	// Returns: serviceName, externalPort, error
	CreateServiceForTCPUDP(ctx context.Context, namespace, instanceID string, port uint32, protocol string) (string, uint32, error)

	// CreateServiceForHTTP creates a ClusterIP Service for HTTP protocol
	// Returns: serviceName, error
	CreateServiceForHTTP(ctx context.Context, namespace, instanceID string, port uint32) (string, error)

	// DeleteService deletes a Service by name
	DeleteService(ctx context.Context, namespace, serviceName string) error

	// DeleteTCPUDPConfigMapEntry deletes a TCP/UDP ConfigMap entry
	DeleteTCPUDPConfigMapEntry(ctx context.Context, protocol string, externalPort uint32) error

	// CreateIngress creates an Ingress for HTTP access
	// Returns: ingressName, accessURL, error
	CreateIngress(ctx context.Context, namespace, instanceID string, port uint32, serviceName, ingressDomain string) (string, string, error)

	// DeleteIngress deletes an Ingress by name
	DeleteIngress(ctx context.Context, namespace, ingressName string) error

	// GetIngressDomain returns the configured ingress domain
	GetIngressDomain() string

	// GetIngressNginxLBIP returns the ingress-nginx LoadBalancer External IP
	GetIngressNginxLBIP(ctx context.Context) (string, error)
}

// ExecRepo K8s exec 操作接口
type ExecRepo interface {
	// StreamExec 流式执行容器命令
	StreamExec(ctx context.Context, opts ExecOptions, input <-chan ExecInput, output chan<- ExecOutput) error
}

// ExecOptions exec 执行选项
type ExecOptions struct {
	Namespace     string
	InstanceID    string // 实例 ID，用于通过 label 查找 Pod
	ContainerName string
	Command       []string
	TTY           bool
}

// ExecInput exec 输入消息
type ExecInput struct {
	Type ExecInputType
	Data []byte
	Rows uint32
	Cols uint32
}

// ExecInputType exec 输入类型
type ExecInputType int

const (
	ExecInputStdin ExecInputType = iota
	ExecInputResize
)

// ExecOutput exec 输出消息
type ExecOutput struct {
	Type     ExecOutputType
	Stream   string // "stdout" or "stderr"
	Data     []byte
	ExitCode int32
}

// ExecOutputType exec 输出类型
type ExecOutputType int

const (
	ExecOutputData ExecOutputType = iota
	ExecOutputError
	ExecOutputExit
)

// NetworkBinding 网络绑定信息
// 支持两种暴露模式：
//  1. TCP/UDP: 通过 ClusterIP Service + ingress-nginx ConfigMap 暴露，ExternalPort 字段有值
//  2. HTTP: 通过 ClusterIP Service + Ingress 暴露，IngressName 字段有值
type NetworkBinding struct {
	InstanceID   int64
	Port         uint32
	ServiceName  string
	ServicePort  uint32
	ExternalPort *uint32 // TCP/UDP 模式下的外部端口（ConfigMap key）
	IngressName  *string // HTTP 模式下的 Ingress 名称
	Protocol     string  // TCP/UDP/HTTP
	AccessURL    string
	Enabled      bool
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// NetworkRepo 网络配置仓储接口
type NetworkRepo interface {
	CreateNetworkBinding(ctx context.Context, binding NetworkBinding) error
	UpdateNetworkBinding(ctx context.Context, binding NetworkBinding) error
	DeleteNetworkBinding(ctx context.Context, instanceID int64, port uint32) error
	GetNetworkBinding(ctx context.Context, instanceID int64, port uint32) (*NetworkBinding, error)
	ListNetworkBindings(ctx context.Context, instanceID int64) ([]NetworkBinding, error)
	BatchDeleteNetworkBindings(ctx context.Context, instanceID int64) error
}

func NewResourceUsecase(repo InstanceRepo, audit AuditRepo, k8sRepo K8sRepo, networkRepo NetworkRepo, execRepo ExecRepo, logger log.Logger) *ResourceUsecase {
	return &ResourceUsecase{
		InstanceSpec: repo,
		AuditRepo:    audit,
		K8sRepo:      k8sRepo,
		NetworkRepo:  networkRepo,
		ExecRepo:     execRepo,
		log:          log.NewHelper(logger),
	}
}

func (uc *ResourceUsecase) CreateInstance(ctx context.Context, spec InstanceSpec) error {
	uc.log.WithContext(ctx).Infof("CreateInstance: %v", spec)
	err := uc.InstanceSpec.CreateInstance(ctx, spec)
	if err != nil {
		if errors.Is(err, ErrInstanceAlreadyExists) {
			return nil
		}
		return err
	}
	err = uc.K8sRepo.CreateInstance(ctx, spec)
	if err != nil {
		return err
	}
	err = uc.AuditRepo.CreateAudit(ctx, AuditInformation{
		InstanceID: spec.InstanceID,
		LogType:    "CREATE",
		Message:    "Instance created",
		DataJson:   json.RawMessage(`{"cpu":` + strconv.Itoa(int(spec.CPU)) + `,"memory":` + strconv.Itoa(int(spec.Memory)) + `,"GPU":` + strconv.Itoa(int(spec.GPU)) + `}`),
		CreatedAt:  time.Now(),
	})

	if err != nil {
		return err
	}
	return nil
}

// Resource is a read model for listing resources.
type Resource struct {
	InstanceID int64
	Name       string
	UserID     string
	Type       string
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// ListResourcesFilter defines optional filters for listing resources.
type ListResourcesFilter struct {
	UserID *string
	Start  *time.Time
	End    *time.Time
	Type   *string
}

// ListResources returns resources filtered by ListResourcesFilter.
func (uc *ResourceUsecase) ListResources(ctx context.Context, filter ListResourcesFilter) ([]Resource, error) {
	uc.log.WithContext(ctx).Infof("ListResources: userID=%v type=%v start=%v end=%v", filter.UserID, filter.Type, filter.Start, filter.End)
	return uc.InstanceSpec.ListResources(ctx, filter)
}

// ListResourceSpecs returns specs for given instance IDs.
func (uc *ResourceUsecase) ListResourceSpecs(ctx context.Context, instanceIDs []int64) (map[int64]InstanceSpec, error) {
	return uc.InstanceSpec.ListResourceSpecs(ctx, instanceIDs)
}

// GetResource returns a single resource by instance ID.
func (uc *ResourceUsecase) GetResource(ctx context.Context, instanceID int64) (*Resource, error) {
	return uc.InstanceSpec.GetResource(ctx, instanceID)
}

// SetInstancePort sets port exposure for an instance.
// When open=true, creates Service/Ingress and persists configuration.
// When open=false, deletes Service/Ingress and removes configuration.
func (uc *ResourceUsecase) SetInstancePort(ctx context.Context, instanceID int64, port uint32, protocol string, open bool, ingressDomain string) (string, error) {
	uc.log.WithContext(ctx).Infof("SetInstancePort: instanceID=%d port=%d protocol=%s open=%v", instanceID, port, protocol, open)

	// 1. 获取实例信息（验证实例是否存在，并获取 namespace）
	resource, err := uc.InstanceSpec.GetResource(ctx, instanceID)
	if err != nil {
		return "", err
	}
	if resource == nil {
		return "", errors.New("instance not found")
	}

	instanceIDStr := strconv.FormatInt(instanceID, 10)
	namespace := resource.UserID

	if open {
		// 打开端口
		return uc.openPort(ctx, instanceID, instanceIDStr, namespace, port, protocol, ingressDomain)
	} else {
		// 关闭端口
		return "", uc.closePort(ctx, instanceID, namespace, port)
	}
}

// openPort opens a port for an instance.
func (uc *ResourceUsecase) openPort(ctx context.Context, instanceID int64, instanceIDStr, namespace string, port uint32, protocol, ingressDomain string) (string, error) {
	// 检查端口是否已经打开
	existing, err := uc.NetworkRepo.GetNetworkBinding(ctx, instanceID, port)
	if err != nil {
		return "", err
	}
	if existing != nil && existing.Enabled {
		uc.log.WithContext(ctx).Infof("port %d already opened, returning existing URL", port)
		return existing.AccessURL, nil // 幂等：已打开
	}

	var accessURL string
	var serviceName string
	var externalPort *uint32
	var ingressName *string

	// 根据协议选择暴露模式
	switch protocol {
	case "TCP", "UDP":
		// TCP/UDP 模式：创建 ClusterIP Service + patch ingress-nginx ConfigMap
		svcName, allocatedExternalPort, err := uc.K8sRepo.CreateServiceForTCPUDP(ctx, namespace, instanceIDStr, port, protocol)
		if err != nil {
			return "", err
		}
		serviceName = svcName
		externalPort = &allocatedExternalPort

		// 获取 ingress-nginx LoadBalancer IP
		lbIP, err := uc.K8sRepo.GetIngressNginxLBIP(ctx)
		if err != nil {
			uc.log.Warnf("failed to get ingress-nginx LB IP: %v, using placeholder", err)
			lbIP = "<ingress-lb-ip>"
		}
		accessURL = fmt.Sprintf("%s:%d", lbIP, allocatedExternalPort)

	case "HTTP":
		// HTTP 模式：创建 ClusterIP Service + Ingress
		svcName, err := uc.K8sRepo.CreateServiceForHTTP(ctx, namespace, instanceIDStr, port)
		if err != nil {
			return "", err
		}
		serviceName = svcName

		ingName, url, err := uc.K8sRepo.CreateIngress(ctx, namespace, instanceIDStr, port, serviceName, ingressDomain)
		if err != nil {
			// 创建 Ingress 失败，回滚 Service
			_ = uc.K8sRepo.DeleteService(ctx, namespace, serviceName)
			return "", err
		}
		ingressName = &ingName
		accessURL = url

	default:
		return "", errors.New("invalid protocol, must be TCP/UDP/HTTP")
	}

	// 持久化网络配置
	binding := NetworkBinding{
		InstanceID:   instanceID,
		Port:         port,
		ServiceName:  serviceName,
		ServicePort:  port,
		ExternalPort: externalPort,
		IngressName:  ingressName,
		Protocol:     protocol,
		AccessURL:    accessURL,
		Enabled:      true,
	}

	if err := uc.NetworkRepo.CreateNetworkBinding(ctx, binding); err != nil {
		// 持久化失败，回滚 K8s 资源
		_ = uc.K8sRepo.DeleteService(ctx, namespace, serviceName)
		if ingressName != nil {
			_ = uc.K8sRepo.DeleteIngress(ctx, namespace, *ingressName)
		}
		if externalPort != nil {
			_ = uc.K8sRepo.DeleteTCPUDPConfigMapEntry(ctx, protocol, *externalPort)
		}
		return "", err
	}

	// 记录审计日志
	_ = uc.AuditRepo.CreateAudit(ctx, AuditInformation{
		InstanceID: instanceID,
		LogType:    "PORT_OPENED",
		Message:    "Port " + strconv.Itoa(int(port)) + " opened with protocol " + protocol,
		DataJson:   json.RawMessage(`{"port":` + strconv.Itoa(int(port)) + `,"protocol":"` + protocol + `","access_url":"` + accessURL + `"}`),
		CreatedAt:  time.Now(),
	})

	uc.log.WithContext(ctx).Infof("port %d opened successfully, access URL: %s", port, accessURL)
	return accessURL, nil
}

// closePort closes a port for an instance.
func (uc *ResourceUsecase) closePort(ctx context.Context, instanceID int64, namespace string, port uint32) error {
	// 查询网络配置
	binding, err := uc.NetworkRepo.GetNetworkBinding(ctx, instanceID, port)
	if err != nil {
		return err
	}
	if binding == nil {
		uc.log.WithContext(ctx).Infof("port %d not found, already closed", port)
		return nil // 幂等：已关闭
	}

	// 删除 K8s Service
	if err := uc.K8sRepo.DeleteService(ctx, namespace, binding.ServiceName); err != nil {
		uc.log.Errorf("failed to delete service %s: %v", binding.ServiceName, err)
		// 继续删除其他资源
	}

	// 删除 K8s Ingress（如果存在）
	if binding.IngressName != nil {
		if err := uc.K8sRepo.DeleteIngress(ctx, namespace, *binding.IngressName); err != nil {
			uc.log.Errorf("failed to delete ingress %s: %v", *binding.IngressName, err)
			// 继续删除其他资源
		}
	}

	// 删除 TCP/UDP ConfigMap 条目（如果存在）
	if binding.ExternalPort != nil {
		if err := uc.K8sRepo.DeleteTCPUDPConfigMapEntry(ctx, binding.Protocol, *binding.ExternalPort); err != nil {
			uc.log.Errorf("failed to delete ConfigMap entry for port %d: %v", *binding.ExternalPort, err)
			// 继续删除数据库记录
		}
	}

	// 删除数据库记录
	if err := uc.NetworkRepo.DeleteNetworkBinding(ctx, instanceID, port); err != nil {
		return err
	}

	// 记录审计日志
	_ = uc.AuditRepo.CreateAudit(ctx, AuditInformation{
		InstanceID: instanceID,
		LogType:    "PORT_CLOSED",
		Message:    "Port " + strconv.Itoa(int(port)) + " closed",
		DataJson:   json.RawMessage(`{"port":` + strconv.Itoa(int(port)) + `}`),
		CreatedAt:  time.Now(),
	})

	uc.log.WithContext(ctx).Infof("port %d closed successfully", port)
	return nil
}

// StreamExec 流式执行容器命令
func (uc *ResourceUsecase) StreamExec(ctx context.Context, namespace string, instanceID int64, command []string, tty bool, containerName string, input <-chan ExecInput, output chan<- ExecOutput) error {
	if namespace == "" {
		return errors.New("namespace is required")
	}

	// 构建 exec 选项
	instanceIDStr := strconv.FormatInt(instanceID, 10)

	// 如果未指定容器名称，使用实例 ID（与 Deployment 定义一致）
	if containerName == "" {
		containerName = instanceIDStr
	}

	opts := ExecOptions{
		Namespace:     namespace,
		InstanceID:    instanceIDStr,
		ContainerName: containerName,
		Command:       command,
		TTY:           tty,
	}

	// 调用 data 层执行
	return uc.ExecRepo.StreamExec(ctx, opts, input, output)
}
