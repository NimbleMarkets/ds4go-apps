# trippad — live psychedelic shader playground

Animate WGSL compute shaders on the GPU, tune their parameters with sliders, and ask a local LLM to rewrite them. Trippad shares cadpad's GPU executor, terminal picture widget, and ds4go generation stack.

## Quick start

```sh
go build -o bin/ds4go-trippad ./cmd/trippad
./bin/ds4go-trippad --no-engine
./bin/ds4go-trippad # choose an installed model before loading
./bin/ds4go-trippad --model MODEL_ALIAS_OR_GGUF
./bin/ds4go-trippad --no-engine --preset my-shader.trip.json
```

Taskfile equivalents: `task build:trippad` and `task run:trippad -- --no-engine`.

A hardware GPU supported by gogpu is required. Kitty graphics are detected automatically; other terminals use colored glyphs. Four built-in shaders are included: plasma, kaleidoscope, tunnel, and fbm-warp. Animation targets 60 fps (`--fps 1..60`), skips work when a frame is still pending, and reduces GPU resolution for slow renders. The GPU raster and Kitty upload use the same reduced dimensions, avoiding CPU upscaling and encoding extra pixels. Rendering pauses while a dialog covers the viewport; animation time continues. Downscaling preserves the exact terminal-cell aspect ratio and integer source rows; independently rounding width and height can cause padding and horizontal seams in virtual image placements. Some font sizes permit less reduction (coprime cell dimensions require full resolution), so the 1536×1024 raster target is a soft budget in those cases.

Without `--model`, the installed-model picker opens before loading an engine. An explicit `--model` loads directly. The model bar shows the catalog alias (or custom filename), readiness/loading state, and text/vision capability. Vision companions are detected automatically. Text-only models can edit shaders and controls; `trip_preview` is offered only when an image encoder is loaded.

The workspace uses svgpad-style borders around the animation, parameters,
thinking/output, tool log, and prompt. Purple marks active controls; blue marks
the focused prompt. Activity panes show the latest output; Ctrl+T and Ctrl+N
open the full scrollable views. F1 or `?` opens the shared help dialog.

Press **Ctrl+F** (or `f` outside the prompt), or enter `/fullscreen`, for an
animation-only view that fills the terminal with no borders or status bars.
**Esc** or **Ctrl+F** restores the workspace, including draft prompt and focus,
without canceling generation. Space still pauses/resumes; `q` or Ctrl+C quits.
Other editing keys are ignored while fullscreen, and explicitly opened dialogs
return to fullscreen when dismissed. Resizing redraws even while paused.
Start directly in this mode with `--fullscreen`, e.g.:

```sh
./bin/ds4go-trippad --no-engine --fullscreen
```

## Browser playground

```sh
# Serve the current terminal shader alongside trippad. Open the printed URL.
./bin/ds4go-trippad --no-engine --web --preset my-shader.trip.json
# Browser alone: no native GPU, model, or scratchpad initialization.
./bin/ds4go-trippad --web-only --preset my-shader.trip.json
# Use another local port (or :0 to choose a free port).
./bin/ds4go-trippad --web-only --web=127.0.0.1:8090
```

`--web` defaults to `http://127.0.0.1:8080/` and serves embedded HTML/JS/CSS,
without a JavaScript build step or external dependencies. Only loopback bindings
are accepted. Use a browser with WebGPU enabled; localhost works as a secure
context. The server stops with trippad. `--web-only` is useful even on a machine
where the native renderer cannot initialize; shader compilation happens in the
browser and errors appear on the page.

