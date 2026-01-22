package data

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"resource/internal/biz"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/jackc/pgx/v5/pgconn"
	"gorm.io/gorm"
)

// resourceRepo 按照聚合根 Instance 提供数据访问能力，后续可在这里填充
// PostgreSQL 相关的读写逻辑（使用 r.data.db）。
type resourceRepo struct {
	data *Data
	log  *log.Helper
}

// NewResourceRepo 暴露 biz.InstanceRepo 实现。
// 这里只是骨架，暂未校验或初始化具体连接。
func NewResourceRepo(data *Data, logger log.Logger) biz.InstanceRepo {
	return &resourceRepo{
		data: data,
		log:  log.NewHelper(logger),
	}
}

type instance struct {
	InstanceID int64      `gorm:"primaryKey;column:instance_id"` // 雪花 ID: [UserID:24][TS:36][Seq:4]
	UserID     string     `gorm:"column:user_id;type:uuid;index"`
	Name       string     `gorm:"column:name"`
	Status     string     `gorm:"column:status"` // 核心状态机字段
	CreatedAt  time.Time  `gorm:"column:created_at"`
	UpdatedAt  time.Time  `gorm:"column:updated_at"`
	DeletedAt  *time.Time `gorm:"column:deleted_at"`
}

func (instance) TableName() string { return "instance" }

// instanceSpec 附着于聚合根 Instance，存储规格实体。
type instanceSpec struct {
	InstanceID int64           `gorm:"primaryKey;column:instance_id"`
	CPU        uint32          `gorm:"column:cpu"`
	Memory     uint32          `gorm:"column:memory"`
	GPU        *uint32         `gorm:"column:gpu"` // 可为空，取值代表不同 GPU 类型
	Image      string          `gorm:"column:image"`
	ConfigJSON json.RawMessage `gorm:"column:config_json"`
}

func (instanceSpec) TableName() string { return "instance_spec" }

func (r *resourceRepo) ListResources(ctx context.Context, filter biz.ListResourcesFilter) ([]biz.Resource, error) {
	var rows []instance
	db := r.data.db.WithContext(ctx).
		Model(&instance{}).
		Where("deleted_at IS NULL")

	if filter.UserID != nil {
		db = db.Where("user_id = ?", *filter.UserID)
	}
	if filter.Type != nil && *filter.Type != "" {
		db = db.Where("status = ?", *filter.Type)
	}
	if filter.Start != nil {
		db = db.Where("created_at >= ?", *filter.Start)
	}
	if filter.End != nil {
		db = db.Where("created_at <= ?", *filter.End)
	}

	if err := db.Order("created_at DESC").Find(&rows).Error; err != nil {
		return nil, err
	}

	out := make([]biz.Resource, 0, len(rows))
	for _, row := range rows {
		out = append(out, biz.Resource{
			InstanceID: row.InstanceID,
			Name:       row.Name,
			UserID:     row.UserID,
			Type:       row.Status,
			CreatedAt:  row.CreatedAt,
			UpdatedAt:  row.UpdatedAt,
		})
	}
	return out, nil
}

// ListResourceSpecs 列出实例规格
func (r *resourceRepo) ListResourceSpecs(ctx context.Context, instanceIDs []int64) (map[int64]biz.InstanceSpec, error) {
	if len(instanceIDs) == 0 {
		return map[int64]biz.InstanceSpec{}, nil
	}

	var rows []instanceSpec
	if err := r.data.db.WithContext(ctx).
		Model(&instanceSpec{}).
		Where("instance_id IN ?", instanceIDs).
		Find(&rows).Error; err != nil {
		return nil, err
	}

	out := make(map[int64]biz.InstanceSpec, len(rows))
	for _, row := range rows {
		var gpu uint32
		if row.GPU != nil {
			gpu = *row.GPU
		}
		out[row.InstanceID] = biz.InstanceSpec{
			InstanceID: row.InstanceID,
			CPU:        row.CPU,
			Memory:     row.Memory,
			GPU:        gpu,
			Image:      row.Image,
			ConfigJSON: append([]byte(nil), row.ConfigJSON...),
		}
	}
	return out, nil
}

// CreateInstance 负责落库 Instance 及其规格
func (r *resourceRepo) CreateInstance(ctx context.Context, spec biz.InstanceSpec) error {
	instanceSpecDetail := &instanceSpec{
		InstanceID: spec.InstanceID,
		CPU:        spec.CPU,
		Memory:     spec.Memory,
		Image:      spec.Image,
		ConfigJSON: append([]byte(nil), spec.ConfigJSON...),
	}
	instance := &instance{
		InstanceID: spec.InstanceID,
		UserID:     spec.UserID,
		Name:       spec.Name,
		Status:     "CREATING",
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
		DeletedAt:  nil,
	}

	// 确保 instance 和 instanceSpec 创建
	err := r.data.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(instance).Error; err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23505" {
				return biz.ErrInstanceAlreadyExists
			}
			return err
		}
		if err := tx.Create(instanceSpecDetail).Error; err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23505" {
				return biz.ErrInstanceAlreadyExists
			}
			return err
		}
		return nil
	})

	return err
}

// GetResource returns a single resource by instance ID
func (r *resourceRepo) GetResource(ctx context.Context, instanceID int64) (*biz.Resource, error) {
	var row instance
	if err := r.data.db.WithContext(ctx).
		Model(&instance{}).
		Where("instance_id = ?", instanceID).
		First(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil // 返回 nil 表示未找到
		}
		return nil, err
	}

	return &biz.Resource{
		InstanceID: row.InstanceID,
		Name:       row.Name,
		UserID:     row.UserID,
		Type:       row.Status,
		CreatedAt:  row.CreatedAt,
		UpdatedAt:  row.UpdatedAt,
	}, nil
}
