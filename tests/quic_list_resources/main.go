package main

import (
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	resourcev1 "resource/api/resource/v1"
	"resource/internal/pkg/grpcquic"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
)

func main() {
	var (
		addr      = flag.String("addr", "localhost:9000", "QUIC gRPC server address (UDP), e.g. localhost:9000")
		timeout   = flag.Duration("timeout", 3*time.Second, "RPC timeout")
		userID    = flag.Int64("user-id", 4222, "user id")
		resType   = flag.String("type", "CREATING", "resource type")
		fieldMask = flag.String("field-mask", "name", "comma-separated field mask paths, e.g. name,created_at")
		insecure  = flag.Bool("insecure", true, "skip TLS verification (for local self-signed cert)")
	)
	flag.Parse()

	tlsConf := &tls.Config{
		InsecureSkipVerify: *insecure,
		NextProtos:         []string{"grpc-quic"},
	}
	creds := grpcquic.NewCredentials(tlsConf)
	dialer := grpcquic.NewQuicDialer(tlsConf, nil)

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	conn, err := grpc.DialContext(ctx, *addr,
		grpc.WithContextDialer(dialer),
		grpc.WithTransportCredentials(creds),
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "dial failed: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close()

	client := resourcev1.NewResourceServiceClient(conn)

	paths := make([]string, 0, 4)
	for _, p := range strings.Split(*fieldMask, ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		paths = append(paths, p)
	}

	uid := *userID
	typ := *resType
	req := &resourcev1.ListResourcesReq{
		UserId:    &uid,
		Type:      &typ,
		FieldMask: &fieldmaskpb.FieldMask{Paths: paths},
	}

	reply, err := client.ListResources(ctx, req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ListResources failed: %v\n", err)
		os.Exit(1)
	}

	out, err := protojson.MarshalOptions{Multiline: true, Indent: "  "}.Marshal(reply)
	if err != nil {
		fmt.Fprintf(os.Stderr, "marshal reply failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(string(out))
}
