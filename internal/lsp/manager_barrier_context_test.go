package lsp

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestManager_GIVENSameTargetLifecycleBarrier_WHENCallerContextEnds_THENReturnsPromptly(t *testing.T) {
	tests := []struct {
		name    string
		install func(*ExternalServer)
		context func() (context.Context, context.CancelFunc)
		want    error
	}{
		{
			name: "cleanup cancellation",
			context: func() (context.Context, context.CancelFunc) {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return ctx, cancel
			},
			want: context.Canceled,
		},
		{
			name: "retry deadline",
			install: func(server *ExternalServer) {
				server.retry = &serverCleanupTransition{done: make(chan struct{}), deadline: time.Now().Add(time.Hour)}
				close(server.stopDone)
			},
			context: func() (context.Context, context.CancelFunc) {
				return context.WithTimeout(context.Background(), time.Millisecond)
			},
			want: context.DeadlineExceeded,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			core := newManagerCore(nil)
			server := &ExternalServer{
				manager:  core,
				target:   "go",
				stopDone: make(chan struct{}),
				stopBy:   time.Now().Add(time.Hour),
			}
			if test.install != nil {
				test.install(server)
			}
			core.stopping[server] = errors.New("cleanup incomplete")
			manager := newManagerLease(core)
			ctx, cancel := test.context()
			defer cancel()

			started := time.Now()
			_, err := manager.ProxyRequest(ctx, "go", []byte(`{"jsonrpc":"2.0","id":1,"method":"test"}`))
			if !errors.Is(err, test.want) {
				t.Fatalf("ProxyRequest() error = %v, want %v", err, test.want)
			}
			if elapsed := time.Since(started); elapsed > 100*time.Millisecond {
				t.Fatalf("ProxyRequest() returned after %v, want prompt context cancellation", elapsed)
			}
			select {
			case <-server.stopDone:
				if server.retry == nil {
					t.Fatal("retry barrier was discarded when caller context ended")
				}
			default:
				// The independent cleanup barrier remains active.
			}
		})
	}
}
