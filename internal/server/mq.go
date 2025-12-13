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

// MQServer consumes RabbitMQ messages and routes them to the greeter service.
type MQServer struct {
	conn *amqp.Connection
	ch   *amqp.Channel

	log        *log.Helper
	resource   *service.ResourceService
	queue      string
	exchange   string
	routingKey string

	consumerTag string
	consumeFunc func(context.Context, []byte) error
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
	fmt.Printf("the output is %d,%t,%d ", d.DeliveryTag, d.Redelivered, len(d.Body))
	s.processMessageBody(
		ctx,
		d.Body,
		func() error { return d.Ack(false) },
		func() error { return d.Reject(true) },
		func() error { return d.Reject(false) },
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
	if err := consume(ctx, body); err != nil {
		s.log.Errorf("Failed to process message: %v", err)
		if err := rejectRequeue(); err != nil {
			s.log.Errorf("Failed to reject message (requeue): %v", err)
		}
		return
	}
	if err := ack(); err != nil {
		s.log.Errorf("Failed to ACK message: %v", err)
		return
	}
	s.log.Info("Message handled and ACKed")
}
