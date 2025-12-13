package main

import (
	"context"
	"flag"
	"log"
	"math/rand"
	"os"
	"path/filepath"
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

	log.Printf("connecting to rabbitmq: %s", rabbitMqConf.GetUrl())

	event := &v1.Event{

		EventType: v1.EventType_INSTANCE_CREATED.String(),

		InstanceId: rand.Int63(),

		UserId: 4222,

		Name: "demo-instance",

		Timestamp: timestamppb.Now(),

		Spec: &v1.InstanceSpec{

			Cpus: 2, MemoryMb: 4096, Gpu: 0, Image: "ubuntu:22.04",
		},
	}

	body, err := proto.Marshal(event)

	if err != nil {

		log.Fatalf("marshal: %v", err)

	}

	conn, err := amqp.Dial(rabbitMqConf.GetUrl())

	if err != nil {
		log.Fatal(err)
	}

	defer func() {
		if err := conn.Close(); err != nil {
			log.Printf("close connection: %v", err)
		}
	}()

	ch, err := conn.Channel()

	if err != nil {
		log.Fatal(err)
	}

	defer func() {
		if err := ch.Close(); err != nil {
			log.Printf("close channel: %v", err)
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)

	defer cancel()

	if err := ch.PublishWithContext(ctx,

		"", // exchange，留空表示直接投递到 queue

		rabbitMqConf.GetQueue(), // routing key == queue 名，从配置文件获取

		false, false,

		amqp.Publishing{

			ContentType: "application/octet-stream",

			Body: body,
		},
	); err != nil {

		log.Fatal(err)

	}

	log.Println("message sent")

}
