//go:build sizeb

package main

import (
	"context"
	"io"

	"github.com/karl-tye/ktml"
	"github.com/karl-tye/ktml/bench/renderapi/b/cross"
	"github.com/karl-tye/ktml/bench/renderapi/b/form"
	"github.com/karl-tye/ktml/bench/renderapi/b/list"
	"github.com/karl-tye/ktml/bench/renderapi/b/nested"
	"github.com/karl-tye/ktml/bench/renderapi/b/script"
	"github.com/karl-tye/ktml/bench/renderapi/b/static"
)

func renderAll(ctx context.Context, w io.Writer) error {
	if err := static.RenderStatic(ctx, w, static.StaticProps{}); err != nil {
		return err
	}
	if err := nested.RenderNestedSame(ctx, w, nested.NestedSameProps{Label: "leaf"}); err != nil {
		return err
	}
	if err := nested.RenderNestedSlots(ctx, w, nested.NestedSlotsProps{Label: "leaf"}); err != nil {
		return err
	}
	if err := cross.RenderNestedCross(ctx, w, cross.NestedCrossProps{Label: "leaf"}); err != nil {
		return err
	}
	if err := list.RenderListHeavy(ctx, w, list.ListHeavyProps{Entries: []list.Entry{{Id: "a", Tags: []list.Tag{{Id: 1}}}}}); err != nil {
		return err
	}
	if err := form.RenderFormHeavy(ctx, w, form.FormHeavyProps{Action: ktml.NewURL("/")}); err != nil {
		return err
	}
	return script.RenderScriptHeavy(ctx, w, script.ScriptHeavyProps{Links: []script.Link{{Id: 1}}})
}
