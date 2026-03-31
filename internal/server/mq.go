package server

import (
	"context"
	"errors"
	"fmt"
	"resource/internal/conf"
	"resource/internal/service"
	"strings"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/go-kratos/kratos/v2/transport"
	"github.com/streadway/amqp"
)

var _ transport.Server = (*MQServer)(nil)

// MQServer consumes RabbitMQ messages and routes them to the greeter service.
type MQServer struct {
	conn *amqp.Connection
	ch   *amqp.Channel

	log         *log.Helper
	resource    *service.ResourceService
	queue       string
	exchange    string
	routingKey  string
	dlxExchange string
	dlxQueue    string
	maxRetries  uint32

	consumerTag string
	consumeFunc func(context.Context, []byte) error
}

// NewMQServer new a MQ server.
func NewMQServer(c *conf.Data, conn *amqp.Connection, resource *service.ResourceService, logger log.Logger) *MQServer {
	r := c.GetRabbitmq()

	// 设置默认值
	maxRetries := r.GetMaxRetries()
	if maxRetries == 0 {
		maxRetries = 3 // 默认重试 3 次
	}

	dlxExchange := r.GetDlxExchange()
	if dlxExchange == "" {
		dlxExchange = "resource.dlx"
	}

	dlxQueue := r.GetDlxQueue()
	if dlxQueue == "" {
		dlxQueue = "resource.dlq"
	}

	return &MQServer{
		conn:        conn,
		queue:       r.GetQueue(),
		exchange:    r.GetExchange(),
		routingKey:  r.GetRoutingKey(),
		dlxExchange: dlxExchange,
		dlxQueue:    dlxQueue,
		maxRetries:  maxRetries,
		resource:    resource,
		log:         log.NewHelper(logger),
	}
}

// Stop 停止 MQ 消费；连接由 data.NewRabbitMQ 的 cleanup 负责关闭。
func (s *MQServer) Stop(ctx context.Context) error {
	if s.ch == nil {
		return nil
	}
	if s.consumerTag != "" {
		_ = s.ch.Cancel(s.consumerTag, false)
	}
	if err := s.ch.Close(); err != nil && err != amqp.ErrClosed {
		return err
	}
	return nil
}

