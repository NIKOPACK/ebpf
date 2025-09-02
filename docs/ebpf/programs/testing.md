# Testing eBPF Programs with ebpf-go

This page collects practical guidance and examples for testing eBPF maps and programs using the ebpf-go library. It focuses on fast local tests first and escalates to integration tests only when necessary.

## What you can test

- Program execution via Program.Test / Program.Run / Program.Benchmark
- Map CRUD operations (Put, Get, Update, Iterate)
- CollectionSpec loading, assignment, and map replacements
- Data sections (.data/.rodata) sharing across collections
- Feature-based skips for portability

## Quick start

Most unit-like tests use Program.Test to run an eBPF program with a small input buffer. The kernel must support BPF_PROG_RUN (≥ 4.12). Always handle feature unavailability.

```go
ret, out, err := prog.Test(in)
if errors.Is(err, ebpf.ErrNotSupported) { t.Skip("prog test run not supported") }
if err != nil { t.Fatal(err) }
_ = ret
_ = out
```

For richer control, use Program.Run with RunOptions (repeat count, CPU, context buffers) or Program.Benchmark for timing.

## Patterns from the repository

- Map replacement before load
  - Replace spec-defined maps with pre-created maps to share state or control lifetimes.
- Assign into structs
  - Use LoadAndAssign/Assign to bind objects to typed fields and transfer ownership.
- Defensive cleanup
  - Ensure object FDs are closed on failures; tests assert that on errors, previously opened FDs are cleaned up.

## Minimal examples

### 1) Run a tiny SocketFilter program

```go
spec := &ebpf.ProgramSpec{
    Type: ebpf.SocketFilter,
    Instructions: asm.Instructions{
        asm.LoadImm(asm.R0, 42, asm.DWord),
        asm.Return(),
    },
    License: "MIT",
}
prog, err := ebpf.NewProgram(spec)
if err != nil { t.Fatal(err) }
t.Cleanup(func(){ _ = prog.Close() })

in := make([]byte, 14) // SKB/XDP often require >=14 bytes
ret, _, err := prog.Test(in)
if errors.Is(err, ebpf.ErrNotSupported) { t.Skip("prog test run not supported") }
if err != nil { t.Fatal(err) }
if ret != 42 { t.Fatalf("want 42, got %d", ret) }
```

### 2) Testing maps

```go
m, err := ebpf.NewMap(&ebpf.MapSpec{Type: ebpf.Array, KeySize:4, ValueSize:4, MaxEntries:1})
if err != nil { t.Fatal(err) }
t.Cleanup(func(){ _ = m.Close() })

if err := m.Put(uint32(0), uint32(7)); err != nil { t.Fatal(err) }
var v uint32
if err := m.Lookup(uint32(0), &v); err != nil { t.Fatal(err) }
if v != 7 { t.Fatalf("want 7, got %d", v) }
```

### 3) CollectionSpec with map replacement

```go
cs := &ebpf.CollectionSpec{
    Maps: map[string]*ebpf.MapSpec{
        "test-map": { Type: ebpf.Array, KeySize:4, ValueSize:4, MaxEntries:1 },
    },
    Programs: map[string]*ebpf.ProgramSpec{
        "p": {
            Type: ebpf.SocketFilter,
            Instructions: asm.Instructions{
                asm.LoadMapPtr(asm.R1, 0).WithReference("test-map"),
                asm.Mov.Reg(asm.R2, asm.R10),
                asm.Add.Imm(asm.R2, -4),
                asm.StoreImm(asm.R2, 0, 0, asm.Word),
                asm.FnMapLookupElem.Call(),
                asm.JEq.Imm(asm.R0, 0, "error"),
                asm.LoadMem(asm.R0, asm.R0, 0, asm.Word),
                asm.Ja.Label("ret"),
                asm.Mov.Imm(asm.R0, 0).WithSymbol("error"),
                asm.Return().WithSymbol("ret"),
            },
            License: "MIT",
        },
    },
}
// Prepare replacement map
m, _ := ebpf.NewMap(cs.Maps["test-map"]) 
_ = m.Put(uint32(0), uint32(2))
coll := testMustNewCollection(t, cs, &ebpf.CollectionOptions{ MapReplacements: map[string]*ebpf.Map{"test-map": m} })
in := make([]byte, 14)
ret, _, err := coll.Programs["p"].Test(in)
if errors.Is(err, ebpf.ErrNotSupported) { t.Skip("prog test run not supported") }
if err != nil { t.Fatal(err) }
if ret != 2 { t.Fatal("replacement not used") }
```

Note: `testMustNewCollection` is a stand-in for your test helper. In the ebpf repository, similar helpers exist under internal/testutils for project tests.

## Skipping and portability

- Use feature probes (features package) or helper `testutils.SkipIfNotSupported(t, err)` to skip gracefully on older kernels or unsupported program types.
- Prefer deterministic inputs; when relying on kernel events, use ringbuffer/perf with timeouts and small fixtures.

## Benchmarks

Use Program.Benchmark to measure kernel execution time. Keep repeats reasonable; report metrics via testing.B.ReportMetric. Avoid flaking due to CPU frequency scaling—pin CPU if needed via RunOptions.CPU.

## Common pitfalls

