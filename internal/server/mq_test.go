package server

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/go-kratos/kratos/v2/log"
)

func TestMQServer_processMessageBody(t *testing.T) {
	logger := log.NewHelper(log.NewStdLogger(io.Discard))

	tests := []struct {
		name           string
		consume        func(context.Context, []byte) error
		wantAck        int
		wantRejectRQ   int
		wantRejectDrop int
	}{
		{
			name: "success_acks",
			consume: func(context.Context, []byte) error {
				return nil
			},
			wantAck: 1,
		},
		{
			name: "handler_error_rejects_requeue",
			consume: func(context.Context, []byte) error {
				return errors.New("boom")
			},
			wantRejectRQ: 1,
		},
		{
			name: "panic_rejects_drop",
			consume: func(context.Context, []byte) error {
				panic("boom")
			},
			wantRejectDrop: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &MQServer{
				log:         logger,
				consumeFunc: tt.consume,
			}

			ackCount := 0
			rejectRQCount := 0
			rejectDropCount := 0

			s.processMessageBody(
				context.Background(),
				[]byte("msg"),
				func() error { ackCount++; return nil },
				func() error { rejectRQCount++; return nil },
				func() error { rejectDropCount++; return nil },
			)

			if ackCount != tt.wantAck {
				t.Fatalf("ackCount=%d want=%d", ackCount, tt.wantAck)
			}
			if rejectRQCount != tt.wantRejectRQ {
				t.Fatalf("rejectRequeueCount=%d want=%d", rejectRQCount, tt.wantRejectRQ)
			}
			if rejectDropCount != tt.wantRejectDrop {
				t.Fatalf("rejectDropCount=%d want=%d", rejectDropCount, tt.wantRejectDrop)
			}
		})
	}
}
