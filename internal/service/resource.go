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

// SetInstancePort sets port exposure for an instance.
func (s *ResourceService) SetInstancePort(ctx context.Context, req *v1.SetInstancePortReq) (*v1.SetInstancePortResp, error) {
	if req == nil {
		return nil, errors.New(400, "INVALID_ARGUMENT", "request is required")
	}

	// 参数验证
	if req.InstanceId == 0 {
		return nil, errors.New(400, "INVALID_ARGUMENT", "instance_id is required")
	}
	if len(req.PortConfigs) == 0 {
		return nil, errors.New(400, "INVALID_ARGUMENT", "port_configs is required")
	}

	// 处理每个端口配置
	results := make([]*v1.PortResult, 0, len(req.PortConfigs))
	successCount := 0

	for _, config := range req.PortConfigs {
		result := &v1.PortResult{
			Port: config.Port,
		}

		// 验证端口范围
		if config.Port == 0 || config.Port > 65535 {
			result.Success = false
			result.Error = "invalid port number, must be 1-65535"
			results = append(results, result)
			continue
		}

		// 协议默认值
		protocol := config.Protocol
		if protocol == "" {
			protocol = "HTTP"
		}

		// 验证协议
		if protocol != "TCP" && protocol != "UDP" && protocol != "HTTP" {
			result.Success = false
			result.Error = "invalid protocol, must be TCP, UDP or HTTP"
			results = append(results, result)
			continue
		}

		// HTTP 模式需要 ingress_domain
		if protocol == "HTTP" && config.IngressDomain == "" {
			result.Success = false
			result.Error = "ingress_domain is required for HTTP protocol"
			results = append(results, result)
			continue
		}

		// 调用业务逻辑
		url, err := s.uc.SetInstancePort(ctx, req.InstanceId, config.Port, protocol, req.Open, config.IngressDomain)
		if err != nil {
			result.Success = false
			result.Error = err.Error()
			results = append(results, result)
			continue
		}

		// 成功
		result.Success = true
		if req.Open {
			result.AccessUrl = url
		}
		results = append(results, result)
		successCount++
	}

	// 构建响应
	allSuccess := successCount == len(req.PortConfigs)
	message := "success"
	if !allSuccess {
		message = strconv.Itoa(successCount) + "/" + strconv.Itoa(len(req.PortConfigs)) + " ports processed successfully"
	}

	return &v1.SetInstancePortResp{
		Success: allSuccess,
		Results: results,
		Message: message,
	}, nil
}

// ExecContainer 容器 Exec 双向流处理
func (s *ResourceService) ExecContainer(stream v1.ResourceService_ExecContainerServer) error {
	ctx := stream.Context()

	// 1. 接收初始化消息
	req, err := stream.Recv()
	if err != nil {
		return errors.New(500, "STREAM_ERROR", "failed to receive init message: "+err.Error())
	}

	init := req.GetInit()
	if init == nil {
		return errors.New(400, "INVALID_ARGUMENT", "first message must be ExecInit")
	}

	// 2. 参数验证
	if init.InstanceId == 0 {
		return errors.New(400, "INVALID_ARGUMENT", "instance_id is required")
	}
	if len(init.Command) == 0 {
		return errors.New(400, "INVALID_ARGUMENT", "command is required")
	}

	// 3. 查询实例信息获取 namespace (user_id)
	resource, err := s.uc.GetResource(ctx, init.InstanceId)
	if err != nil {
		return errors.New(500, "INTERNAL_ERROR", "failed to query instance: "+err.Error())
	}
	if resource == nil {
		return errors.New(404, "NOT_FOUND", "instance not found")
	}

	namespace := resource.UserID
	if namespace == "" {
		return errors.New(500, "INTERNAL_ERROR", "instance namespace is empty")
	}

	// 4. 创建输入输出通道
	inputChan := make(chan biz.ExecInput, 10)
	outputChan := make(chan biz.ExecOutput, 10)

	// 5. 启动输入处理协程（gRPC → channel）
	go func() {
		defer close(inputChan)
		for {
			req, err := stream.Recv()
			if err != nil {
				// 客户端关闭连接或网络错误
				return
			}

			switch msg := req.Message.(type) {
			case *v1.ExecRequest_Input:
				inputChan <- biz.ExecInput{
					Type: biz.ExecInputStdin,
					Data: msg.Input.Data,
				}
			case *v1.ExecRequest_Resize:
				inputChan <- biz.ExecInput{
					Type: biz.ExecInputResize,
					Rows: msg.Resize.Rows,
					Cols: msg.Resize.Cols,
				}
			}
		}
	}()

	// 6. 启动输出处理协程（channel → gRPC）
	errChan := make(chan error, 1)
	go func() {
		for out := range outputChan {
			var resp *v1.ExecResponse

			switch out.Type {
			case biz.ExecOutputData:
				streamType := v1.ExecOutput_STDOUT
				if out.Stream == "stderr" {
					streamType = v1.ExecOutput_STDERR
				}
				resp = &v1.ExecResponse{
					Message: &v1.ExecResponse_Output{
						Output: &v1.ExecOutput{
							Stream: streamType,
							Data:   out.Data,
						},
					},
				}
			case biz.ExecOutputError:
				resp = &v1.ExecResponse{
					Message: &v1.ExecResponse_Error{
						Error: &v1.ExecError{Message: string(out.Data)},
					},
				}
			case biz.ExecOutputExit:
				resp = &v1.ExecResponse{
					Message: &v1.ExecResponse_Exit{
						Exit: &v1.ExecExit{Code: out.ExitCode},
					},
				}
			}

			if err := stream.Send(resp); err != nil {
				errChan <- err
				return
			}
		}
		errChan <- nil
	}()

	// 7. 调用业务逻辑执行命令
	containerName := ""
	if init.ContainerName != nil {
		containerName = *init.ContainerName
	}

	_ = s.uc.StreamExec(ctx, namespace, init.InstanceId, init.Command, init.Tty, containerName, inputChan, outputChan)
	close(outputChan)

	// 8. 等待输出协程完成
	return <-errChan
}
