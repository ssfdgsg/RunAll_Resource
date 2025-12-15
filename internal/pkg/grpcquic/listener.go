package grpcquic

import (
	"context"
	"crypto/tls"
	"net"

	quic "github.com/quic-go/quic-go"
)

var _ net.Listener = (*Listener)(nil)

// Listener adapts a QUIC listener to net.Listener for grpc-go.
type Listener struct {
	ql *quic.Listener
}

// ListenAddr starts a QUIC listener on addr and returns it as a net.Listener.
func ListenAddr(addr string, tlsConf *tls.Config, config *quic.Config) (net.Listener, error) {
	ql, err := quic.ListenAddr(addr, tlsConf, config)
	if err != nil {
		return nil, err
	}
	return Listen(ql), nil
}

// Listen wraps a QUIC listener as a net.Listener.
func Listen(ql *quic.Listener) net.Listener {
	return &Listener{ql: ql}
}

// Accept waits for and returns the next connection to the listener.
func (l *Listener) Accept() (net.Conn, error) {
	conn, err := l.ql.Accept(context.Background())
	if err != nil {
		return nil, err
	}
	stream, err := conn.AcceptStream(context.Background())
	if err != nil {
		_ = conn.CloseWithError(0, "")
		return nil, err
	}
	return newConn(conn, stream), nil
}

// Close closes the listener.
func (l *Listener) Close() error { return l.ql.Close() }

// Addr returns the listener's network address.
func (l *Listener) Addr() net.Addr { return l.ql.Addr() }
