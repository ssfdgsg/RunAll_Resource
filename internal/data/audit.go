package data

import (
	"context"
	"errors"
	"time"

	"resource/internal/biz"

	"github.com/go-kratos/kratos/v2/log"
)

// resourceRepo-like struct, but now for audit logs.
type auditRepo struct {
	data *Data
	log  *log.Helper
}

func NewAuditRepo(data *Data, logger log.Logger) biz.AuditRepo {
	return &auditRepo{
		data: data,
		log:  log.NewHelper(logger),
	}
}

type instanceLog struct {
	ID         int64     `gorm:"primaryKey;column:id"`
	InstanceID int64     `gorm:"column:instance_id"`
	LogType    string    `gorm:"column:log_type"`
	Message    string    `gorm:"column:message"`
	DataJSON   []byte    `gorm:"column:data_json"`
	CreatedAt  time.Time `gorm:"column:created_at;autoCreateTime"`
}

func (instanceLog) TableName() string { return "instance_logs" }

// CreateAudit implements biz.AuditRepo.
func (r *auditRepo) CreateAudit(ctx context.Context, info biz.AuditInformation) error {
	if r.data == nil || r.data.db == nil {
		return errors.New("postgres database not initialized")
	}

	logDetail := &instanceLog{
		InstanceID: info.InstanceID,
		LogType:    info.LogType,
		Message:    info.Message,
		DataJSON:   append([]byte(nil), info.DataJson...),
		CreatedAt:  info.CreatedAt,
	}
	if logDetail.CreatedAt.IsZero() {
		logDetail.CreatedAt = time.Now()
	}

	return r.data.db.WithContext(ctx).Create(logDetail).Error
}
