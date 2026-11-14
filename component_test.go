package ktml_test

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/karl-tye/ktml"
)

type contextKey string

func TestRender_GIVENComponents_WHENRendered_THENPreservesOrderContextAndErrors(t *testing.T) {
	wantErr := errors.New("render failed")
	ctx := context.WithValue(context.Background(), contextKey("request"), "req-1")
	components := []ktml.Component{
		ktml.ComponentFunc(func(ctx context.Context, w io.Writer) error {
			_, err := io.WriteString(w, ctx.Value(contextKey("request")).(string))
			return err
		}),
		ktml.ComponentFunc(func(context.Context, io.Writer) error { return wantErr }),
		ktml.ComponentFunc(func(context.Context, io.Writer) error {
			t.Fatal("Render called a component after an error")
			return nil
		}),
	}

	var output strings.Builder
	err := ktml.Render(ctx, &output, components...)
	if !errors.Is(err, wantErr) {
		t.Fatalf("Render() error = %v, want %v", err, wantErr)
	}
	if got, want := output.String(), "req-1"; got != want {
		t.Errorf("Render() output = %q, want %q", got, want)
	}
}

func TestJoin_GIVENComponents_WHENRendered_THENRendersInOrderAndSkipsNil(t *testing.T) {
	write := func(value string) ktml.Component {
		return ktml.ComponentFunc(func(_ context.Context, w io.Writer) error {
			_, err := io.WriteString(w, value)
			return err
		})
	}
	var output strings.Builder
	if err := ktml.Join(write("a"), nil, write("b")).Render(context.Background(), &output); err != nil {
		t.Fatal(err)
	}
	if got := output.String(); got != "ab" {
		t.Errorf("Join() rendered %q, want ab", got)
	}
}

func TestRender_GIVENLaterComponentFails_WHENRendered_THENPartialCoversEarlierComponents(t *testing.T) {
	write := ktml.ComponentFunc(func(_ context.Context, w io.Writer) error {
		_, err := io.WriteString(w, "a")
		return err
	})
	fail := ktml.ComponentFunc(func(context.Context, io.Writer) error {
		return &ktml.RenderError{Kind: ktml.ErrInvalidState, Component: "app.Card"}
	})
	tests := []struct {
		name        string
		components  []ktml.Component
		wantPartial bool
	}{
		{name: "first fails", components: []ktml.Component{fail, write}, wantPartial: false},
		{name: "second fails", components: []ktml.Component{write, fail}, wantPartial: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var output strings.Builder
			err := ktml.Render(context.Background(), &output, tt.components...)
			renderError, ok := errors.AsType[*ktml.RenderError](err)
			if !ok || renderError.Partial != tt.wantPartial {
				t.Errorf("Render() = %#v, want Partial %v", err, tt.wantPartial)
			}
		})
	}
}