**Load live state** copies the active shader, current parameter values, clock,
render dimensions, and target FPS. Browser sliders use the same names, ranges,
steps and defaults. They are independent after loading; press the button again
to copy later terminal/model edits. The browser clock also runs independently.
The top bar contains Gallery, Load live state, Pause/Play and Reset time. The
sidebar contains only parameter sliders and a compact FPS/resolution readout.
The **Gallery** button browses saved versions from `--gallery-dir` (default
`trippad-gallery`) and all four built-in starters. The overview groups saved versions by shader name (ignoring case and extra
whitespace), with one preview and version count per shader. **Load latest**
loads the newest compatible version; **View versions** opens that shader’s
history, ordered newest first. **All shaders** returns to the previous search
and page. Search matches names, dates and historical version IDs. Each history
card restores its exact shader and saved control values. Built-in starters
remain separate from saved histories. Existing preset files have no session
IDs, so grouping uses names rather than guessing session boundaries. Refresh
picks up new archived versions.
Thumbnails render sequentially at 160×96 and t=2s; the main browser animation
pauses while the gallery is open, and its performance counters reset on return.
**Include shaders with compiler errors** is unchecked by default. The gallery
checks browser compiler compatibility and hides failed versions; check the box
to inspect their diagnostics. Results are cached by immutable version ID for
the life of the page. Malformed preset files are reported and skipped. The gallery is read-only and does not change the terminal
shader. Browser-only mode can browse starters even before the gallery directory
exists; use `--gallery-dir .` to browse presets saved in the current directory.
Browser-only mode starts at 640×480. The browser uses the loaded state’s
resolution and the CLI `--fps` target; there are no comparison settings in the
sidebar. The page shows the full compute WGSL for inspection.

The browser dispatches the **exact `shader.BuildWGSL` compute module** with the
same 96-byte uniform layout, 8×8 workgroups, UV orientation, helpers and packed
RGBA8 output. A separate fullscreen pass displays that storage buffer directly
on the GPU. This is JavaScript + WebGPU, with no Go/WASM or per-frame pixel
readback. Floating-point output can still vary across GPU/compiler backends.

The FPS readout counts submitted frames and is limited by the display refresh
rate and `--fps`; it does not measure actual display latency. At most two GPU
submissions are in flight. Hidden tabs and the gallery suspend main rendering.
Browser CPU/GPU timing queries and periodic native telemetry polling are omitted.

For deeper native profiling, `/api/state` still exposes the latest native FPS,
render/readback duration, Kitty encoding duration, upload bytes and the
transport the last frame used (`png`, `rgba` or `shm`). With `shm` the upload
bytes are only the reference written to the terminal; the pixels travel through
shared memory. These are
wall times; glyph encoding and terminal transport/display are not measured.
The native path allocates dispatch buffers, waits for the GPU, reads pixels into
Go, then prepares/PNG-encodes frames for the terminal. Browser presentation keeps
the pixels on the GPU, so comparisons measure the whole presentation path.

