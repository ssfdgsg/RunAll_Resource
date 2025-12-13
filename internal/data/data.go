package data

import (
	"errors"

	"resource/internal/conf"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/google/wire"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// ProviderSet is data providers.
var ProviderSet = wire.NewSet(NewData, NewGreeterRepo, NewRabbitMQ, NewK8sRepo, NewAuditRepo, NewResourceRepo)

// Data .
type Data struct {
	db *gorm.DB
}

// NewData .
func NewData(c *conf.Data, logger log.Logger) (*Data, func(), error) {
	helper := log.NewHelper(logger)
	if c == nil || c.GetDatabase() == nil || c.GetDatabase().GetSource() == "" {
		return nil, nil, errors.New("database configuration is missing")
	}

	db, err := gorm.Open(postgres.Open(c.GetDatabase().GetSource()), &gorm.Config{})
	if err != nil {
		return nil, nil, err
	}

	cleanup := func() {
		sqlDB, err := db.DB()
		if err != nil {
			helper.Errorf("failed to obtain sql.DB from gorm: %v", err)
			return
		}
		if err := sqlDB.Close(); err != nil {
			helper.Errorf("failed to close database: %v", err)
			return
		}
		helper.Info("database connection closed")
	}

	return &Data{
		db: db,
	}, cleanup, nil
}
