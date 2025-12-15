package service

import (
	"context"
	mq "resource/api/mq/v1"
	v1 "resource/api/resource/v1"
	"resource/internal/biz"
	"strconv"

	"github.com/go-kratos/kratos/v2/errors"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"
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

func (s *ResourceService) ListResources(ctx context.Context, in *v1.ListResourcesReq) (*v1.ListResourcesReply, error) {
	if in == nil {
		return nil, errors.New(400, "INVALID_ARGUMENT", "request is required")
	}

	var filter biz.ListResourcesFilter
	if in.UserId != nil {
		filter.UserID = in.UserId
	}
	if in.Type != nil {
		filter.Type = in.Type
	}
	if in.Start != nil {
		start := in.Start.AsTime()
		filter.Start = &start
	}
	if in.End != nil {
		end := in.End.AsTime()
		filter.End = &end
	}

	resources, err := s.uc.ListResources(ctx, filter)
	if err != nil {
		return nil, err
	}

	reply := &v1.ListResourcesReply{
		Resources: make([]*v1.Resource, 0, len(resources)),
	}

	mask := in.GetFieldMask()
	instanceIDs := make([]int64, 0, len(resources))
	for _, resource := range resources {
		instanceIDs = append(instanceIDs, resource.InstanceID)
		item := &v1.Resource{
			InstanceId: resource.InstanceID,
			Name:       resource.Name,
			UserId:     resource.UserID,
			Type:       resource.Type,
			CreatedAt:  timestamppb.New(resource.CreatedAt),
			UpdatedAt:  timestamppb.New(resource.UpdatedAt),
		}
		if mask != nil && len(mask.GetPaths()) > 0 {
			item, err = applyResourceFieldMask(item, mask)
			if err != nil {
				return nil, err
			}
		}
		reply.Resources = append(reply.Resources, item)
	}

	specs, err := s.uc.ListResourceSpecs(ctx, instanceIDs)
	if err != nil {
		return nil, err
	}
	if len(specs) > 0 {
		reply.Specs = make(map[string]*v1.ResourceSpec, len(specs))
		for instanceID, spec := range specs {
			var customConfig *structpb.Struct
			if len(spec.ConfigJSON) > 0 {
				cfg := &structpb.Struct{}
				if err := protojson.Unmarshal(spec.ConfigJSON, cfg); err != nil {
					return nil, err
				}
				customConfig = cfg
			}
			reply.Specs[strconv.FormatInt(instanceID, 10)] = &v1.ResourceSpec{
				InstanceId:   instanceID,
				CpuCores:     spec.CPU,
				MemorySize:   spec.Memory,
				Gpu:          spec.GPU,
				Image:        spec.Image,
				CustomConfig: customConfig,
			}
		}
	}

	return reply, nil
}

func applyResourceFieldMask(in *v1.Resource, mask *fieldmaskpb.FieldMask) (*v1.Resource, error) {
	if in == nil {
		return nil, errors.New(400, "INVALID_ARGUMENT", "resource is required")
	}
	if mask == nil || len(mask.GetPaths()) == 0 {
		return in, nil
	}

	out := &v1.Resource{}
	for _, path := range mask.GetPaths() {
		switch path {
		case "instance_id", "instanceId":
			out.InstanceId = in.InstanceId
		case "name":
			out.Name = in.Name
		case "user_id", "userId":
			out.UserId = in.UserId
		case "type":
			out.Type = in.Type
		case "created_at", "createdAt":
			out.CreatedAt = in.CreatedAt
		case "updated_at", "updatedAt":
			out.UpdatedAt = in.UpdatedAt
		default:
			return nil, errors.New(400, "INVALID_FIELD_MASK", "unknown field mask path: "+path)
		}
	}
	return out, nil
}
