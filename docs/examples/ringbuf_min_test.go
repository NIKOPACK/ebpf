//go:build linux

package examples

import (
	"errors"
	"testing"
	"time"

	"github.com/cilium/ebpf"
	"github.com/cilium/ebpf/asm"
	"github.com/cilium/ebpf/ringbuf"
)

// This is a minimal, self-contained ringbuf test pattern. It doesn't include
// the eBPF helper call to write to the ring, but shows the test-side reader
// structure, timeouts, and cleanup. Replace the program body with one that
// calls bpf_ringbuf_output to make it fully functional.
func TestRingbufPattern(t *testing.T) {
	m, err := ebpf.NewMap(&ebpf.MapSpec{Type: ebpf.RingBuf, MaxEntries: 4096})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Close() })

	prog, err := ebpf.NewProgram(&ebpf.ProgramSpec{
		Type: ebpf.SocketFilter,
		Instructions: asm.Instructions{
			asm.LoadImm(asm.R0, 0, asm.DWord),
			asm.Return(),
		},
		License: "MIT",
	})
	if err != nil {
		// On older kernels or without caps, test run may be unsupported.
		if errors.Is(err, ebpf.ErrNotSupported) { t.Skip("unsupported") }
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = prog.Close() })

	rd, err := ringbuf.NewReader(m)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rd.Close() })

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = rd.Read() // would block until sample or close
	}()

	in := make([]byte, 14)
	_, _, runErr := prog.Test(in)
	if errors.Is(runErr, ebpf.ErrNotSupported) { t.Skip("prog test run not supported") }
	if runErr != nil { t.Fatal(runErr) }

	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		// No event expected since program doesn't write to ring; just ensure no deadlock.
	}
}
