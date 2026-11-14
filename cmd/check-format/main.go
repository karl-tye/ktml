package main

import (
	"fmt"

	"github.com/karl-tye/ktml/internal/formatter"
	"github.com/karl-tye/ktml/internal/parser"
)

func main() {
	input := "Component [id:   string, disabled:   bool] {\n\tdiv\n\t\tclass \"hello\"\n\t\tid    \"{id}\"\n}\n"
	doc := parser.New([]byte(input)).ParseDocument()
	out := formatter.Format(doc)
	fmt.Printf("OUTPUT:\n%q\n", out)
}
