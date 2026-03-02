package data

import (
	"errors"

	"resource/internal/conf"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/streadway/amqp"
)

// NewRabbitMQ dials RabbitMQ using the provided configuration.
func NewRabbitMQ(c *conf.Data, logger log.Logger) (*amqp.Connection, func(), error) {
	helper := log.NewHelper(logger)
	if c == nil || c.GetRabbitmq() == nil || c.GetRabbitmq().GetUrl() == "" {
		helper.Warn("rabbitmq configuration is missing, skipping connection")
		return nil, func() {}, nil // 返回 nil 但不报错，允许服务启动
	}

	rabbitURL := c.GetRabbitmq().GetUrl()
	helper.Infof("connecting to RabbitMQ: %s", maskPassword(rabbitURL))

	conn, err := amqp.Dial(rabbitURL)
	if err != nil {
		helper.Errorf("failed to connect to RabbitMQ: %v", err)
		helper.Warn("RabbitMQ connection failed, MQ features will be disabled")
		return nil, func() {}, nil // 不阻止服务启动
	}

	// 测试连接是否真的可用
	ch, err := conn.Channel()
	if err != nil {
		conn.Close()
		helper.Errorf("failed to open test channel: %v", err)
		helper.Warn("RabbitMQ channel test failed, MQ features will be disabled")
		return nil, func() {}, nil
	}
	ch.Close()

	// 监听连接关闭事件
	go func() {
		closeErr := <-conn.NotifyClose(make(chan *amqp.Error))
		if closeErr != nil {
			helper.Errorf("RabbitMQ connection closed: %v", closeErr)
		} else {
			helper.Info("RabbitMQ connection closed gracefully")
		}
	}()

	cleanup := func() {
		if err := conn.Close(); err != nil && !errors.Is(err, amqp.ErrClosed) {
			helper.Errorf("failed to close rabbitmq connection: %v", err)
			return
		}
		helper.Info("rabbitmq connection closed")
	}

	helper.Info("rabbitmq connection established successfully")
	return conn, cleanup, nil
}

// maskPassword 隐藏 RabbitMQ URL 中的密码
func maskPassword(url string) string {
	// amqp://user:password@host:port/vhost -> amqp://user:***@host:port/vhost
	if len(url) < 10 {
		return url
	}
	start := 0
	if idx := len("amqp://"); len(url) > idx {
		start = idx
	}
	if idx := len(url[start:]); idx > 0 {
		if atIdx := start; atIdx < len(url) {
			for i := start; i < len(url); i++ {
				if url[i] == '@' {
					atIdx = i
					break
				}
			}
			if atIdx > start {
				for i := start; i < atIdx; i++ {
					if url[i] == ':' {
						return url[:i+1] + "***" + url[atIdx:]
					}
				}
			}
		}
	}
	return url
}

func NewRabbitMQChannel(conn *amqp.Connection, c *conf.Data, logger log.Logger) (*amqp.Channel, error) {
	ch, err := conn.Channel()
	if err != nil {
		return nil, err
	}

	// 声明 direct exchange
	err = ch.ExchangeDeclare(
		c.Rabbitmq.Exchange,
		"direct",
		true, false, false, false, nil,
	)
	if err != nil {
		return nil, err
	}

	return ch, nil
}
