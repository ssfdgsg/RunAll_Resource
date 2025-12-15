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
	UserID     int64      `gorm:"column:user_id"`
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
