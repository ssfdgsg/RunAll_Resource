package service

import (
	"context"
	mq "resource/api/mq/v1"
	v1 "resource/api/resource/v1"
	"resource/internal/biz"

	"github.com/go-kratos/kratos/v2/errors"
	"google.golang.org/protobuf/proto"
)

type ResourceService struct {
	v1.UnimplementedResourceServiceServer

	uc *biz.ResourceUsecase
}

//const Event_Type = map[string]string{
//	"INSTANCE_CREATED":         "创建实例",
//	"INSTANCE_DELETED":         "删除实例",
//	"INSTANCE_SPEC_CHANGED":    "规格变更（CPU/Memory/Image）",
//	"INSTANCE_IMAGE_REMOVED":   "镜像删除",
//	"INSTANCE_IMAGE_UPDATED":   "镜像更新",
//	"INSTANCE_STARTED":         "启动实例",
//	"INSTANCE_STOPPED":         "停止实例",
//	"INSTANCE_STATUS_CHANGED":  "实例状态变化（Running/Failed）",
//	"INSTANCE_K8S_SYNC":        "K8s 状态回传",
//	"INSTANCE_NETWORK_UPDATED": "域名/网络更新",
//}

// NewResourceService new a resource service.
func NewResourceService(uc *biz.ResourceUsecase) *ResourceService {
	return &ResourceService{uc: uc}
}

// ConsumeMqMessage implements resource.ResourceServiceServer.
func (s *ResourceService) ConsumeMqMessage(ctx context.Context, in []byte) error {
	event := &mq.Event{}
	err := proto.Unmarshal(in, event)
	if err != nil {
		return err
	}
	if event == nil || event.InstanceId == 0 {
		return errors.New(400, "INVALID_ARGUMENT", "invalid mq event")
	}
	switch event.EventType {
	case mq.EventType_INSTANCE_CREATED.String():
		// 处理实例创建事件
		if event.Spec == nil {
			return errors.New(400, "INVALID_ARGUMENT", "spec is required for INSTANCE_CREATED event")
		}
		spec := biz.InstanceSpec{
			InstanceID: event.InstanceId,
			UserID:     event.UserId,
			Name:       event.Name,
			CPU:        event.Spec.Cpus,
			Memory:     event.Spec.MemoryMb,
			GPU:        event.Spec.Gpu,
			Image:      event.Spec.Image,
			ConfigJSON: nil,
		}
		return s.uc.CreateInstance(ctx, spec)

	case mq.EventType_INSTANCE_DELETED.String():
		// 处理实例删除事件
	case mq.EventType_INSTANCE_SPEC_CHANGED.String():
		// 处理实例规格变更事件
	case mq.EventType_INSTANCE_IMAGE_REMOVED.String():
		// 处理实例镜像删除事件
	case mq.EventType_INSTANCE_IMAGE_UPDATED.String():
		// 处理实例镜像更新事件
	case mq.EventType_INSTANCE_STARTED.String():
		// 处理实例启动事件
	case mq.EventType_INSTANCE_STOPPED.String():
		// 处理实例停止事件
	case mq.EventType_INSTANCE_STATUS_CHANGED.String():
		// 处理实例状态变化事件
	case mq.EventType_INSTANCE_K8S_SYNC.String():
		// 处理K8s状态回传事件
	case mq.EventType_INSTANCE_NETWORK_UPDATED.String():
		// 处理域名/网络更新事件
	default:
		return errors.New(400, "UNKNOWN_EVENT_TYPE", "unknown event type")
	}
	return nil
}
