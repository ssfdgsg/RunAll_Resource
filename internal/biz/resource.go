package biz

import (
	"github.com/go-kratos/kratos/v2/log"
)

type ResourceRepo interface {
}

type ResourceUsecase struct {
	repo ResourceRepo
	log  *log.Helper
}

func NewResourceUsecase(repo ResourceRepo, logger log.Logger) *ResourceUsecase {
	return &ResourceUsecase{repo: repo}
}
