package server

import (
	"crypto/tls"
	"fmt"
	"net/url"
	"os"

	hellov1 "resource/api/helloworld/v1"
	resourcev1 "resource/api/resource/v1"
	"resource/internal/conf"
	"resource/internal/pkg/grpcquic"
	"resource/internal/service"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/go-kratos/kratos/v2/middleware/recovery"
	kgrpc "github.com/go-kratos/kratos/v2/transport/grpc"
	gogrpc "google.golang.org/grpc"
)

// NewGRPCServer new a gRPC server.
func NewGRPCServer(c *conf.Server, greeter *service.GreeterService, resource *service.ResourceService, logger log.Logger) *kgrpc.Server {
	grpcAddr := ""
	if c != nil && c.Grpc != nil {
		grpcAddr = c.Grpc.Addr
	}
	if grpcAddr == "" {
		grpcAddr = "0.0.0.0:9000"
	}

	certFile := os.Getenv("GRPC_QUIC_CERT_FILE")
	if certFile == "" {
		certFile = "server.crt"
	}
	keyFile := os.Getenv("GRPC_QUIC_KEY_FILE")
	if keyFile == "" {
		keyFile = "server.key"
	}

	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		panic(fmt.Errorf("load QUIC TLS cert/key failed (cert=%s key=%s): %w", certFile, keyFile, err))
	}
	tlsConf := &tls.Config{
		Certificates: []tls.Certificate{cert},
		NextProtos:   []string{"grpc-quic"},
	}

	lis, err := grpcquic.ListenAddr(grpcAddr, tlsConf, nil)
	if err != nil {
		panic(fmt.Errorf("listen QUIC addr failed (%s): %w", grpcAddr, err))
	}

	var opts = []kgrpc.ServerOption{
		kgrpc.Middleware(
			recovery.Recovery(),
		),
		kgrpc.Listener(lis),
		kgrpc.Endpoint(&url.URL{Scheme: "grpcs", Host: grpcAddr}),
		kgrpc.Options(
			gogrpc.Creds(grpcquic.NewCredentials(tlsConf)),
		),
	}
	if c != nil && c.Grpc != nil {
		if c.Grpc.Network != "" {
			opts = append(opts, kgrpc.Network(c.Grpc.Network))
		}
		if c.Grpc.Addr != "" {
			opts = append(opts, kgrpc.Address(c.Grpc.Addr))
		}
		if c.Grpc.Timeout != nil {
			opts = append(opts, kgrpc.Timeout(c.Grpc.Timeout.AsDuration()))
		}
	}

	srv := kgrpc.NewServer(opts...)
	hellov1.RegisterGreeterServer(srv, greeter)
	resourcev1.RegisterResourceServiceServer(srv, resource)
	return srv
}
