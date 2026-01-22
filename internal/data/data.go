package data

import (
	"errors"
	"os"
	"path/filepath"

	"resource/internal/conf"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/google/wire"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// ProviderSet is data providers.
var ProviderSet = wire.NewSet(
	NewData,
	NewRabbitMQ,
	NewK8sRepo,
	NewK8sClient,
	NewAuditRepo,
	NewResourceRepo,
	NewNetworkRepo,
	NewExecRepo,
)

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

// K8sClient 包装 Kubernetes 客户端和配置
type K8sClient struct {
	Client *kubernetes.Clientset
	Config *rest.Config
}

// NewK8sClient 创建 Kubernetes 客户端
func NewK8sClient(c *conf.Data, logger log.Logger) (*K8sClient, error) {
	helper := log.NewHelper(logger)

	// 获取 kubeconfig 路径
	var kubeconfigPath string
	if c != nil && c.GetKubernetes() != nil && c.GetKubernetes().GetKubeconfig() != "" {
		kubeconfigPath = c.GetKubernetes().GetKubeconfig()
	} else if env := os.Getenv("KUBECONFIG"); env != "" {
		kubeconfigPath = env
	} else {
		home := os.Getenv("HOME")
		if home == "" {
			home = os.Getenv("USERPROFILE")
		}
		if home == "" {
			return nil, errors.New("kubeconfig path not configured")
		}
		kubeconfigPath = filepath.Join(home, ".kube", "config")
	}

	// 构建配置
	cfg, err := clientcmd.BuildConfigFromFlags("", kubeconfigPath)
	if err != nil {
		helper.Errorf("failed to load kubeconfig %s: %v", kubeconfigPath, err)
		return nil, err
	}

	// 创建客户端
	client, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, err
	}

	return &K8sClient{
		Client: client,
		Config: cfg,
	}, nil
}
