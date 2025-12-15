package grpcquic

import (
	"context"
	"crypto/tls"
	"net"

	quic "github.com/quic-go/quic-go"
	"google.golang.org/grpc/credentials"
)

var _ credentials.AuthInfo = (*Info)(nil)

// Info contains the auth information.
type Info struct {
	conn *Conn
}

// NewInfo creates Info.
func NewInfo(c *Conn) *Info { return &Info{conn: c} }

// AuthType returns the type of Info as a string.
func (i *Info) AuthType() string { return "quic-tls" }

// Conn returns the underlying net.Conn.
func (i *Info) Conn() net.Conn { return i.conn }

var _ credentials.TransportCredentials = (*Credentials)(nil)

// Credentials is a grpc-go TransportCredentials implementation that treats *Conn as already-secured by QUIC/TLS.
type Credentials struct {
	tlsConfig        *tls.Config
	isQuicConnection bool
	serverName       string

	grpcCreds credentials.TransportCredentials
}

// NewCredentials creates TransportCredentials for grpc-go.
func NewCredentials(tlsConfig *tls.Config) credentials.TransportCredentials {
	grpcCreds := credentials.NewTLS(tlsConfig)
	return &Credentials{
		grpcCreds: grpcCreds,
		tlsConfig: tlsConfig,
	}
}

// ClientHandshake performs the client handshake.
func (pt *Credentials) ClientHandshake(ctx context.Context, authority string, conn net.Conn) (net.Conn, credentials.AuthInfo, error) {
	if c, ok := conn.(*Conn); ok {
		pt.isQuicConnection = true
		return conn, NewInfo(c), nil
	}
	return pt.grpcCreds.ClientHandshake(ctx, authority, conn)
}

// ServerHandshake performs the server handshake.
func (pt *Credentials) ServerHandshake(conn net.Conn) (net.Conn, credentials.AuthInfo, error) {
	if c, ok := conn.(*Conn); ok {
		pt.isQuicConnection = true
		return conn, NewInfo(c), nil
	}
	return pt.grpcCreds.ServerHandshake(conn)
}

// Info provides the ProtocolInfo of this Credentials.
func (pt *Credentials) Info() credentials.ProtocolInfo {
	if pt.isQuicConnection {
		return credentials.ProtocolInfo{
			ProtocolVersion:  "/quic/1.0.0",
			SecurityProtocol: "quic-tls",
			ServerName:       pt.serverName,
		}
	}
	return pt.grpcCreds.Info()
}

// Clone makes a copy of this Credentials.
func (pt *Credentials) Clone() credentials.TransportCredentials {
	return &Credentials{
		tlsConfig:  pt.tlsConfig.Clone(),
		grpcCreds:  pt.grpcCreds.Clone(),
		serverName: pt.serverName,
	}
}

// OverrideServerName overrides the server name used to verify the hostname.
func (pt *Credentials) OverrideServerName(name string) error {
	pt.serverName = name
	return pt.grpcCreds.OverrideServerName(name)
}

// NewQuicDialer creates a grpc.WithContextDialer-compatible dialer for QUIC.
func NewQuicDialer(tlsConf *tls.Config, quicConfig *quic.Config) func(context.Context, string) (net.Conn, error) {
	return func(ctx context.Context, target string) (net.Conn, error) {
		conn, err := quic.DialAddr(ctx, target, tlsConf, quicConfig)
		if err != nil {
			return nil, err
		}
		return NewConn(ctx, conn)
	}
}
