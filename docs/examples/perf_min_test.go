//go:build linux

package examples

import (
	"errors"
	"testing"

	"github.com/cilium/ebpf"
	"github.com/cilium/ebpf/asm"
	"github.com/cilium/ebpf/perf"
)

// Minimal perf reader test pattern. Replace program body with a helper that
// writes to perf ring (e.g., bpf_perf_event_output) to make it functional.
func TestPerfPattern(t *testing.T) {
	m, err := ebpf.NewMap(&ebpf.MapSpec{Type: ebpf.PerfEventArray, KeySize: 4, ValueSize: 4, MaxEntries: 1})
	if err != nil { t.Fatal(err) }
	t.Cleanup(func(){ _ = m.Close() })

	prog, err := ebpf.NewProgram(&ebpf.ProgramSpec{
		Type: ebpf.SocketFilter,
		Instructions: asm.Instructions{ asm.LoadImm(asm.R0, 0, asm.DWord), asm.Return() },
		License: "MIT",
	})
	if err != nil { if errors.Is(err, ebpf.ErrNotSupported) { t.Skip("unsupported") }; t.Fatal(err) }
	t.Cleanup(func(){ _ = prog.Close() })

	rd, err := perf.NewReader(m, 4096)
	if err != nil { t.Fatal(err) }
	t.Cleanup(func(){ _ = rd.Close() })

	in := make([]byte, 14)
	_, _, runErr := prog.Test(in)
	if errors.Is(runErr, ebpf.ErrNotSupported) { t.Skip("prog test run not supported") }
	if runErr != nil { t.Fatal(runErr) }
}
