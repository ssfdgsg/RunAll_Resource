package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"time"

	v1 "resource/api/resource/v1"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

var (
	addr        = flag.String("addr", "localhost:9000", "gRPC server address")
	instanceID  = flag.Int64("instance", 0, "instance ID to exec into")
	command     = flag.String("cmd", "/bin/sh", "command to execute")
	input       = flag.String("input", "", "input to send (e.g., 'ls -la\\n')")
	interactive = flag.Bool("i", false, "interactive mode (read from stdin)")
)

func main() {
	flag.Parse()

	if *instanceID == 0 {
		log.Fatal("instance ID is required, use -instance flag")
	}

	log.Printf("connecting to gRPC server: %s", *addr)

	// 建立 gRPC 连接
	conn, err := grpc.Dial(*addr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
		grpc.WithTimeout(5*time.Second),
	)
	if err != nil {
		log.Fatalf("failed to connect: %v", err)
	}
	defer conn.Close()

	client := v1.NewResourceServiceClient(conn)

	// 创建双向流
	ctx := context.Background()
	stream, err := client.ExecContainer(ctx)
	if err != nil {
		log.Fatalf("failed to create stream: %v", err)
	}

	log.Printf("stream created, sending init message...")

	// 发送初始化消息
	initReq := &v1.ExecRequest{
		Message: &v1.ExecRequest_Init{
			Init: &v1.ExecInit{
				InstanceId: *instanceID,
				Command:    []string{*command},
				Tty:        true,
			},
		},
	}

	if err := stream.Send(initReq); err != nil {
		log.Fatalf("failed to send init: %v", err)
	}

	log.Printf("init message sent: instance_id=%d, command=%s", *instanceID, *command)

	// 启动接收协程
	recvDone := make(chan error, 1)
	go func() {
		for {
			resp, err := stream.Recv()
			if err == io.EOF {
				log.Println("stream closed by server")
				recvDone <- nil
				return
			}
			if err != nil {
				log.Printf("receive error: %v", err)
				recvDone <- err
				return
			}

			// 处理响应
			switch msg := resp.Message.(type) {
			case *v1.ExecResponse_Output:
				streamType := "stdout"
				if msg.Output.Stream == v1.ExecOutput_STDERR {
					streamType = "stderr"
				}
				fmt.Printf("[%s] %s", streamType, string(msg.Output.Data))

			case *v1.ExecResponse_Error:
				log.Printf("ERROR: %s", msg.Error.Message)

			case *v1.ExecResponse_Exit:
				log.Printf("process exited with code: %d", msg.Exit.Code)
				recvDone <- nil
				return
			}
		}
	}()

	// 发送输入
	if *interactive {
		log.Println("interactive mode: type commands and press Enter (Ctrl+C to exit)")
		// 交互模式：从 stdin 读取
		go func() {
			buf := make([]byte, 1024)
			for {
				n, err := os.Stdin.Read(buf)
				if err != nil {
					if err != io.EOF {
						log.Printf("stdin read error: %v", err)
					}
					stream.CloseSend()
					return
				}

				if n > 0 {
					inputReq := &v1.ExecRequest{
						Message: &v1.ExecRequest_Input{
							Input: &v1.ExecInput{
								Data: buf[:n],
							},
						},
					}
					if err := stream.Send(inputReq); err != nil {
						log.Printf("send input error: %v", err)
						return
					}
				}
			}
		}()
	} else if *input != "" {
		// 非交互模式：发送指定的输入
		log.Printf("sending input: %q", *input)
		inputReq := &v1.ExecRequest{
			Message: &v1.ExecRequest_Input{
				Input: &v1.ExecInput{
					Data: []byte(*input),
				},
			},
		}
		if err := stream.Send(inputReq); err != nil {
			log.Fatalf("failed to send input: %v", err)
		}

		// 等待一段时间后关闭输入流
		time.Sleep(1 * time.Second)
		if err := stream.CloseSend(); err != nil {
			log.Printf("close send error: %v", err)
		}
	} else {
		// 没有输入，直接关闭发送端
		log.Println("no input specified, closing send stream")
		if err := stream.CloseSend(); err != nil {
			log.Printf("close send error: %v", err)
		}
	}

	// 等待接收完成
	if err := <-recvDone; err != nil {
		log.Fatalf("receive failed: %v", err)
	}

	log.Println("test completed successfully")
}
