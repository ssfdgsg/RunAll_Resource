package main

import (
	"context"
	"crypto/tls"
	"flag"
	"log"
	"math/rand"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"resource/api/mq/v1"
	"resource/internal/conf"

	"github.com/go-kratos/kratos/v2/config"
	"github.com/go-kratos/kratos/v2/config/file"
	amqp "github.com/rabbitmq/amqp091-go"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func main() {
	// 加载配置文件
	confDir := flag.String("conf", "", "config dir, default is ./configs or ../configs")
	flag.Parse()

	// 如果未指定配置目录，自动查找
	var configPath string
	if *confDir != "" {
		configPath = *confDir
	} else {
		// 获取当前工作目录
		cwd, err := os.Getwd()
		if err != nil {
			log.Fatalf("get working directory: %v", err)
		}
		log.Printf("current working directory: %s", cwd)

		// 尝试从当前目录或上一级目录查找 configs
		candidates := []string{
			filepath.Join(cwd, "configs"),       // 从项目根目录运行
			filepath.Join(cwd, "../configs"),    // 从 tests 目录运行
			filepath.Join(cwd, "../../configs"), // 其他情况
			"configs",                           // 相对路径回退
			"../configs",                        // 相对路径回退
		}

		for _, candidate := range candidates {
			log.Printf("trying config path: %s", candidate)
			if info, err := os.Stat(candidate); err == nil && info.IsDir() {
				configPath, _ = filepath.Abs(candidate)
				log.Printf("found config directory: %s", configPath)
				break
			}
		}

		if configPath == "" {
			log.Fatal("cannot find configs directory, please use -conf flag to specify the path")
		}
	}

	// 转换为绝对路径以确保正确加载
	absPath, err := filepath.Abs(configPath)
	if err != nil {
		log.Fatalf("get absolute path: %v", err)
	}

	log.Printf("loading config from: %s", absPath)
	c := config.New(config.WithSource(file.NewSource(absPath)))
	if err := c.Load(); err != nil {
		log.Fatalf("load config from %s: %v", absPath, err)
	}

	var bc conf.Bootstrap
	if err := c.Scan(&bc); err != nil {
		log.Fatalf("scan config: %v", err)
	}

	// 获取 RabbitMQ 配置
	rabbitMqConf := bc.GetData().GetRabbitmq()
	if rabbitMqConf == nil {
		log.Fatal("rabbitmq config not found")
	}

	// 测试 RabbitMQ 连接
	log.Printf("=== Testing RabbitMQ Connection ===")
	originalURL := rabbitMqConf.GetUrl()
	log.Printf("Original URL: %s", maskPassword(originalURL))
	log.Printf("Queue: %s", rabbitMqConf.GetQueue())
	log.Printf("Exchange: %s", rabbitMqConf.GetExchange())
	log.Printf("Routing Key: %s", rabbitMqConf.GetRoutingKey())

	// 解析 URL 获取主机和端口
	log.Printf("\n=== Connection Diagnostics ===")
	host, port := parseHostPort(originalURL)
	log.Printf("Host: %s", host)
	log.Printf("Port: %s", port)

	// 测试 TCP 连接
	log.Printf("\n[Diagnostic 1] Testing TCP connectivity...")
	if err := testTCPConnection(host, port); err != nil {
		log.Printf("❌ TCP connection failed: %v", err)
		log.Printf("Possible issues:")
		log.Printf("  - Firewall blocking the port")
		log.Printf("  - Incorrect host/port")
		log.Printf("  - Service not running")
		log.Fatalf("Cannot proceed without TCP connectivity")
	}
	log.Printf("✅ TCP connection successful")

	// 尝试多种连接方式
	log.Printf("\n=== Attempting AMQP Connection ===")
	
	// 测试配置
	testConfigs := []struct {
		name string
		url  string
		useTLS bool
	}{
		{"Plain AMQP", originalURL, false},
		{"AMQP with TLS", strings.Replace(originalURL, "amqp://", "amqps://", 1), true},
	}

	// 尝试不同的 VHost
	vhosts := []string{"/", "/%2F", ""} // /, URL编码的/, 空
	
	var conn *amqp.Connection
	var lastErr error
	var successURL string

	for _, cfg := range testConfigs {
		for _, vhost := range vhosts {
			testURL := replaceVHost(cfg.url, vhost)
			log.Printf("\n[Attempt] %s with vhost '%s'", cfg.name, vhost)
			log.Printf("URL: %s", maskPassword(testURL))
			
			if cfg.useTLS {
				tlsConfig := &tls.Config{
					InsecureSkipVerify: false,
				}
				conn, lastErr = amqp.DialTLS(testURL, tlsConfig)
			} else {
				conn, lastErr = amqp.Dial(testURL)
			}

			if lastErr == nil {
				successURL = testURL
				log.Printf("✅ Connection established!")
				goto connected
			}
			
			log.Printf("❌ Failed: %v", lastErr)
		}
	}

connected:
	if conn == nil {
		log.Printf("\n=== Troubleshooting Guide ===")
		log.Printf("All connection attempts failed. Please check:")
		log.Printf("1. Verify credentials in Zeabur dashboard")
		log.Printf("2. Check if the RabbitMQ instance is running")
		log.Printf("3. Verify the connection URL format")
		log.Printf("4. Check if there are IP whitelist restrictions")
		log.Printf("5. Try connecting from Zeabur's web console first")
		log.Fatalf("\n❌ Last error: %v", lastErr)
	}

	log.Printf("\n✅ Successfully connected with: %s", maskPassword(successURL))
	defer func() {
		if err := conn.Close(); err != nil {
			log.Printf("close connection: %v", err)
		}
	}()

	// 步骤 2: 测试 Channel
	log.Printf("\n[Step 2] Opening channel...")
	ch, err := conn.Channel()
	if err != nil {
		log.Fatalf("❌ Channel open failed: %v", err)
	}
	log.Printf("✅ Channel opened")
	defer func() {
		if err := ch.Close(); err != nil {
			log.Printf("close channel: %v", err)
		}
	}()

	// 步骤 3: 声明 Exchange（如果配置了）
	if rabbitMqConf.GetExchange() != "" {
		log.Printf("\n[Step 3] Declaring exchange: %s", rabbitMqConf.GetExchange())
		err = ch.ExchangeDeclare(
			rabbitMqConf.GetExchange(), // name
			"direct",                    // type
			true,                        // durable
			false,                       // auto-deleted
			false,                       // internal
			false,                       // no-wait
			nil,                         // arguments
		)
		if err != nil {
			log.Fatalf("❌ Exchange declare failed: %v", err)
		}
		log.Printf("✅ Exchange declared")
	}

	// 步骤 4: 声明 Queue
	log.Printf("\n[Step 4] Declaring queue: %s", rabbitMqConf.GetQueue())
	q, err := ch.QueueDeclare(
		rabbitMqConf.GetQueue(), // name
		true,                     // durable
		false,                    // delete when unused
		false,                    // exclusive
		false,                    // no-wait
		nil,                      // arguments
	)
	if err != nil {
		log.Fatalf("❌ Queue declare failed: %v", err)
	}
	log.Printf("✅ Queue declared: %s (messages: %d, consumers: %d)", q.Name, q.Messages, q.Consumers)

	// 步骤 5: 绑定 Queue 到 Exchange（如果配置了）
	if rabbitMqConf.GetExchange() != "" && rabbitMqConf.GetRoutingKey() != "" {
		log.Printf("\n[Step 5] Binding queue to exchange with routing key: %s", rabbitMqConf.GetRoutingKey())
		err = ch.QueueBind(
			q.Name,
			rabbitMqConf.GetRoutingKey(),
			rabbitMqConf.GetExchange(),
			false,
			nil,
		)
		if err != nil {
			log.Fatalf("❌ Queue bind failed: %v", err)
		}
		log.Printf("✅ Queue bound")
	}

	// 步骤 6: 发送测试消息
	log.Printf("\n[Step 6] Sending test message...")
	event := &v1.Event{

		EventType: v1.EventType_INSTANCE_CREATED.String(),

		InstanceId: rand.Int63(),

		UserId: "7de5d3bc-c5d7-44d0-a5bb-a94be248f523",

		Name: "demo-instance-2",

		Timestamp: timestamppb.Now(),

		Spec: &v1.InstanceSpec{

			Cpus: 1, MemoryMb: 256, Gpu: 0, Image: "ubuntu:22.04",
		},
	}

	body, err := proto.Marshal(event)
	if err != nil {
		log.Fatalf("❌ Marshal failed: %v", err)
	}
	log.Printf("Message size: %d bytes", len(body))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// 根据是否配置了 Exchange 选择发送方式
	var exchange, routingKey string
	if rabbitMqConf.GetExchange() != "" {
		exchange = rabbitMqConf.GetExchange()
		routingKey = rabbitMqConf.GetRoutingKey()
		log.Printf("Publishing to exchange: %s, routing key: %s", exchange, routingKey)
	} else {
		exchange = ""
		routingKey = rabbitMqConf.GetQueue()
		log.Printf("Publishing directly to queue: %s", routingKey)
	}

	err = ch.PublishWithContext(ctx,
		exchange,
		routingKey,
		false, // mandatory
		false, // immediate
		amqp.Publishing{
			ContentType:  "application/octet-stream",
			Body:         body,
			DeliveryMode: amqp.Persistent, // 持久化消息
		},
	)
	if err != nil {
		log.Fatalf("❌ Publish failed: %v", err)
	}

	log.Printf("✅ Message sent successfully")
	log.Printf("\n=== Test Summary ===")
	log.Printf("✅ Connection: OK")
	log.Printf("✅ Channel: OK")
	log.Printf("✅ Exchange: OK")
	log.Printf("✅ Queue: OK")
	log.Printf("✅ Binding: OK")
	log.Printf("✅ Publish: OK")
	log.Printf("\n🎉 All tests passed!")
}

// maskPassword 隐藏 RabbitMQ URL 中的密码
func maskPassword(url string) string {
	if len(url) < 10 {
		return url
	}
	start := len("amqp://")
	if start >= len(url) {
		return url
	}
	
	atIdx := -1
	for i := start; i < len(url); i++ {
		if url[i] == '@' {
			atIdx = i
			break
		}
	}
	
	if atIdx == -1 {
		return url
	}
	
	for i := start; i < atIdx; i++ {
		if url[i] == ':' {
			return url[:i+1] + "***" + url[atIdx:]
		}
	}
	
	return url

}

// parseHostPort 从 AMQP URL 中提取主机和端口
func parseHostPort(url string) (string, string) {
	// amqp://user:pass@host:port/vhost
	start := strings.Index(url, "@")
	if start == -1 {
		return "", ""
	}
	start++
	
	end := strings.Index(url[start:], "/")
	if end == -1 {
		end = len(url) - start
	}
	
	hostPort := url[start : start+end]
	parts := strings.Split(hostPort, ":")
	if len(parts) == 2 {
		return parts[0], parts[1]
	}
	return hostPort, "5672"
}

// testTCPConnection 测试 TCP 连接
func testTCPConnection(host, port string) error {
	timeout := 10 * time.Second // 增加超时时间
	addr := net.JoinHostPort(host, port)
	log.Printf("Connecting to %s (timeout: %v)...", addr, timeout)
	
	conn, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return err
	}
	conn.Close()
	return nil
}

// replaceVHost 替换 URL 中的 VHost
func replaceVHost(url, vhost string) string {
	// 找到最后一个 /
	lastSlash := strings.LastIndex(url, "/")
	if lastSlash == -1 {
		return url + vhost
	}
	
	// 检查是否是协议部分的 /
	protocolEnd := 0
	if strings.HasPrefix(url, "amqp://") {
		protocolEnd = len("amqp://")
	} else if strings.HasPrefix(url, "amqps://") {
		protocolEnd = len("amqps://")
	}
	
	if lastSlash < protocolEnd {
		return url + vhost
	}
	
	// 替换 vhost
	return url[:lastSlash] + vhost
}