func (s *MQServer) Start(ctx context.Context) error {
	// 如果连接为 nil，说明 RabbitMQ 未配置或连接失败，跳过启动
	if s.conn == nil {
		s.log.Warn("RabbitMQ connection is nil, MQ server will not start")
		// 阻塞等待 context 取消，但不消费消息
		<-ctx.Done()
		return nil
	}

	var err error

	s.log.Infof("starting MQ server, queue=%s, exchange=%s, routingKey=%s, maxRetries=%d", s.queue, s.exchange, s.routingKey, s.maxRetries)

	// 1. 获取 Channel
	s.ch, err = s.conn.Channel()
	if err != nil {
		s.log.Errorf("failed to open channel: %v", err)
		return fmt.Errorf("failed to open channel: %v", err)
	}

	// 2. 声明死信交换机
	s.log.Infof("declaring DLX exchange: %s", s.dlxExchange)
	err = s.ch.ExchangeDeclare(
		s.dlxExchange, // name
		"direct",      // type
		true,          // durable
		false,         // auto-deleted
		false,         // internal
		false,         // no-wait
		nil,
	)
	if err != nil {
		s.log.Errorf("DLX exchange declare failed: %v", err)
		return fmt.Errorf("DLX exchange declare failed: %v", err)
	}

	// 3. 声明死信队列
	s.log.Infof("declaring DLQ queue: %s", s.dlxQueue)
	dlq, err := s.ch.QueueDeclare(
		s.dlxQueue, // name
		true,       // durable
		false,      // delete when unused
		false,      // exclusive
		false,      // no-wait
		nil,
	)
	if err != nil {
		s.log.Errorf("DLQ queue declare failed: %v", err)
		return fmt.Errorf("DLQ queue declare failed: %v", err)
	}

	// 4. 绑定死信队列到死信交换机
	err = s.ch.QueueBind(
		dlq.Name,
		s.dlxQueue,    // routing key
		s.dlxExchange, // exchange
		false,
		nil,
	)
	if err != nil {
		s.log.Errorf("DLQ queue bind failed: %v", err)
		return fmt.Errorf("DLQ queue bind failed: %v", err)
	}
	s.log.Infof("DLQ configured successfully")

	// 5. 声明主 Exchange
	if s.exchange != "" {
		s.log.Infof("declaring exchange: %s (type=direct, durable=true)", s.exchange)
		err = s.ch.ExchangeDeclare(
			s.exchange, // name
			"direct",   // type
			true,       // durable: 持久化
			false,      // auto-deleted
			false,      // internal
			false,      // no-wait
			nil,        // arguments
		)
		if err != nil {
			s.log.Errorf("exchange declare failed: %v", err)
			return fmt.Errorf("exchange declare failed: %v", err)
		}
		s.log.Infof("exchange declared successfully: %s", s.exchange)
	}

	// 6. 声明主 Queue（配置死信队列）
	s.log.Infof("declaring queue: %s (durable=true, with DLX)", s.queue)
	q, err := s.ch.QueueDeclare(
		s.queue, // name
		true,    // durable: 队列持久化，防止重启丢失
		false,   // delete when unused
		false,   // exclusive
		false,   // no-wait
		amqp.Table{
			"x-dead-letter-exchange":    s.dlxExchange, // 死信交换机
			"x-dead-letter-routing-key": s.dlxQueue,    // 死信路由键
		},
	)
	if err != nil {
		s.log.Errorf("queue declare failed: %v", err)
		return fmt.Errorf("queue declare failed: %v", err)
	}
	s.queue = q.Name
	s.log.Infof("queue declared successfully: %s", s.queue)

	// 7. 绑定 Queue 到 Exchange
	if s.exchange != "" && s.routingKey != "" {
		s.log.Infof("binding queue %s to exchange %s with routing key %s", q.Name, s.exchange, s.routingKey)
		err = s.ch.QueueBind(
			q.Name,
			s.routingKey,
			s.exchange,
			false,
			nil,
		)
		if err != nil {
			s.log.Errorf("queue bind failed: %v", err)
			return fmt.Errorf("queue bind failed: %v", err)
		}
		s.log.Infof("queue bound successfully")
	}

	// 5. QoS：单条串行处理，避免一次拉取过多消息
	if err := s.ch.Qos(1, 0, false); err != nil {
		return fmt.Errorf("qos set failed: %v", err)
	}

	// 6. 消费消息（autoAck 必须为 false，否则手动 Ack/Reject 会触发 channel exception）
	s.consumerTag = "resource-consumer"
	msgs, err := s.ch.Consume(
		q.Name,        // queue
		s.consumerTag, // consumer tag
		false,         // auto-ack: 关闭自动确认
		false,         // exclusive
		false,         // no-local
		false,         // no-wait
		nil,           // args
	)
	if err != nil {
		return err
	}
	s.log.Infof("MQ Consumer started on Queue [%s]. Mode: Serial/Blocking", s.queue)

	notifyClose := s.ch.NotifyClose(make(chan *amqp.Error, 1))

	// 7. 启动消费循环
	for {
		select {
		case <-ctx.Done():
			s.log.Info("MQ Consumer stopping (context canceled)")
			_ = s.ch.Cancel(s.consumerTag, false)
			return nil
		case amqpErr := <-notifyClose:
			if amqpErr != nil {
				return fmt.Errorf("mq channel closed: %v", amqpErr)
			}
			s.log.Info("MQ channel closed")
			return nil
		case d, ok := <-msgs:
			if !ok {
				s.log.Info("MQ delivery channel closed")
				return nil
			}
			s.processMessage(ctx, d)
		}
	}
}

