package client

import (
	"context"
	"crypto/tls"

	"resource/internal/pkg/grpcquic"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
)

// NewGRPCClientConn creates a gRPC client connection.
// When useQUIC is true, it dials the server using grpc-quic.
func NewGRPCClientConn(ctx context.Context, addr string, useQUIC bool) (*grpc.ClientConn, error) {
	if useQUIC {
		return NewGRPCClientConnWithTLS(ctx, addr, true, nil)
	}
	return grpc.DialContext(ctx, addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
}

// NewGRPCClientConnWithTLS creates a gRPC client connection with TLS.
// When useQUIC is true, it uses grpc-quic and requires TLS (QUIC always uses TLS).
func NewGRPCClientConnWithTLS(ctx context.Context, addr string, useQUIC bool, tlsConfig *tls.Config) (*grpc.ClientConn, error) {
	if useQUIC {
		if tlsConfig == nil {
			tlsConfig = &tls.Config{
				InsecureSkipVerify: true,
			}
		}
		if len(tlsConfig.NextProtos) == 0 {
			tlsConfig.NextProtos = []string{"grpc-quic"}
		}
		creds := grpcquic.NewCredentials(tlsConfig)
		dialer := grpcquic.NewQuicDialer(tlsConfig, nil)
		return grpc.DialContext(ctx, addr,
			grpc.WithContextDialer(dialer),
			grpc.WithTransportCredentials(creds),
		)
	}

	if tlsConfig == nil {
		tlsConfig = &tls.Config{}
	}
	return grpc.DialContext(ctx, addr, grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig)))
}
