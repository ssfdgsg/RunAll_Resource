package biz

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/go-kratos/kratos/v2/log"
)

var ErrInstanceAlreadyExists = errors.New("instance already exists")

type ResourceUsecase struct {
	InstanceSpec InstanceRepo
	AuditRepo    AuditRepo
	K8sRepo      K8sRepo
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
	UserID     int64
	Name       string
	CPU        uint32
	Memory     uint32
	GPU        uint32
	Image      string
	ConfigJSON json.RawMessage
}

type InstanceRepo interface {
	CreateInstance(ctx context.Context, spec InstanceSpec) error
}

type K8sRepo interface {
	CreateInstance(ctx context.Context, spec InstanceSpec) error
}

func NewResourceUsecase(repo InstanceRepo, audit AuditRepo, k8sRepo K8sRepo, logger log.Logger) *ResourceUsecase {
	return &ResourceUsecase{InstanceSpec: repo, AuditRepo: audit, K8sRepo: k8sRepo, log: log.NewHelper(logger)}
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
