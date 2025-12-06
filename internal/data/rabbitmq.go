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
		return nil, nil, errors.New("rabbitmq configuration is missing")
	}

	conn, err := amqp.Dial(c.GetRabbitmq().GetUrl())
	if err != nil {
		return nil, nil, err
	}

	cleanup := func() {
		if err := conn.Close(); err != nil && !errors.Is(err, amqp.ErrClosed) {
			helper.Errorf("failed to close rabbitmq connection: %v", err)
			return
		}
		helper.Info("rabbitmq connection closed")
	}

	helper.Info("rabbitmq connection established")
	return conn, cleanup, nil
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
