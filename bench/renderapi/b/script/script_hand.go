//go:build !ktmldev

// HAND-LOWERED (ADR-0003 evaluation protocol). The compiler does not lower
// JSONValue or TrustedScript yet, so this file is written by hand in the exact
// shape gogen emits for API B (see script_ktml.go beside it). It lowers:
//
//	import { Link } from "./script.ktmlschema"
//
//	ScriptHeavy [id: integer, title: string, summary: string, data: JSONValue,
//	             links: [Link], boot: TrustedScript] {
//		section class="lecturenotes" data-lecturenotes="{id}" {
//			script id="lecturenotes-data" type="application/json" "{data}"
//			ScriptLinks title="{title}" summary="{summary}" links="{links}"
//			script "{boot}"
//		}
//	}
//
// Only the two script elements are hand-lowered (rt.JSONScript, rt.Script);
// ScriptLinks is generated and called statically, as gogen calls a
// same-package component.

package script

import (
	"context"
	"io"

	"github.com/karl-tye/ktml"
	"github.com/karl-tye/ktml/rt"
)

type ScriptHeavyProps struct {
	Id      int64
	Title   string
	Summary string
	Data    ktml.JSONValue
	Links   []Link
	Boot    ktml.TrustedScript
}

func RenderScriptHeavy(ctx context.Context, w io.Writer, props ScriptHeavyProps) error {
	ktmlW, ktmlOwned := rt.Wrap(w)
	return rt.Finish(ktmlW, ktmlOwned, ktmlRenderScriptHeavy(ctx, ktmlW, &props))
}
func ktmlRenderScriptHeavy(ctx context.Context, w *rt.Writer, props *ScriptHeavyProps) error {
	if err := ctx.Err(); err != nil {
		return rt.Canceled("github.com/karl-tye/ktml/bench/renderapi/b/script.ScriptHeavy", "", err)
	}
	if err := rt.Raw(w, "<section class=\"lecturenotes\" data-lecturenotes=\""); err != nil {
		return rt.WriteError("github.com/karl-tye/ktml/bench/renderapi/b/script.ScriptHeavy", err)
	}
	if err := rt.AttrInt(w, props.Id); err != nil {
		return rt.WriteError("github.com/karl-tye/ktml/bench/renderapi/b/script.ScriptHeavy", err)
	}
	if err := rt.Raw(w, "\"><script id=\"lecturenotes-data\" type=\"application/json\">"); err != nil {
		return rt.WriteError("github.com/karl-tye/ktml/bench/renderapi/b/script.ScriptHeavy", err)
	}
	if err := rt.JSONScript(w, props.Data); err != nil {
		return rt.WriteError("github.com/karl-tye/ktml/bench/renderapi/b/script.ScriptHeavy", err)
	}
	if err := rt.Raw(w, "</script>"); err != nil {
		return rt.WriteError("github.com/karl-tye/ktml/bench/renderapi/b/script.ScriptHeavy", err)
	}
	if err := ktmlRenderScriptLinks(ctx, w, &ScriptLinksProps{Title: props.Title, Summary: props.Summary, Links: props.Links}); err != nil {
		return err
	}
	if err := rt.Raw(w, "<script>"); err != nil {
		return rt.WriteError("github.com/karl-tye/ktml/bench/renderapi/b/script.ScriptHeavy", err)
	}
	if err := rt.Script(w, props.Boot); err != nil {
		return rt.WriteError("github.com/karl-tye/ktml/bench/renderapi/b/script.ScriptHeavy", err)
	}
	if err := rt.Raw(w, "</script></section>"); err != nil {
		return rt.WriteError("github.com/karl-tye/ktml/bench/renderapi/b/script.ScriptHeavy", err)
	}
	return nil
}
