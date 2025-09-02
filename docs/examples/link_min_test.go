//go:build linux

package examples

import (
	"errors"
	"testing"

	"github.com/cilium/ebpf"
	"github.com/cilium/ebpf/asm"
	"github.com/cilium/ebpf/link"
)

// Minimal attach/detach pattern using raw tracepoint. On unsupported kernels
// it should be skipped gracefully.
func TestLinkPattern(t *testing.T) {
	prog, err := ebpf.NewProgram(&ebpf.ProgramSpec{
		Type: ebpf.RawTracepoint,
		Instructions: asm.Instructions{
			asm.LoadImm(asm.R0, 0, asm.DWord),
			asm.Return(),
		},
		License: "MIT",
	})
	if err != nil { if errors.Is(err, ebpf.ErrNotSupported) { t.Skip("unsupported") }; t.Fatal(err) }
	t.Cleanup(func(){ _ = prog.Close() })

	lk, err := link.AttachRawTracepoint(link.RawTracepointOptions{Program: prog, Name: "sched_switch"})
	if errors.Is(err, ebpf.ErrNotSupported) { t.Skip("raw tracepoint unsupported") }
	if err != nil { t.Fatal(err) }
	t.Cleanup(func(){ _ = lk.Close() })
}
