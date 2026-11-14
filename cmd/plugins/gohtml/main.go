//go:build wasip1

package main

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"unsafe"

	"github.com/karl-tye/ktml/internal/ast"
	"github.com/karl-tye/ktml/internal/formatter"
	"github.com/karl-tye/ktml/internal/plugin/gohtml"
)

//go:wasmimport env ktml_host_write_file
func ktml_host_write_file(pathPtr uint32, pathLen uint32, contentPtr uint32, contentLen uint32)

//go:wasmimport env ktml_host_proxy_lsp
func ktml_host_proxy_lsp(targetPtr uint32, targetLen uint32, payloadPtr uint32, payloadLen uint32) uint64

type Output struct {
	Content    string   `json:"content"`
	OutputPath string   `json:"output_path"`
	Errors     []string `json:"errors,omitempty"`
}

var buf []byte

//go:wasmexport allocate
func allocate(size uint32) uint32 {
	buf = make([]byte, size)
	return uint32(uintptr(unsafe.Pointer(&buf[0])))
}

//go:wasmexport ktml_configure
func ktml_configure(settingsPtr uint32, settingsLen uint32) {
	settings := unsafe.Slice((*byte)(unsafe.Pointer(uintptr(settingsPtr))), settingsLen)
	if err := gohtml.Configure(settings); err != nil {
		panic(err)
	}
}

//go:wasmexport ktml_manifest
func ktml_manifest() uint64 {
	manifest := `{"name":"gohtml","version":"1.1.0","abi":2,"capabilities":["generate","format","completion","semantic-tokens","symbol-index"]}`
	return packString(manifest)
}

func processFile(inAST, inCfg []byte) []byte {
	var doc ast.Document
	var errs []string

	if err := json.Unmarshal(inAST, &doc); err != nil {
		errs = append(errs, err.Error())
	}

	var cfg struct {
		Environment string         `json:"environment"`
		FilePath    string         `json:"file_path"`
		ProjectRoot string         `json:"project_root"`
		Settings    map[string]any `json:"settings"`
		DevMode     bool           `json:"dev_mode"`
	}
	if err := json.Unmarshal(inCfg, &cfg); err != nil {
		errs = append(errs, err.Error())
	}

	var content string
	outPath, _ := strings.CutSuffix(cfg.FilePath, ".ktml")
	outPath += ".html"
	if relativePath, err := filepath.Rel(cfg.ProjectRoot, outPath); err == nil {
		outPath = relativePath
	} else {
		errs = append(errs, err.Error())
	}

	if len(errs) == 0 {
		var generateErr error
		content, generateErr = gohtml.GenerateChecked(&doc, cfg.DevMode, cfg.FilePath, func(path string, data []byte) {
			path, err := filepath.Rel(cfg.ProjectRoot, path)
			if err != nil {
				errs = append(errs, err.Error())
				return
			}
			pathPtr := uint32(uintptr(unsafe.Pointer(unsafe.StringData(path))))
			pathLen := uint32(len(path))
			var contentPtr uint32
			if len(data) > 0 {
				contentPtr = uint32(uintptr(unsafe.Pointer(&data[0])))
			}
			contentLen := uint32(len(data))
			ktml_host_write_file(pathPtr, pathLen, contentPtr, contentLen)
		})
		if generateErr != nil {
			errs = append(errs, generateErr.Error())
		}
	}

	p := Output{
		Content:    content,
		OutputPath: outPath,
		Errors:     errs,
	}

	res, _ := json.Marshal(p)
	return res
}

//go:wasmexport ktml_generate
func ktml_generate(astPtr uint32, astSize uint32, cfgPtr uint32, cfgSize uint32) uint64 {
	inAST := unsafe.Slice((*byte)(unsafe.Pointer(uintptr(astPtr))), astSize)
	inCfg := unsafe.Slice((*byte)(unsafe.Pointer(uintptr(cfgPtr))), cfgSize)

	out := processFile(inAST, inCfg)
	return packString(string(out))
}

//go:wasmexport ktml_format
func ktml_format(astPtr uint32, astSize uint32) uint64 {
	inAST := unsafe.Slice((*byte)(unsafe.Pointer(uintptr(astPtr))), astSize)
	var doc ast.Document
	if err := json.Unmarshal(inAST, &doc); err != nil {
		return 0
	}
	formatted := formatter.Format(&doc)
	return packString(formatted)
}

//go:wasmexport ktml_lsp_handle
//go:wasmexport ktml_lsp_handle
func ktml_lsp_handle(methodPtr uint32, methodSize uint32, payloadPtr uint32, payloadSize uint32) uint64 {
	methodBytes := unsafe.Slice((*byte)(unsafe.Pointer(uintptr(methodPtr))), methodSize)
	payloadBytes := unsafe.Slice((*byte)(unsafe.Pointer(uintptr(payloadPtr))), payloadSize)

	resp, err := gohtml.HandleLSP(string(methodBytes), payloadBytes)
	if err != nil || len(resp) == 0 {
		return 0
	}
	return packString(string(resp))
}

func packString(s string) uint64 {
	b := []byte(s)
	if len(b) == 0 {
		return 0
	}
	ptr := uint32(uintptr(unsafe.Pointer(&b[0])))
	size := uint32(len(b))
	return (uint64(ptr) << 32) | uint64(size)
}

func main() {
}
