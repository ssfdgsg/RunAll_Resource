package grpcquic

import (
	"context"
	"net"
	"time"

	quic "github.com/quic-go/quic-go"
)

var _ net.Conn = (*Conn)(nil)

// Conn adapts a QUIC connection + stream to net.Conn for grpc-go.
type Conn struct {
	conn   *quic.Conn
	stream *quic.Stream
}

func newConn(conn *quic.Conn, stream *quic.Stream) *Conn {
	return &Conn{conn: conn, stream: stream}
}

// NewConn opens a new stream on the QUIC connection and returns it as a net.Conn.
func NewConn(ctx context.Context, conn *quic.Conn) (net.Conn, error) {
	stream, err := conn.OpenStreamSync(ctx)
	if err != nil {
		return nil, err
	}
	return newConn(conn, stream), nil
}

// Read reads data from the stream.
func (c *Conn) Read(b []byte) (n int, err error) { return c.stream.Read(b) }

// Write writes data to the stream.
func (c *Conn) Write(b []byte) (n int, err error) { return c.stream.Write(b) }

// Close closes the stream and then closes the QUIC connection.
func (c *Conn) Close() error {
	_ = c.stream.Close()
	return c.conn.CloseWithError(0, "")
}

// LocalAddr returns the local network address.
func (c *Conn) LocalAddr() net.Addr { return c.conn.LocalAddr() }

// RemoteAddr returns the remote network address.
func (c *Conn) RemoteAddr() net.Addr { return c.conn.RemoteAddr() }

// SetDeadline sets the read and write deadlines associated with the stream.
func (c *Conn) SetDeadline(t time.Time) error { return c.stream.SetDeadline(t) }

// SetReadDeadline sets the deadline for future Read calls.
func (c *Conn) SetReadDeadline(t time.Time) error { return c.stream.SetReadDeadline(t) }

// SetWriteDeadline sets the deadline for future Write calls.
func (c *Conn) SetWriteDeadline(t time.Time) error { return c.stream.SetWriteDeadline(t) }
