package data

import (
	"context"
	"time"

	"resource/internal/biz"

	"github.com/go-kratos/kratos/v2/log"
	"gorm.io/gorm"
)

// networkRepo 提供实例网络配置的数据访问能力
type networkRepo struct {
	data *Data
	log  *log.Helper
}

// NewNetworkRepo 创建网络配置仓储实现
func NewNetworkRepo(data *Data, logger log.Logger) biz.NetworkRepo {
	return &networkRepo{
		data: data,
		log:  log.NewHelper(logger),
	}
}

// instanceNetwork 实例网络配置表，记录端口暴露信息
// 支持两种暴露模式：
//  1. TCP/UDP: 通过 ClusterIP Service + ingress-nginx ConfigMap 暴露，使用 ExternalPort
//  2. HTTP: 通过 ClusterIP Service + Ingress 暴露，使用 IngressName
type instanceNetwork struct {
	InstanceID   int64     `gorm:"primaryKey;column:instance_id;not null"` // 实例ID
	Port         uint32    `gorm:"primaryKey;column:port;not null"`        // 容器端口 (targetPort)
	ServiceName  string    `gorm:"column:service_name;size:64;not null"`   // K8s Service 名称
	ServicePort  uint32    `gorm:"column:service_port;not null"`           // Service 暴露的端口
	ExternalPort *uint32   `gorm:"column:external_port"`                   // TCP/UDP 模式的外部端口（ConfigMap key）
	IngressName  *string   `gorm:"column:ingress_name;size:64"`            // HTTP 模式的 Ingress 名称
	Protocol     string    `gorm:"column:protocol;default:'HTTP'"`         // TCP/UDP/HTTP
	AccessURL    string    `gorm:"column:access_url;not null"`             // 最终访问地址
	Enabled      bool      `gorm:"column:enabled;default:true"`            // 是否启用
	CreatedAt    time.Time `gorm:"column:created_at;autoCreateTime"`
	UpdatedAt    time.Time `gorm:"column:updated_at;autoUpdateTime"`
}

func (instanceNetwork) TableName() string { return "instance_network" }

// CreateNetworkBinding 创建端口绑定记录
func (r *networkRepo) CreateNetworkBinding(ctx context.Context, binding biz.NetworkBinding) error {
	network := &instanceNetwork{
		InstanceID:   binding.InstanceID,
		Port:         binding.Port,
		ServiceName:  binding.ServiceName,
		ServicePort:  binding.ServicePort,
		ExternalPort: binding.ExternalPort,
		IngressName:  binding.IngressName,
		Protocol:     binding.Protocol,
		AccessURL:    binding.AccessURL,
		Enabled:      binding.Enabled,
	}

	if err := r.data.db.WithContext(ctx).Create(network).Error; err != nil {
		r.log.Errorf("failed to create network binding: %v", err)
		return err
	}

	return nil
}

// UpdateNetworkBinding 更新端口绑定记录
func (r *networkRepo) UpdateNetworkBinding(ctx context.Context, binding biz.NetworkBinding) error {
	updates := map[string]interface{}{
		"service_name":  binding.ServiceName,
		"service_port":  binding.ServicePort,
		"external_port": binding.ExternalPort,
		"ingress_name":  binding.IngressName,
		"protocol":      binding.Protocol,
		"access_url":    binding.AccessURL,
		"enabled":       binding.Enabled,
		"updated_at":    time.Now(),
	}

	result := r.data.db.WithContext(ctx).
		Model(&instanceNetwork{}).
		Where("instance_id = ? AND port = ?", binding.InstanceID, binding.Port).
		Updates(updates)

	if result.Error != nil {
		r.log.Errorf("failed to update network binding: %v", result.Error)
		return result.Error
	}

	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}

	return nil
}

// DeleteNetworkBinding 删除端口绑定记录（物理删除）
func (r *networkRepo) DeleteNetworkBinding(ctx context.Context, instanceID int64, port uint32) error {
	result := r.data.db.WithContext(ctx).
		Where("instance_id = ? AND port = ?", instanceID, port).
		Delete(&instanceNetwork{})

	if result.Error != nil {
		r.log.Errorf("failed to delete network binding: %v", result.Error)
		return result.Error
	}

	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}

	return nil
}

// GetNetworkBinding 获取单个端口绑定记录
func (r *networkRepo) GetNetworkBinding(ctx context.Context, instanceID int64, port uint32) (*biz.NetworkBinding, error) {
	var network instanceNetwork
	err := r.data.db.WithContext(ctx).
		Where("instance_id = ? AND port = ?", instanceID, port).
		First(&network).Error

	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		r.log.Errorf("failed to get network binding: %v", err)
		return nil, err
	}

	return &biz.NetworkBinding{
		InstanceID:   network.InstanceID,
		Port:         network.Port,
		ServiceName:  network.ServiceName,
		ServicePort:  network.ServicePort,
		ExternalPort: network.ExternalPort,
		IngressName:  network.IngressName,
		Protocol:     network.Protocol,
		AccessURL:    network.AccessURL,
		Enabled:      network.Enabled,
		CreatedAt:    network.CreatedAt,
		UpdatedAt:    network.UpdatedAt,
	}, nil
}

// ListNetworkBindings 列出实例的所有端口绑定
func (r *networkRepo) ListNetworkBindings(ctx context.Context, instanceID int64) ([]biz.NetworkBinding, error) {
	var networks []instanceNetwork
	err := r.data.db.WithContext(ctx).
		Where("instance_id = ?", instanceID).
		Order("port ASC").
		Find(&networks).Error

	if err != nil {
		r.log.Errorf("failed to list network bindings: %v", err)
		return nil, err
	}

	bindings := make([]biz.NetworkBinding, 0, len(networks))
	for _, network := range networks {
		bindings = append(bindings, biz.NetworkBinding{
			InstanceID:   network.InstanceID,
			Port:         network.Port,
			ServiceName:  network.ServiceName,
			ServicePort:  network.ServicePort,
			ExternalPort: network.ExternalPort,
			IngressName:  network.IngressName,
			Protocol:     network.Protocol,
			AccessURL:    network.AccessURL,
			Enabled:      network.Enabled,
			CreatedAt:    network.CreatedAt,
			UpdatedAt:    network.UpdatedAt,
		})
	}

	return bindings, nil
}

// BatchDeleteNetworkBindings 批量删除实例的所有端口绑定
func (r *networkRepo) BatchDeleteNetworkBindings(ctx context.Context, instanceID int64) error {
	result := r.data.db.WithContext(ctx).
		Where("instance_id = ?", instanceID).
		Delete(&instanceNetwork{})

	if result.Error != nil {
		r.log.Errorf("failed to batch delete network bindings: %v", result.Error)
		return result.Error
	}

	return nil
}