WebGPU API reference: [W3C WebGPU specification](https://www.w3.org/TR/webgpu/).

## Keys

| Key | Action |
| --- | --- |
| ↑ / ↓ | Select parameter |
| ← / → | Nudge by one step |
| Shift+← / Shift+→ | Nudge by ten steps |
| `[` / `]` | Previous / next starter shader |
| Space | Play / pause |
| `R` | Randomize controls |
| Ctrl+O | Open the shared model picker, including while editing a prompt |
| Ctrl+N | Open native diagnostics and application logs; arrows/PageUp/PageDown scroll, Esc closes |
| Ctrl+T / `T` | Show live thinking, replies, and tool activity for the latest request |
| Ctrl+L / `L` | Inspect current source: shader body, full WGSL, or preset JSON; ←/→ or Tab switches tabs |
| Ctrl+R | Cycle reasoning off → high → max for the next request |
| Ctrl+Y | Copy the active thinking/source view to the terminal clipboard |
| F3 | Browse saved shader versions with search and previews; Enter loads, Esc closes |
| Tab / `e` / Enter | Focus prompt (Tab returns to controls) |
| `/` | Focus prompt for a slash command |
| Enter in prompt | Submit |
| Esc | Cancel generation, blur prompt, dismiss help |
| F1 / `?` | Open scrollable help |
| Ctrl+F / `f` | Toggle animation-only fullscreen (`f` when not typing) |
| `q` / Ctrl-C | Quit (`q` types normally in the prompt) |

## Slash commands

These work without a model, except `/model`:

```text
/help
/pause
/play
/randomize
/set speed 0.8
/preset kaleidoscope
/describe
/save my-shader
/load my-shader.trip.json
/gallery
/fullscreen
/memory
/compact
/model
/quit
```

`/model` or Ctrl+O opens the same searchable model picker as the other pads, with the active model marked `[current]`. Enter switches models; Esc dismisses the picker and preserves prompt text/focus. Switching closes the old session and engine before loading the replacement, preserves the shader and controls, and carries recent user intent into a fresh conversation. Loading uses the shared animated loading indicator. Wait for generation to finish (or cancel with Esc) before switching; another load cannot start while one is in progress. A failed load leaves the shader intact and Ctrl+O available to retry. `--no-engine` keeps model loading disabled.

Save/load paths may contain spaces; the entire remainder of the command is the filename. The `.trip.json` suffix is appended when omitted.

## Shader history and gallery

Every successful shader replacement is saved automatically in `trippad-gallery/` beneath the working directory. This includes intermediate versions produced by the model, starter changes, and imported presets. Before replacement, the previous shader and its current control values are also saved. Current controls are checkpointed when opening the gallery and on clean exit. A failed compile does not enter history; a failed history write prevents replacing the working shader and reports the error. Exact duplicates share one file, and previous versions are never overwritten.

Press **F3** or enter `/gallery` to search by name, date, or file ID. Arrow keys select a version and show a rendered thumbnail at two seconds without changing the live shader. **Enter** compiles and restores its source and controls; **Esc** returns without changing the workspace or pending prompt. Restoring waits until generation or another preset operation finishes. The library persists across restarts; reopening F3 refreshes the list. Use `--gallery-dir PATH` to choose another location, or `/save FILE` to export a named preset. Gallery files are ordinary `.trip.json` presets. Previous unsaved sessions cannot be recovered automatically.

Logs, thinking, memory, help, and source use a shared pager spanning the terminal width,
with a visible line range and following/more-below indicator. Ctrl+T and Ctrl+L work
while editing a prompt and during generation; Esc or `q` closes the pager and preserves
prompt text, cursor, and focus. Arrows or `j`/`k` scroll a line, Space/`b` or
PageDown/PageUp scroll a page, Ctrl+D/Ctrl+U scroll half a page, `g`/Home jumps to the
beginning, and `G`/End follows new output. Paging up holds your position as output arrives.
The thinking view retains the latest request's streamed output after completion or
cancellation (up to 20 rounds, with long text bounded). Reasoning starts off, as in the
other pads; Ctrl+R enables it for the next request without changing an active run.
Replies and tool activity remain visible when reasoning is off. Source tabs show the
current successfully compiled shader and live parameter values, with WGSL and JSON
syntax highlighting using cadpad's Chroma palette. Highlighting is cached between
source/width changes. Ctrl+Y copies source without display colors or wrapping.

Go dependency diagnostics, including the language server's stderr, are captured
in the same log pane and `trippad.log`. During shutdown, trippad suppresses the
LSP client's expected closed-stderr-pipe message; other read errors remain logged.
Timestamped language-server messages retain their reported severity, so an
`INFO Initialized` line is informational. A chunk containing multiple messages
uses the highest reported severity; unrecognized output remains visible as an error.

Trippad allocates **131,072 tokens (128K)** by default. The configured allocation
appears before the first prompt; afterward `prompt used/capacity tokens` shows the
last measured rendered prompt, including system instructions, tool schemas, history,
memory notes, and image tokens. Replies and future tool results need space in that
same allocation. This counter is not the model's maximum context or a live generated-token
counter. `/memory` explains usage and checkpoint counts.

Set `--ctx N` when starting trippad to override the allocation, for example
`./bin/ds4go-trippad --model glm53-q2 --ctx 262144`. Larger windows use more memory;
the supported maximum and cost depend on the model/backend. Use a smaller explicit
allocation on memory-constrained machines. Existing sessions keep their original
allocation until restarted. The former 16K default was an application setting.

## Persistent memory and context

Trippad uses ds4go's new `scratchtool` API from the updated sibling `../ds4-go`
checkout. Each launch creates a session ID. `scratch_list/get/set/append/delete`
store that session's plan, findings and open questions;
`global_scratch_list/get/set/append/delete` share reusable lessons and preferences
across all trippad sessions. Global here means this trippad memory root, shared
with the coding-agent CLI; other apps do not read it automatically. Notes are
reference data, not model training or higher-priority instructions.

Storage defaults to `$DS4_DIR/scratch/trippad` (normally
`~/.ds4/scratch/trippad`). Use `--memory-dir PATH` to isolate it and
`--memory-session ID` to resume a session's notes. Resuming notes does not restore
a shader; use the gallery or `--preset` for that. `/memory` shows the directory,
session ID, available keys, bounded notes, prompt usage and compaction count in
the same scrollable, copyable dialog as the source inspector.

The host saves `latest-request`, `last-result` and `checkpoint`. Model tools can
read these keys but cannot overwrite, append to, or delete them. The model is
instructed to maintain concise `plan`, `findings` and `open` keys and promote
verified reusable facts to global notes. A bounded brief, including the last run outcome, is refreshed before
each request and after compaction; additional keys are read on demand. Stores
use scratchtool's atomic files and limits (32 keys, 16 KiB per value, 128 KiB per
scope), plus a process lock so concurrent app/CLI appends cannot lose updates.
Avoid storing secrets, full conversations or large source files.