- Always close FDs (Program/Map/Link); on assignment failures ensure cleanup is performed.
- Some program types expect a minimum input size (e.g., 14 bytes for SKB/XDP). Pass a properly sized buffer when using Test/Run.
- DataOut size for Test is best-effort; if program modifies packet head, allocate a bit of padding.
- Map spec mismatches during replacement should return ErrMapIncompatible—assert this in tests.

## See also

- Repository tests (search for `*_test.go`): real patterns for collections, maps, links.
- Concepts: Feature Detection, Object Lifecycle, Loader.
- Guides: Getting Started, Portable eBPF.

---

## Integration examples: ringbuf, perf, link

下面示例以「测试范式」为主，侧重结构与时序，具体 eBPF 指令可参考仓库内对应测试文件（ringbuf/reader_test.go、perf/reader_test.go）。

### RingBuffer（环形缓冲）

目标：程序向 ringbuf 写入事件；测试侧用 ringbuf.Reader 读取并断言。

步骤要点：
- 创建 Type=RingBuf 的 map，MaxEntries 设置为页大小倍数（如 4096/8192）。
- 启动 ringbuf.Reader，并用 goroutine 接收；为读取设置超时，避免测试阻塞。
- 触发程序执行（可用 Program.Test，或实际 attach 后触发内核路径）。

示例骨架：

```go
m, err := ebpf.NewMap(&ebpf.MapSpec{Type: ebpf.RingBuf, MaxEntries: 4096})
if err != nil { t.Fatal(err) }
t.Cleanup(func(){ _ = m.Close() })

rd, err := ringbuf.NewReader(m)
if err != nil { t.Fatal(err) }
t.Cleanup(func(){ _ = rd.Close() })

done := make(chan struct{})
go func(){
    defer close(done)
    for {
        rec, err := rd.Read()
        if err != nil {
            if errors.Is(err, ringbuf.ErrClosed) { return }
            t.Errorf("ringbuf read: %v", err); return
        }
        // 断言事件内容
        _ = rec.RawSample
        return
    }
}()

// 执行会向 ringbuf 写事件的 eBPF 程序：
// ret, _, err := prog.Test(input)
// if errors.Is(err, ebpf.ErrNotSupported) { t.Skip("no test run") }
// if err != nil { t.Fatal(err) }

select {
case <-done:
case <-time.After(2*time.Second):
    t.Fatal("ringbuf read timeout")
}
```

提示：在最小化示例中，程序端需要调用 bpf_ringbuf_output 提交样本。

### Perf Event（perf ring）

目标：程序向 PERF_EVENT_ARRAY 写入事件；测试侧用 perf.Reader 读取并断言。

步骤要点：
- 建立 PERF_EVENT_ARRAY map，并在程序中写入 perf 事件。
- 测试中创建 perf.Reader，设置合适的读取大小与超时。

示例骨架：

```go
m, err := ebpf.NewMap(&ebpf.MapSpec{Type: ebpf.PerfEventArray, KeySize:4, ValueSize:4, MaxEntries:1})
if err != nil { t.Fatal(err) }
t.Cleanup(func(){ _ = m.Close() })

rd, err := perf.NewReader(m, 4096)
if err != nil { t.Fatal(err) }
t.Cleanup(func(){ _ = rd.Close() })

// 触发程序写事件，例如 prog.Test / 实际挂载后触发

ev, err := rd.Read()
if err != nil { t.Fatal(err) }
_ = ev.RawSample // 断言内容
```

### 链接（link）attach/detach

目标：验证程序挂载点可创建并能正常释放；对不支持的内核要优雅跳过。

范式：

```go
lk, err := link.RawTracepoint(link.RawTracepointOptions{Program: prog, Name: "sched_switch"})
if errors.Is(err, ebpf.ErrNotSupported) { t.Skip("raw tracepoint unsupported") }
if err != nil { t.Fatal(err) }
t.Cleanup(func(){ _ = lk.Close() })

// 可配合事件路径（ringbuf/perf）断言收到一次事件
```

注：不同内核/配置下可选择不同 attach 方式（kprobe/fentry/raw_tracepoint 等）。对常见失败（权限不足、符号不存在、PMU 不可用）用 ErrNotSupported 分支处理。

---

## CI 与稳定性建议

- 条件跳过：对特性缺失用 `errors.Is(err, ebpf.ErrNotSupported)` 跳过，不要误判为失败。
- 超时与资源回收：异步读取（ringbuf/perf）必须设置超时；所有 Program/Map/Link/Reader 均需 Close。
- 输入最小长度：SKB/XDP 的 Test 输入至少 14 字节，避免 EINVAL。
- 降低抖动：基准测试固定 CPU（RunOptions.CPU），使用 testing.B.ReportMetric；避免把 IO/读取计入核内时间。
- 环境要求：
  - 内核 ≥ 4.12 才支持 PROG_TEST_RUN；
  - 权限（CAP_BPF/CAP_SYS_ADMIN）或 BPF LSM 配置可能影响 attach；
  - 容器环境需挂载 bpffs、允许 bpf（或特权模式）；
  - Windows 测试与 Linux 行为差异大，参考 Guides/Windows。

交叉引用：
- Concepts/Feature Detection：为不同内核功能做前置探测
- Concepts/Object Lifecycle：对象所有权与 FD 回收
- Concepts/Loader：从 ELF/Spec 加载到运行
