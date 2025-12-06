package server

import (
	"context"
	"fmt"
	"resource/internal/conf"
	"resource/internal/service"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/go-kratos/kratos/v2/transport"
	"github.com/streadway/amqp"
)

var _ transport.Server = (*MQServer)(nil)

const defaultQueueName = "greeter.hello"

// MQServer consumes RabbitMQ messages and routes them to the greeter service.
type MQServer struct {
	conn *amqp.Connection
	ch   *amqp.Channel

	log        *log.Helper
	resource   *service.ResourceService
	queue      string
	exchange   string
	routingKey string
}

// NewMQServer new a MQ server.

func NewMQServer(c *conf.Data, conn *amqp.Connection, resource *service.ResourceService, logger log.Logger) *MQServer {
	r := c.GetRabbitmq()

	return &MQServer{
		conn:       conn,
		queue:      r.GetQueue(),
		exchange:   r.GetExchange(),
		routingKey: r.GetRoutingKey(),
		resource:   resource,
		log:        log.NewHelper(logger),
	}
}

// Stop 断开channel 和 connection
func (s *MQServer) Stop(ctx context.Context) error {
	err := s.ch.Close()
	if err != nil {
		return err
	}
	err = s.conn.Close()
	if err != nil {
		return err
	}
	return nil
}

// 错误处理函数
func (s *MQServer) handleError(err error, msg string) {
	if err != nil {
		s.log.Errorf("%s: %s", msg, err)
		panic(fmt.Sprintf("rabbitMQ panic: %s: %s", msg, err))
	}
}

func (s *MQServer) Start(ctx context.Context) error {
	var err error

	// 1. 获取 Channel
	s.ch, err = s.conn.Channel()
	if err != nil {
		return fmt.Errorf("failed to open channel: %v", err)
	}

	// 2. 声明 Exchange
	if s.exchange != "" {
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
			return fmt.Errorf("exchange declare failed: %v", err)
		}
	}

	// 3. 声明 Queue
	q, err := s.ch.QueueDeclare(
		s.queue, // name
		true,    // durable: 队列持久化，防止重启丢失
		false,   // delete when unused
		false,   // exclusive
		false,   // no-wait
		nil,     // arguments
	)
	if err != nil {
		return fmt.Errorf("queue declare failed: %v", err)
	}
	s.queue = q.Name

	// 4. 绑定 Queue 到 Exchange
	if s.exchange != "" && s.routingKey != "" {
		err = s.ch.QueueBind(
			q.Name,
			s.routingKey,
			s.exchange,
			false,
			nil,
		)
		if err != nil {
			return fmt.Errorf("queue bind failed: %v", err)
		}
	}

	// 5. 消费消息
	msgs, err := s.ch.Consume(
		q.Name, // queue
		"",     // consumer tag
		true,   // auto-ack: 关闭自动确认
		false,  // exclusive
		false,  // no-local
		false,  // no-wait
		nil,    // args
	)
	if err != nil {
		return err
	}
	s.log.Infof("MQ Consumer started on Queue [%s]. Mode: Serial/Blocking", s.queue)

	// 6. 启动消费信息
	for d := range msgs {
		s.processMessage(ctx, d)
	}

	// 循环退出说明 Channel 被关闭了（通常是 Stop 方法触发的）
	s.log.Info("MQ Consumer stopped")
	return nil
}

// processMessage 处理单条消息
func (s *MQServer) processMessage(ctx context.Context, d amqp.Delivery) {
	defer func() {
		if r := recover(); r != nil {
			s.log.Errorf("PANIC in MQ handler: %v", r)
			// 发生 Panic 时，可以选择 Reject(false) 丢弃消息，避免死循环
			_ = d.Reject(false)
		}
	}()

	if err := s.resource.ConsumeMqMessage(ctx, d.Body); err != nil {
		s.log.Errorf("Failed to process message: %v", err)
		_ = d.Reject(true)
		return
	}
	_ = d.Ack(false)
	s.log.Info("Message handled and ACKed")
}
