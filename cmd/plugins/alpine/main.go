//go:build wasip1

package main

import (
	"unsafe"

	"github.com/karl-tye/ktml/internal/plugin/alpine"
)

var buffer []byte

//go:wasmexport allocate
func allocate(size uint32) uint32 {
	if size == 0 {
		buffer = nil
		return 0
	}
	buffer = make([]byte, size)
	return uint32(uintptr(unsafe.Pointer(&buffer[0])))
}

//go:wasmexport ktml_manifest
func ktml_manifest() uint64 {
	return packString(`{"name":"alpine","version":"0.2.0","abi":2,"capabilities":["completion","embedded-language","symbol-index"]}`)
}

//go:wasmexport ktml_lsp_handle
func ktml_lsp_handle(methodPtr uint32, methodLen uint32, payloadPtr uint32, payloadLen uint32) uint64 {
	method := unsafe.Slice((*byte)(unsafe.Pointer(uintptr(methodPtr))), methodLen)
	payload := unsafe.Slice((*byte)(unsafe.Pointer(uintptr(payloadPtr))), payloadLen)
	response, err := alpine.HandleLSP(string(method), payload)
	if err != nil || len(response) == 0 {
		return 0
	}
	return packString(string(response))
}

func packString(value string) uint64 {
	result := []byte(value)
	if len(result) == 0 {
		return 0
	}
	pointer := uint32(uintptr(unsafe.Pointer(&result[0])))
	return uint64(pointer)<<32 | uint64(len(result))
}

func main() {}
