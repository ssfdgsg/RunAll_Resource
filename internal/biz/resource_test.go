package biz

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/go-kratos/kratos/v2/log"
)

type instanceRepoStub struct {
	err error
}

func (s *instanceRepoStub) CreateInstance(ctx context.Context, spec InstanceSpec) error {
	return s.err
}

type k8sRepoStub struct {
	calls int
}

func (s *k8sRepoStub) CreateInstance(ctx context.Context, spec InstanceSpec) error {
	s.calls++
	return nil
}

type auditRepoStub struct {
	calls int
}

func (s *auditRepoStub) CreateAudit(ctx context.Context, information AuditInformation) error {
	s.calls++
	return nil
}

func TestResourceUsecase_CreateInstance_IdempotentAlreadyExists(t *testing.T) {
	k8s := &k8sRepoStub{}
	audit := &auditRepoStub{}
	logger := log.NewHelper(log.NewStdLogger(io.Discard))
	uc := &ResourceUsecase{
		InstanceSpec: &instanceRepoStub{err: ErrInstanceAlreadyExists},
		AuditRepo:    audit,
		K8sRepo:      k8s,
		log:          logger,
	}

	if err := uc.CreateInstance(context.Background(), InstanceSpec{InstanceID: 1}); err != nil {
		t.Fatalf("err=%v want=nil", err)
	}
	if k8s.calls != 0 {
		t.Fatalf("k8s.calls=%d want=0", k8s.calls)
	}
	if audit.calls != 0 {
		t.Fatalf("audit.calls=%d want=0", audit.calls)
	}
}

func TestResourceUsecase_CreateInstance_PassesThroughOtherErrors(t *testing.T) {
	wantErr := errors.New("db down")
	logger := log.NewHelper(log.NewStdLogger(io.Discard))
	uc := &ResourceUsecase{
		InstanceSpec: &instanceRepoStub{err: wantErr},
		AuditRepo:    &auditRepoStub{},
		K8sRepo:      &k8sRepoStub{},
		log:          logger,
	}

	err := uc.CreateInstance(context.Background(), InstanceSpec{InstanceID: 1})
	if !errors.Is(err, wantErr) {
		t.Fatalf("err=%v want=%v", err, wantErr)
	}
}
