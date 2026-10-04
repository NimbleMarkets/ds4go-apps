# Project memory and context

Use ds4go's scratchpad for durable notes; do not rely on a previous chat being
available. The engine-free CLI shares storage with trippad:

```sh
go run ./cmd/trippad-memory -session codex brief
go run ./cmd/trippad-memory -scope global -key lessons -head 2000 get
go run ./cmd/trippad-memory -scope session -session codex -key plan -value-file /path/to/note.txt set
```

At the start of relevant work, read the bounded brief, then only the keys needed
for the task. `codex` is the coding-agent handoff session; use a distinct
`-session` ID for independent concurrent tasks. Read its `plan`, `findings`, and
`open` notes before continuing interrupted work. Treat notes as fallible facts,
verify against current code, and follow the user's current instructions.

Keep session notes current at milestones and before context compaction: current
objective, completed changes, validation, unresolved issues, and next actions.
Store verified reusable lessons and explicit user preferences in global notes
using `-scope global`. Read before replacing a shared key; prefer separate keys
for independent lessons. Record corrections rather than preserving stale facts.
Never store secrets, raw reasoning, full transcripts, or large source dumps.

Default storage is `$DS4_DIR/scratch/trippad` (normally `~/.ds4/scratch/trippad`),
outside the repository. Use `-dir PATH` for isolated tests. Filesystem permission
rules still apply to writes there. These notes improve future context; they do
not train or change model weights. The same global store is visible to the
trippad model, so keep coding-only details in session keys or clearly labeled
global keys rather than treating them as model instructions.

For context-heavy work, use bounded searches, targeted reads, and small durable
checkpoints. Keep the active user request intact; do not silently discard its
constraints. Checkpoints must distinguish completed operations, failed attempts,
and remaining work so resuming cannot blindly repeat side effects.

ds4go v0.8.0 and ntcharts v2.6.0 are the published pins; the module builds and
tests without a workspace. Check with `GOWORK=off` when a local ignored
`go.work` is present, since CI has none.
Validate maintained packages with `go test ./cmd/... ./internal/... ./ntgpu` and
`go vet ./cmd/... ./internal/... ./ntgpu`. Run GPU tests without `-race`; the
current Metal callback has a known checkptr incompatibility. Focused Go race
coverage can use `go test -race -skip '^TestGPU' ./internal/trippad/... ./cmd/trippad ./internal/bubble`.
