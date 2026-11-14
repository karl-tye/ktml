package wasm

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// readerGuest answers every LSP request with the contents of the file
// named by the method, or the error reading it.
const readerGuest = `package main

import (
	"os"
	"unsafe"
)

var keep [][]byte

func pack(b []byte) uint64 {
	if len(b) == 0 { b = []byte("-") }
	keep = append(keep, b)
	return uint64(uintptr(unsafe.Pointer(&b[0])))<<32 | uint64(len(b))
}

//go:wasmexport allocate
func allocate(size uint32) uint32 { b := make([]byte, size+1); keep = append(keep, b); return uint32(uintptr(unsafe.Pointer(&b[0]))) }

//go:wasmexport ktml_manifest
func manifest() uint64 { return pack([]byte(` + "`" + `{"name":"guest","version":"1.0.0","abi":1,"capabilities":["completion"]}` + "`" + `)) }

//go:wasmexport ktml_lsp_handle
func handle(mPtr, mLen, pPtr, pLen uint32) uint64 {
	path := string(unsafe.Slice((*byte)(unsafe.Pointer(uintptr(mPtr))), mLen))
	b, err := os.ReadFile(path)
	if err != nil { return pack([]byte("ERR:" + err.Error())) }
	return pack(append([]byte("OK:"), b...))
}

func main() {}
`

func TestNewPlugin_GIVENSymlinksOutOfAnAllowReadDirectory_WHENTheGuestReadsThem_THENTheyStayOutOfReach(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(readerGuest), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "p.wasm")
	cmd := exec.Command("go", "build", "-buildmode=c-shared", "-o", out, "main.go")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm")
	if o, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, o)
	}
	wasmBytes, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}

	outside := t.TempDir()
	secret := filepath.Join(outside, "secret.txt")
	mount := t.TempDir()
	for _, err := range []error{
		os.WriteFile(secret, []byte("TOP-SECRET"), 0o600),
		os.WriteFile(filepath.Join(mount, "inside.txt"), []byte("INSIDE"), 0o600),
		os.Symlink(secret, filepath.Join(mount, "link")),
		os.Symlink(outside, filepath.Join(mount, "dirlink")),
	} {
		if err != nil {
			t.Fatal(err)
		}
	}

	ctx := context.Background()
	p, err := NewPlugin(ctx, wasmBytes, []string{mount}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close(ctx)
	if resp, err := p.ExecuteLSP(ctx, mount+"/inside.txt", []byte(`{}`)); err != nil || !strings.Contains(string(resp), "OK:INSIDE") {
		t.Fatalf("reading a file inside the mount = %q, %v; want its contents", resp, err)
	}
	for _, path := range []string{mount + "/link", mount + "/dirlink/secret.txt", mount + "/../" + filepath.Base(outside) + "/secret.txt", secret} {
		resp, _ := p.ExecuteLSP(ctx, path, []byte(`{}`))
		if strings.Contains(string(resp), "TOP-SECRET") {
			t.Errorf("guest read outside allow_read via %s", path)
		}
	}
}
