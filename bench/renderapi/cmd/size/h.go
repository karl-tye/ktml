//go:build sizeh

package main

import (
	"context"
	"io"

	"github.com/karl-tye/ktml"
	"github.com/karl-tye/ktml/bench/renderapi/h/cross"
	"github.com/karl-tye/ktml/bench/renderapi/h/form"
	"github.com/karl-tye/ktml/bench/renderapi/h/list"
	"github.com/karl-tye/ktml/bench/renderapi/h/nested"
	"github.com/karl-tye/ktml/bench/renderapi/h/script"
	"github.com/karl-tye/ktml/bench/renderapi/h/static"
)

func renderAll(ctx context.Context, w io.Writer) error {
	if err := (static.Static{}).Render(ctx, w); err != nil {
		return err
	}
	if err := (nested.NestedSame{Label: "leaf"}).Render(ctx, w); err != nil {
		return err
	}
	if err := (nested.NestedSlots{Label: "leaf"}).Render(ctx, w); err != nil {
		return err
	}
	if err := (cross.NestedCross{Label: "leaf"}).Render(ctx, w); err != nil {
		return err
	}
	if err := (list.ListHeavy{Entries: []list.Entry{{Id: "a", Tags: []list.Tag{{Id: 1}}}}}).Render(ctx, w); err != nil {
		return err
	}
	if err := (form.FormHeavy{Action: ktml.NewURL("/")}).Render(ctx, w); err != nil {
		return err
	}
	return script.ScriptHeavy{Links: []script.Link{{Id: 1}}}.Render(ctx, w)
}
