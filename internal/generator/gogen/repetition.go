package gogen

import (
	"bytes"
	"fmt"
	"strconv"

	"github.com/karl-tye/ktml/internal/ast"
	"github.com/karl-tye/ktml/internal/compiler"
)

// repetition writes a Keyed Repetition: a preflight that adds every key to one
// set and returns a duplicate-key error before any body writes, then a second
// pass that streams each body directly to the writer. The development variant
// also surrounds each body with Render Identity boundaries.
func (g *generator) repetition(node *ast.RepetitionNode, fieldTypes map[string]input) error {
	spelling := node.Collection.String()
	if g.snapshot == nil {
		return fmt.Errorf("keyed repetition over %s requires a workspace snapshot; use GenerateFile", spelling)
	}
	repetition, ok := g.snapshot.RepetitionFor(node)
	if !ok || repetition.KeyType == "" || repetition.Collection.Type.Element == nil {
		return fmt.Errorf("keyed repetition over %s has no validated collection and key", spelling)
	}
	collection, _, err := g.pathExpression(node.Collection, fieldTypes)
	if err != nil {
		return fmt.Errorf("keyed repetition collection: %w", err)
	}
	g.repetitionSequence++
	sequence := g.repetitionSequence
	items := fmt.Sprintf("ktmlItems%d", sequence)
	keys := fmt.Sprintf("ktmlKeys%d", sequence)
	index := fmt.Sprintf("ktmlIndex%d", sequence)
	item := items + "[" + index + "]"

	locals := make(map[string]input, len(fieldTypes)+1)
	for name, info := range fieldTypes {
		locals[name] = info
	}
	locals[node.Item] = input{name: node.Item, typ: *repetition.Collection.Type.Element, mode: ast.RequiredInput, expr: item}
	key, keyType, err := g.pathExpression(node.Key, locals)
	if err != nil {
		return fmt.Errorf("keyed repetition key: %w", err)
	}
	set, duplicate, frame := "rt.StringKeys", "rt.DuplicateStringKey", "dev.StringFrame"
	if keyType.IsScalar(compiler.IntegerType) {
		set, duplicate, frame = "rt.IntegerKeys", "rt.DuplicateIntegerKey", "dev.IntegerFrame"
	} else if !keyType.IsScalar(compiler.StringType) {
		return fmt.Errorf("keyed repetition key %s has unsupported type %s", node.Key.String(), keyType)
	}
	componentID := strconv.Quote(g.componentID(g.current))
	source := strconv.Quote(spelling)
	g.usesRuntime = true

	g.emitf("\t{\n\t%s := %s\n", items, collection)
	g.emitf("\tvar %s %s\n\t%s.Reset(len(%s))\n", keys, set, keys, items)
	g.emitf("\tfor %s := range %s {\n\t\tif !%s.Add(%s) { return %s(%s, %s, %s) }\n\t}\n", index, items, keys, key, duplicate, componentID, source, key)
	scope := g.identity
	if g.options.Development {
		g.usesDev = true
		if scope == "" {
			scope = fmt.Sprintf("ktmlScope%d", sequence)
			g.emitf("\t%s := dev.Scope(ctx)\n", scope)
		}
	}
	// The streaming body is generated aside so the loop can omit the index
	// when nothing in it reads the item.
	enclosing := g.output
	g.output = bytes.Buffer{}
	outer := g.identity
	if g.options.Development {
		identity := fmt.Sprintf("ktmlIdentity%d", sequence)
		g.emitf("\t\t%s := %s(%s, %s, %s, %s)\n", identity, frame, scope, componentID, strconv.Quote(repetition.Source), key)
		g.emitf("\t\tif err := dev.Begin(w, %s); err != nil { return rt.WriteError(%s, err) }\n", identity, componentID)
		g.identity = identity
	}
	for _, child := range node.Body {
		if err := g.node(child, locals); err != nil {
			g.output, g.identity = enclosing, outer
			g.pending.Reset()
			return err
		}
	}
	if g.options.Development {
		g.emitf("\t\tif err := dev.End(w, %s); err != nil { return rt.WriteError(%s, err) }\n", g.identity, componentID)
	}
	g.flush()
	body := g.output
	g.output, g.identity = enclosing, outer
	if bytes.Contains(body.Bytes(), []byte(item)) {
		g.emitf("\tfor %s := range %s {\n", index, items)
	} else {
		g.emitf("\tfor range %s {\n", items)
	}
	g.emitf("\t\tif err := ctx.Err(); err != nil { return rt.Canceled(%s, %s, err) }\n", componentID, source)
	g.output.Write(body.Bytes())
	g.emitln("\t}\n\t}")
	return nil
}