Before each model round, trippad drops old budget notices and retained reasoning,
keeps only the latest tool preview image, and bounds long error observations.
The thinking inspector and log remain available. At 75% prompt usage or less
than 4,608 free tokens, it attempts a durable context checkpoint. The checkpoint
preserves the latest user request verbatim, recent tool observations, a bounded
excerpt of earlier requests, session notes, and the newest assistant/tool group.
It retrieves live source via `trip_describe` rather than carrying every old shader.
These are excerpts, so older details can be omitted; keep enduring constraints in
session notes. Compaction does not reset the tool-round budget or replay edits.
Only a smaller measured prompt replaces the original. Generation is capped to
leave spare context space, and preflight stops if fewer than 1,025 tokens remain.
An oversized active request or tool group may still require a shorter request or
larger `--ctx`.

Tool rounds are separate from context capacity. The default is 20 tool-capable
rounds plus one final response-only turn. Each round can contain several tool
calls; scratchpad calls use the same budget, so notes should be updated at
meaningful milestones and independent small updates can be batched. The status
line shows the current round and switches to “Finishing” at the limit.

On the final turn, tool definitions are removed and the model is asked to
summarize confirmed changes and unfinished work. Any calls it still emits are
recorded as **NOT EXECUTED**, paired with their call IDs, and never run. If the
model cannot produce a usable final response, the app supplies a factual stopping
note. The worker saves a bounded checkpoint containing the request, current
shader/controls, final outcome, and recent tool observations without consuming a
model round. `/memory` shows the saved outcome; a new prompt can continue from
the live shader and retained history. Checkpoint write failures remain visible;
a round-limit stop is not a claim that the user's request is complete.

Use `/compact` when idle to checkpoint manually. The header reports the last
measured **prompt** tokens (including tool schemas and images, before the current
response); the log records before/after counts when automatic compaction succeeds.

Coding agents can access the same stores without loading a model or GPU:

```sh
go run ./cmd/trippad-memory -session codex brief
go run ./cmd/trippad-memory -scope global -key lessons -head 2000 get
go run ./cmd/trippad-memory -scope session -session codex -key plan -value-file note.txt set
# Operations: list, get, set, append, delete, brief. Flags precede the operation.
# -value-file - reads stdin; -dir PATH selects an isolated root.
```

Repository `AGENTS.md` describes retrieval and checkpoint habits for future coding
sessions. Global notes should be read before replacing them to avoid overwriting
another session's lessons.

## Model-facing WGSL language server

Trippad reuses ds4go's `lsp` client. Install our patched server and rebuild:

```sh
sh scripts/install-trippad-wgsl-lsp.sh
go build -o bin/ds4go-trippad ./cmd/trippad
```