// processMessage 处理单条消息
func (s *MQServer) processMessage(ctx context.Context, d amqp.Delivery) {
	// 获取重试次数
	retryCount := getRetryCount(d.Headers)

	s.log.Infof("processing message: deliveryTag=%d, redelivered=%t, retryCount=%d/%d, bodySize=%d",
		d.DeliveryTag, d.Redelivered, retryCount, s.maxRetries, len(d.Body))

	// 检查是否超过最大重试次数
	if retryCount >= int(s.maxRetries) {
		s.log.Errorf("message exceeded max retries (%d), sending to DLQ", s.maxRetries)
		// 超过重试次数，拒绝并发送到死信队列
		if err := d.Reject(false); err != nil {
			s.log.Errorf("failed to reject message: %v", err)
		}
		return
	}

	s.processMessageBody(
		ctx,
		d.Body,
		func() error { return d.Ack(false) },
		func() error {
			s.log.Warnf("requeuing message (retry %d/%d)", retryCount+1, s.maxRetries)
			return d.Reject(true) // 重新入队
		},
		func() error {
			s.log.Errorf("dropping message (non-retryable error)")
			return d.Reject(false) // 发送到死信队列
		},
	)
}

func (s *MQServer) processMessageBody(
	ctx context.Context,
	body []byte,
	ack func() error,
	rejectRequeue func() error,
	rejectDrop func() error,
) {
	defer func() {
		if r := recover(); r != nil {
			s.log.Errorf("PANIC in MQ handler: %v", r)
			if err := rejectDrop(); err != nil {
				s.log.Errorf("Failed to drop message after panic: %v", err)
			}
		}
	}()

	consume := s.consumeFunc
	if consume == nil {
		consume = s.resource.ConsumeMqMessage
	}

	err := consume(ctx, body)
	if err == nil {
		if err := ack(); err != nil {
			s.log.Errorf("Failed to ACK message: %v", err)
		} else {
			s.log.Info("Message handled and ACKed")
		}
		return
	}

	// 根据错误类型决定是否重试
	if isRetryableError(err) {
		s.log.Warnf("Retryable error: %v, requeuing", err)
		if err := rejectRequeue(); err != nil {
			s.log.Errorf("Failed to requeue message: %v", err)
		}
	} else {
		s.log.Errorf("Non-retryable error: %v, sending to DLQ", err)
		if err := rejectDrop(); err != nil {
			s.log.Errorf("Failed to drop message: %v", err)
		}
	}
}

// getRetryCount 获取消息的重试次数
func getRetryCount(headers amqp.Table) int {
	if headers == nil {
		return 0
	}

	// RabbitMQ 在消息被 reject(requeue=true) 后会增加 x-death header
	if xDeath, ok := headers["x-death"].([]interface{}); ok && len(xDeath) > 0 {
		if death, ok := xDeath[0].(amqp.Table); ok {
			if count, ok := death["count"].(int64); ok {
				return int(count)
			}
		}
	}

	return 0
}

// isRetryableError 判断错误是否可重试
func isRetryableError(err error) bool {
	if err == nil {
		return false
	}

	errMsg := err.Error()

	// 网络错误、超时错误 → 可重试
	if errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(err, context.Canceled) {
		return true
	}

	// K8s API 临时错误 → 可重试
	if strings.Contains(errMsg, "connection refused") ||
		strings.Contains(errMsg, "connection reset") ||
		strings.Contains(errMsg, "timeout") ||
		strings.Contains(errMsg, "temporary failure") {
		return true
	}

	// 数据库约束冲突、参数错误 → 不可重试
	if strings.Contains(errMsg, "duplicate key") ||
		strings.Contains(errMsg, "INVALID_ARGUMENT") ||
		strings.Contains(errMsg, "NOT_FOUND") ||
		strings.Contains(errMsg, "ALREADY_EXISTS") ||
		strings.Contains(errMsg, "constraint") {
		return false
	}

	// K8s 资源已存在 → 不可重试
	if strings.Contains(errMsg, "AlreadyExists") {
		return false
	}

	// 默认可重试（保守策略）
	return true
}
