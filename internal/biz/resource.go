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
	ListResources(ctx context.Context, filter ListResourcesFilter) ([]Resource, error)
	// ListResourceSpecs returns resource specs keyed by instance ID.
	ListResourceSpecs(ctx context.Context, instanceIDs []int64) (map[int64]InstanceSpec, error)
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

// Resource is a read model for listing resources.
type Resource struct {
	InstanceID int64
	Name       string
	UserID     int64
	Type       string
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// ListResourcesFilter defines optional filters for listing resources.
type ListResourcesFilter struct {
	UserID *int64
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