The installer pins our [wgsl-analyzer v0.9.11-trippad.1 fork](https://github.com/NimbleMarkets/wgsl-analyzer/releases/tag/v0.9.11-trippad.1)
and checks its downloaded artifact's SHA-256. It selects a native Apple Silicon
or Intel macOS executable; Apple Silicon no longer requires Rosetta.
The fork fixes upstream v0.9.11's numeric lexer, which incorrectly consumed the
minus in `uv.x-0.1` and produced false argument-count/type errors in `smoothstep`
and nested `mix` calls. Lexer, parser, semantic diagnostics, and live trippad
integration tests cover subtraction, unary negatives, and signed exponents.
Both Mac builds passed live syntax/type diagnostics and completion tests.
Several newer releases tested during integration returned no useful diagnostics
for invalid WGSL, so do not substitute the latest release without running the
integration tests. Other platforms can build this tag from
`https://github.com/NimbleMarkets/wgsl-analyzer` with Rust 1.85.1 using
`cargo build --locked --release -p wgsl-analyzer`, then specify the executable.
Restart trippad after installing to replace any already-running server process.

`--wgsl-lsp auto` (default) searches beside the app, then the project's `bin/`,
then PATH. `--wgsl-lsp PATH` selects a server and `--wgsl-lsp off` disables it.
The selected path appears in the application log (Ctrl+N). The server starts on
the generation worker's first query, persists across model switches, and shuts
down with trippad. A startup probe must diagnose a deliberately invalid shader;
a silent or incompatible server reports unavailable, never a clean bill of health.

The model gets two tools:

- `trip_validate_shader`: checks current or proposed `shade_body` and `params`
  using the actual GPU compiler and, when available, the language server. Returns
  `compile_ok`, `lsp_status`, bounded diagnostics, and targeted advisory hints.
  It does not replace the live shader, alter controls, or create gallery entries.
- `trip_complete_shader`: suggests members, names and builtins at a position in
  current or proposed source. Positions are 1-based body lines and UTF-8 byte columns
  (ordinary ASCII characters each count as one); at most 25 suggestions return.
  This tool is offered only when a server executable is configured.

Diagnostics distinguish `shade_body`, generated `full_wgsl`, and translated
`backend` locations. The renderer compiler remains authoritative: the upstream
analyzer can differ in supported WGSL syntax. Shader-body line offsets account
for generated parameter accessors. Analysis uses in-memory documents, with a
fresh URI per changed candidate to avoid stale unversioned diagnostics. Results
and tool activity appear in the existing thinking/log dialogs. General hover and
document symbols are omitted because this pinned release does not implement them
for our use case. Native validation remains available when LSP is disabled.

```sh
GOCACHE=/private/tmp/ds4go-gocache go test ./internal/trippad/wgslls -count=1 -v
# Optional: test a different installed server.
TRIPPAD_TEST_WGSL_LSP=/path/to/server go test ./internal/trippad/wgslls -count=1
```

## LLM tools and shader contract

Try: “make it a melting rainbow kaleidoscope.”

Tools: `trip_validate_shader`, `trip_complete_shader` (when configured), `trip_set_shader`, `trip_list_params`, `trip_set_param`, `trip_randomize`, `trip_preview`, `trip_describe`, `trip_save_preset`, `trip_load_preset`.

`trip_set_shader` accepts `shade_body`, the body of `fn shade(uv: vec2<f32>) -> vec3<f32>`, and a required `mode`:

- `new`: requires a descriptive `name` different from the live shader and an explicit, complete `params` array (`[]` for no controls). No name or controls are inherited.
- `edit`: omitted name and definitions preserve the current name, controls and tuned values. Supplied definitions replace controls with their defaults; `[]` removes all controls.

The model decides which mode fits the request. Its instructions start a new subject from a neutral body (`return vec3<f32>(0.0);`), using `uv` and `uni.time` to build the requested design. Ambiguous requests and refinements use edit mode. The current animation stays visible until a replacement compiles and is saved successfully. Failed compilation returns the compiler's diagnostic and preserves the current shader.

Proposed bodies sent to `trip_validate_shader` or `trip_complete_shader` use the same mode contract; checking the current source without a proposed body needs no mode. Validation reports `body_matches_live`. Validation and replacement both report `parameter_usage` and warn about controls whose `p_NAME()` accessors are never called. This lexical check ignores comments; a reference alone does not prove that a control visibly affects the animation.

Replacement results report `status`, `changed`, and separate body/name/parameter-definition/value changes. An identical submission returns `status: "unchanged"` without compiling, advancing the revision, or writing gallery history. Body comparison is exact text. A name or control change can be applied while `changes.body` remains false; the model is instructed not to describe that as a shader rewrite.

- `uv`: centered and aspect corrected; vertical range −1 to 1, y increasing downward.
- Uniforms: `uni.time`, `uni.dt` (seconds), `uni.frame`, `uni.w`, `uni.h`.
- Helpers: `hash(vec2)`, `noise(vec2)`, `fbm(vec2)`, `palette(float)` (cosine rainbow), `rot2d(float)` (2×2 rotation matrix).
- Up to 16 named float controls, accessed with `p_NAME()`. Each definition has `name`, `min`, `max`, `step`, and `default`.
- Return RGB values in 0–1. Keep loops bounded for interactive GPU rendering.

Presets contain the name, shader body, parameter definitions, and named values. Loading validates and compiles before replacing the current state. Saving uses an atomic file replacement. Time and play/pause state are not saved.

## Flags

Use `--help` for all flags. Shared flags include `--model` / `-m`, `--lib`, `--ctx`, `--backend`, `--mtp`, `--debug`, `--power`, `--ssd-streaming`, `--temp`, `--top-p`, `--seed`, and `--tool-rounds`.

Trippad adds `--web[=127.0.0.1:8080]`, `--web-only`, `--no-engine`, `--fullscreen`, `--preset FILE`, `--gallery-dir PATH`, `--memory-dir PATH`, `--memory-session ID`, `--wgsl-lsp auto|off|PATH`, `--fps 1..60` (default 60), `--kitty-transport png|rgba|shm|auto` (default `png`; see the top-level README), and `--downscale 1..8` (default 2). Higher downscale values trade detail for faster rendering and smaller terminal uploads; `+`/`-` in the controls pane or `/downscale N` change it while running, and the header shows the current raster; `--fps 30` reduces CPU and terminal work. Inference defaults to a 16,384-token context, 100% power (no throttling), and 20 tool rounds plus a final response. Qwen3.8 does not support power throttling, so selecting a catalog Qwen model temporarily uses 100% even when a lower `--power` was configured; switching back restores that setting. Native diagnostics always go to `trippad.log`; `--debug` additionally logs raw LLM traffic. Failed loads open the shared log dialog automatically so the native reason is visible alongside the status code.

## Development

```sh
go build ./...
go vet ./internal/trippad/... ./cmd/trippad/...
go test ./internal/trippad/... ./cmd/trippad/...
go test -race -skip '^TestGPU' ./internal/trippad/... ./cmd/trippad/...
go test ./cmd/trippad -run '^$' -bench BenchmarkPictureEncode -benchmem
node --test internal/trippad/web/gallery-model.test.mjs
```

GPU tests skip when a hardware adapter is unavailable. They check every starter for an opaque, non-uniform, animated frame and verify all 16 uniform parameter slots. The remaining tests cover WGSL parsing/validation, layout, clamping, preset round trips, compiler feedback, PNG tool results, concurrent state access, keyboard focus, and render coalescing.

Fatal Go runtime reports are also appended to `trippad-crash.log`, preserving the faulting stack if the terminal only shows the end of a crash dump. GPU readback uses `MapAsync` and synchronous completion polling on the locked executor thread; the pinned wgpu `Buffer.Map` helper starts an unpinned worker that can crash while draining a Metal autorelease pool. A repeated-readback GPU test covers concurrent animation/preview calls under GC pressure.

Run GPU tests without `-race`: the pinned gogpu Metal callback triggers Go's `checkptr` instrumentation under `-race` on macOS/arm64. The race command above covers the Go state, tools, and UI while the normal test command covers GPU dispatch.

Implementation lives in `internal/trippad/{params,shader,tools,library,memory,wgslls,web}` and `cmd/trippad`. The renderer cache holds at most eight compiled pipelines; changing only values or time reuses the pipeline. All GPU calls and resource releases run on `ntgpu.Executor`'s locked thread.
