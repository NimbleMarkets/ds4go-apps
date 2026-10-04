# ds4go-apps

Interactive local inferencing applications using [ds4go](https://github.com/NimbleMarkets/ds4go), a Golang library for the [`ds4` inferencing engine](https://github.com/antirez/ds4).  These explore using the DeepSeek v4 DF

This repository contains a family of Bubble Tea TUI tools that run DeepSeek-family models locally (Metal on macOS, CUDA on Linux, or CPU) for creative and technical tasks—from drawing SVGs and ASCII glyphs to parametric CAD modeling and neural activation steering.

---

## Applications

| App | Binary | What it does |
|-----|--------|--------------|
| **glyphpad** | `ds4go-glyphpad` | Unicode glyph / ASCII-art scratchpad. Chat with a local model to generate, edit, and arrange characters, blocks, and symbols in a canvas. |
| **svgpad** | `ds4go-svgpad` | SVG drawing scratchpad. Describe shapes, diagrams, or illustrations in natural language and get rendered SVG output directly in the terminal via `ntcharts-svg`. |
| **cadpad** | `ds4go-cadpad` | CAD modeling workbench driven by LLM tool-calling against the `simplesdf` geometry engine. Create, transform, and boolean 3D solids with live picture previews. Can also run headless (no engine) as a pure Go geometry harness. |
| **[trippad](cmd/trippad/README.md)** | `ds4go-trippad` | Live psychedelic WGSL shaders, GPU animation, parameter sliders, and LLM shader editing with optional vision previews. Runs without a model using `--no-engine`. |
| **steering** | `ds4go-steering` | DeepSeek activation-steering dashboard. Tweak FFN and attention steering vectors in real time, branch comparison timelines, and explore token logits interactively. |

---

## Prerequisites

These apps require `ds4go` to be installed, with a `ds4` dynamic library and associated model downloaded.  `ds4` requires 128G or more of GPU memory.

The apps use **ds4go v0.8.0**, which requires libds4 **v0.5.20260910** or
newer. DeepSeek V4.1 and think levels require **v0.6.20260912**; DGX Spark
(GB10) requires the **v0.6.20260913** `linux-arm64-gb10-cuda` library asset;
Qwen3.8 Flash Next and the low and medium reasoning modes require
**v0.7.20260918**.

On Linux, cadpad uses Vulkan for its GPU viewport. The Taskfile builds with
`CGO_ENABLED=0` and `-tags=nofakecgo`, sharing purego's FFI runtime with goffi;
OpenGL development headers are not needed for this build. A Vulkan-capable
graphics driver is required for GPU rendering.

---

## Build

ds4go v0.8.0 and ntcharts v2.3.0 are published, so the module builds without a
workspace.

Build everything at once:

```bash
task build
```

Or build individual apps:

```bash
task build:glyphpad
task build:svgpad
task build:cadpad
task build:steering
```

Builds use `-mod=readonly` and do not run `go mod tidy`; run `task tidy`
explicitly when updating dependencies. Task's `sources`, `generates`, and
`method: checksum` directives skip unchanged apps entirely, including the build
command. Sources include embedded assets, workspace files, and the selected
ds4go module's Go sources and module files (including a local checkout).
A `status` check also detects changes
to the Go environment, target platform, toolchain, and Git revision/status.
Each app retains its previous binary if compilation fails. Use `task --force
build` to bypass these checks, for example after changing an external local
module replacement other than ds4go. State lives in the ignored `.task/` directory.

Binaries are written to `./bin/`.

For a direct Linux CAD build without Task:

```bash
CGO_ENABLED=0 go build -tags=nofakecgo -o bin/ds4go-cadpad ./cmd/cadpad
```

Task run commands select the inference backend automatically; use
`BACKEND=cuda`, `BACKEND=metal`, or `BACKEND=cpu` to override it.

---

## Quick Start

### glyhpad — Glyph Scratchpad

```bash
task run:glyphpad
# or directly
./bin/ds4go-glyphpad --backend metal --ctx 32768
```

Type natural-language prompts to generate Unicode patterns, box-drawing diagrams, or pixel-art-style blocks. Use the modal edit box (`Ctrl+E`) to refine selections.

Glyphpad and Cadpad now share **F1** help, **F2** run settings, **Ctrl+R**
reasoning, **Ctrl+O** model selection, **Ctrl+Y** pane copying, and
**Tab/Shift+Tab** focus traversal including the prompt. Settings changed during
inference apply to the next request; loading locks settings. Engine loading is
lazy, preserves queued prompts, and shows the bicycle animation. Logs remain
available with **Ctrl+N**. Clipboard writes require OSC 52 terminal support.

Both accept `--temp` (default 0.7), `--top-p` (0.95), and `--seed` (0 chooses a
fresh seed per request). Cadpad also accepts `--tool-rounds` (default 36), with
an additional final-answer turn, and Escape cancels its current run. Model
switching preserves the drawing/program or CAD world. Cadpad's viewport copy
is a textual world description; Glyph's canvas copy is the complete glyph grid.

The remaining saved-work, visual-review, and headless rollout is tracked in
[PAD-ROLLOUT.md](PAD-ROLLOUT.md).

### svgpad — SVG Scratchpad

```bash
task run:svgpad
```

Describe an image (e.g., *“a blue circle inside a rounded rectangle”*) and the model emits SVG markup rendered live in the terminal. The engine is lazily loaded, so you can sketch offline and summon the LLM only when needed.

Drafting tools reject appends after the outer SVG root closes, preserving the draft and directing the model to edit inside the existing root. Validation reports content outside the root at the earlier closing line, so a misplaced `</svg>` can be corrected without repeatedly adding or deleting closing tags at the end of the file.

With a vision model, svgpad also reviews its rendered output: **generate → render PNG → inspect → edit → render again**. The model checks labels, clipping, spacing, contrast, and composition after the normal drafting loop. Reviews use the same renderer as the viewer, preserve the canvas proportions, and composite transparency onto white. This is model feedback, not a guarantee of visual correctness.

Visual review defaults to `auto`: it runs when the loaded engine has vision and otherwise reports that only syntax/render validation is available. The matching installed encoder is discovered through ds4go's model catalog. To require vision explicitly using the Vision-Exp model installed on a Linux/CUDA host:

```bash
task build:svgpad
./bin/ds4go-svgpad \
  --model vision-q2 \
  --visual-review on --visual-rounds 3
```

All apps accept installed catalog aliases or GGUF paths with `--model`. Alias
resolution uses ds4go's public API; a missing catalog model gets its own download
command.

In SVGPad, press **Ctrl+O** while idle to choose a different installed model.
Type to search by alias or family, use **↑/↓**, then **Enter** to load it;
**Esc** cancels. The picker shows model size and vision encoder availability.
It refreshes the catalog each time it opens and does not download models or
change the CLI's default model.

Press **Ctrl+R** to cycle reasoning while typing, without moving the prompt
cursor. The settings strip above the prompt keeps reasoning, model, vision mode,
review rounds, and tool rounds visible. A reasoning change during generation is
marked **next**; the active request and its automatic retries retain their
original setting. **F1** opens help while typing. The footer shows the focused
panel and available actions, fitting whole shortcuts onto one line.

**F2** opens run settings from the prompt or any panel: reasoning, visual-review
mode, review passes, tool rounds, and whether context is preserved between new
prompts. Use **↑/↓** to select, **←/→** to change, and **Esc** to close. Changes
last for the app session. Review and tool budgets are locked during generation,
enrichment, and engine loading; reasoning and context can be set for the next
request. Settings are read-only during model switching or engine release.
If vision is not loaded, close settings and use **Ctrl+O** to choose a vision
model; selecting the current model can reload it with its installed encoder.

**F3** opens a searchable saved-drawing browser while idle. Type to filter by
title, prompt, filename, or keywords, then press **Enter** to inspect a drawing.
Browsing preserves your pending prompt, cursor, and working `draft.svg`;
**c** continues the working draft. **Page Up/Down** now scroll or pan the focused
panel, and **j/k** scroll down/up when the prompt is not focused. **Tab** and
**Shift+Tab** cycle the visible panels and prompt.

**Ctrl+Y** copies the focused pane: prompt text, SVG source, activity text, or
Tool panel content. It copies the full pane content, including scrolled-off
lines, without terminal styling. The SVG source follows the selected drawing
or live preview, rather than reading a potentially different working draft.
The same shortcut copies logs, help, or model details when those dialogs are
open. Copying preserves focus, cursor, and scroll position; empty panes leave
the clipboard unchanged. A footer notice reports the clipboard request.
This uses OSC 52, so your terminal must allow clipboard writes; over SSH it
targets the local terminal's clipboard.

Help, settings, model details, drawings, and logs close with **Esc**; ordinary
letters no longer dismiss help or model details. Help and model details support
scrolling. **Ctrl+N** opens logs even during model loading; **F1** and **F2** also
remain available. Shortcut routing, availability, footer hints, and help share
an action registry in `internal/editmode`, ready for reuse by other apps.

Starting with `--visual-review on` automatically opens the picker when the
startup model is missing, is text-only, or has no selected vision encoder.
Canceling keeps the app open; submitting reopens the picker until a suitable
model is selected. `--vision-review on` is accepted as an alias.

Switching closes the old engine before loading the new one. It keeps the prompt
and `draft.svg`, replaces the old model's transcript with a draft checkpoint,
and preserves backend, context size, power, SSD streaming, and review budgets.
After loading, press **Esc**, then **c** to continue the existing draft, or enter
a new prompt. Vision and MTP companions are selected afresh for the new model;
an explicit companion path from startup is not reused across models. With
`--visual-review on`, selection requires an installed matching vision encoder.
Switching is available only when generation/enrichment is idle. A load failure keeps the
draft available; choose another model or retry loading.

The reusable Bubble Tea widget lives in `internal/modelpicker`; it reports a
`SelectedMsg` so each app can own its engine lifecycle. SVGPad is its first user.

For DeepSeek V4.1 Q2 on a Mac with SSD streaming:

```bash
./bin/ds4go-svgpad --model v41-q2 --backend metal --ssd-streaming \
  --visual-review on --visual-rounds 3 --tool-rounds 20
# Or build and run through Task:
task run:svgpad -- --model v41-q2 --backend metal --ssd-streaming --visual-review on
```

The installed `v41-vision` encoder is selected automatically. `--ssd-streaming`
passes through to the engine's expert streaming mode and defaults to off.

With `--model glm53-q2`, the installed `glm53-vision` encoder is also selected
automatically. To select an encoder explicitly, `--vision` accepts an installed
alias (such as `glm53-vision`) or a GGUF path.

Use `--visual-review off` to disable image review. This requires a vision-capable libds4 runtime and a compatible model/encoder pair; upgrading the Go package alone does not add vision to a text-only model. Extra flags also work with `task run:svgpad -- --visual-review off`.

The default budget is three automatic image review passes per request (`--visual-rounds 1..5`), shared across syntax-correction retries. An unchanged draft ends review early. If the final pass changes the drawing, the status reports that the automatic review limit was reached after edits. Each pass retains text feedback and sends only the latest review image, limiting image context growth. Cancellation and context exhaustion keep the draft available for recovery.

In the TUI, press **Esc** to leave prompt editing, then **`[` / `]`** to decrease/increase the review limit while idle. The footer shows `reviews:N`; `?` shows the shortcut and `m` shows the limit and review mode. Adjustments last for the current app session; `--visual-rounds` sets the startup value. This controls image review passes, each of which can include several edit-tool calls. It does not change the separate syntax-correction limit.

The model can also call `svg_preview()` to inspect the current complete draft
while working. The tool returns a PNG using the same renderer and white
background as automatic review, or text diagnostics for an invalid/empty draft.
It leaves the SVG unchanged. It requires a vision model with its encoder loaded
and visual review set to `auto` or `on`; otherwise it returns an explanation.

`--tool-rounds` sets the tool-round limit for each drafting or review phase
(default **20**, minimum 1). A round is one assistant tool-call batch; multiple
calls can share it, and syntax-repair retries consume the same budget. An extra
model turn is reserved for a final answer without tools. For example, add
`--tool-rounds 30` to give a complex drawing more room. The `m` info panel shows
the configured limit.

`--temp` sets the sampling temperature (default **0.7**; `--temp 0` restores
greedy decoding), with `--top-p` (default 0.95) applied when sampling. Tool-call
markup is always decoded greedily regardless, so sampling only shapes free
content. Greedy decoding is deterministic but can loop on tool-call markers and
makes auto-correct retries replay the identical failure. `--seed` pins the
sampler for reproducible turns; the default (0) draws a fresh random seed each
turn so regeneration and retries explore.

`--prompt "..."` runs headless: no TUI, the full drafting/auto-correct/review
pipeline executes once, the saved SVG path prints to stdout, and the exit code
reports success. `--outfile out.svg` names the result instead of the timestamped
`svgpad.<timestamp>.svg`. All other flags compose, e.g.
`ds4go-svgpad --prompt "a red fox" --outfile fox.svg --model vision-q2 --visual-review on`.

Before each model turn, SVGPad measures the rendered context, including tool
definitions and image tokens, and tells the model how much space remains.
Warnings become stronger around 75% and 90% usage (or sooner when little
response space remains). The context meter and `[CONTEXT]` log entries refresh
at these checks. Drafting and review phases share this capacity even when their
tool-round budgets reset.

SVGPad pauses before a turn that cannot fit at least 1,024 response tokens plus
one spare position. A long individual response can still hit the runtime limit.
After either context stop, press **Esc**, then **`c`** to keep your requests and
`draft.svg`, discard the previous assistant/tool transcript and review images,
and continue by inspecting the existing drawing. This recovery does not generate
a summary: prior plans and review conclusions are discarded, so the model must
check the artifact again. If the preserved requests themselves are too large,
shorten the prompt or restart with a larger `--ctx`.

The system prompt includes the configured limit and actual vision availability.
Application messages report remaining rounds before each model turn, ask the
model to finish when three remain, and require a final response at zero. These
messages do not replace the user's prompt in saved metadata. The hard cap still
applies if the model ignores the guidance.

Preview calls use the normal tool-round budget, independently of
`--visual-rounds`, and automatic review still runs after drafting. Only the latest
preview or automatic-review image is retained in model context; earlier tool
observations and critiques remain as text. The tool panel and log show the
preview result summary.

After saving, metadata enrichment generates the title, screen-reader description,
and keywords in a separate session. If the loaded engine has vision, that session
receives a rendered preview alongside the original prompt and SVG markup, and
uses the visible result to describe the image. Text-only engines use markup;
a rendering failure falls back to markup and is logged. This follows the loaded
engine's vision capability independently of the automatic-review budget. The
`[META]` log records `vision=true` or `vision=false`. Failed enrichment preserves
the already-saved SVG.

An optional test exercises live image inference and SVG editing without a terminal (ordinary tests use mocks):

```bash
SVG_VISION_MODEL="$HOME/.ds4/models/DeepSeek-V4-Flash-Vision-Exp-IQ2XXS-w2Q2K-AProjQ8-SExpQ8-OutQ8.gguf" \
  go test ./cmd/svgpad -run TestVisualReviewLive -v -count=1 -timeout=10m
```

On Linux, also set `CGO_ENABLED=0 GOFLAGS=-tags=nofakecgo` as described above. `SVG_VISION_ENCODER` and `SVG_VISION_LIB` override the companion and runtime paths for this test.

### cadpad — CAD Workbench

```bash
task run:cadpad
# pure geometry mode (no LLM)
NO_ENGINE=1 task run:cadpad
```

**Example session file** (`examples/cadpad/demo.cad.json`):

```json
[
  {"kind":"create",  "args":{"name":"base","shape":"box","params":{"x":10,"y":8,"z":2}}},
  {"kind":"create",  "args":{"name":"post","shape":"cylinder","params":{"r":1,"h":6}}},
  {"kind":"transform","args":{"name":"post","op":"translate","args":{"x":0,"y":0,"z":4}}},
  {"kind":"boolean",  "args":{"op":"union","target":"base","source":"post","blend_radius":0.3}}
]
```

Operations are stored as a replayable JSON log—every boolean, transform, and create is undoable and serializable.

### steering — Activation Steering

```bash
task run:steering -- --dir-steering ./my-vectors --scale 0,1,-1
```

Explore how steering vectors shift next-token probabilities. Branch timelines, adjust FFN scales on the fly, and compare outputs side-by-side.

---

## Common CLI Flags

All four apps share a similar inference flag set:

| Flag | Default | Meaning |
|------|---------|---------|
| `--ctx` | 32768 (16384 for cadpad) | Token context window |
| `--backend` | `metal` | `metal`, `cuda`, or `cpu` |
| `--power` | 80 (cadpad) / 100 (others) | GPU duty-cycle throttle % (1–100) |
| `-d, --debug` | `false` | Tee engine logs to `*.log` |

cadpad-only:

| Flag | Meaning |
|------|---------|
| `--no-engine` | Start without LLM (pure geometry / headless tool use) |

cadpad and trippad:

| Flag | Default | Meaning |
|------|---------|---------|
| `--kitty-transport` | `auto` | How Kitty frames reach the terminal: `png`, `rgba`, `shm`, or `auto` |

`png` works everywhere Kitty graphics do, including over SSH. `rgba` skips the
PNG encode but sends about 5.3 bytes per pixel through the terminal. `shm`
passes raw pixels through shared memory, so only a short reference goes through
the terminal. It needs a local terminal that supports Kitty's shared-memory
medium (`t=s`), such as Kitty or Ghostty. `shm` trusts you: it uses shared memory
once the terminal answers the startup `t=s` probe, falls back to PNG until then (and
whenever an object cannot be created), and the pad logs the fallback once.

`auto` makes the same request but says nothing when it lands on PNG, because that is
the expected result over SSH or in a terminal without `t=s`. The widget only sends
shared-memory frames after the terminal has answered the probe, and a terminal that
cannot read the object never does. The cadpad header labels the transport (`shm`)
once shared memory is in use. `auto` is the default. If the viewport stays blank,
run with `--kitty-transport png`.
cadpad names the transport in the viewport header and trippad logs the fallback.

steering-only:

| Flag | Meaning |
|------|---------|
| `--dir-steering` | Directory containing `vectors.json` registry |
| `--scale` | Comma-separated FFN scales |
| `--attn-scale` | Attention steering scale |
| `--allow-attn-steering` | Enable attention steering |
| `--top-k` | Top-K sampling filter |
| `--max-tokens` | Generation cap per roll |

---

## Project Layout

```
.
├── cmd/
│   ├── glyphpad/          # Unicode glyph TUI
│   ├── svgpad/            # SVG drawing TUI
│   ├── cadpad/            # CAD modeling TUI + headless harness
│   └── steering/          # Activation-steering dashboard
├── internal/
│   ├── cadpad/            # CAD world state, SDF ops, rendering
│   ├── cliopts/           # Shared CLI option parsing
│   ├── ds4log/            # Engine logging helpers
│   ├── editmode/          # Modal edit-box component (glyphpad & svgpad)
│   ├── engineinit/        # Lazy ds4go engine lifecycle
│   ├── headerbar/         # Shared headerbar component
│   ├── steerinspect/      # Steering vector inspection logic
│   └── steertui/          # Steering-specific TUI views
├── examples/
│   └── cadpad/            # Sample .cad.json session files
├── bin/                   # Built binaries (gitignored)
└── Taskfile.yml           # Build & run tasks
```

---

## Tech Stack

- **[Bubble Tea v2](https://github.com/charmbracelet/bubbletea)** – TUI framework
- **[Bubbles v2](https://github.com/charmbracelet/bubbles)** – Text inputs, key maps, etc.
- **[Lipgloss v2](https://github.com/charmbracelet/lipgloss)** – Terminal styling & layout
- **[Cobra](https://github.com/spf13/cobra)** – CLI framework (steering)
- **[ds4go](https://github.com/NimbleMarkets/ds4go)** – Local DeepSeek inference (GGUF via libds4)
- **[ntcharts](https://github.com/NimbleMarkets/ntcharts)** / **[ntcharts-svg](https://github.com/NimbleMarkets/ntcharts-svg)** – Terminal picture & SVG rendering
- **[gsdf / simplesdf](https://github.com/soypat/gsdf)** – Signed-distance-field geometry engine (cadpad)

---

## Development Tips

- **Watch logs** while a TUI is running:
  ```bash
  task log
  ```
- **Format check before pushing:**
  ```bash
  task pre-push
  ```
- **Run tests:**
  ```bash
  task test
  ```
- **Reduce VRAM pressure** if you hit OOM:
  ```bash
  task run:svgpad -- --ctx 8192 --power 50
  ```

## Acknowledgements

Thanks to [@antirez](https://github.com/antirez) for his work on [`ds4`](https://github.com/antirez/ds4) and for his local-LLM advocacy.  Thanks to [DeepSeek](https://www.deepseek.com/) for their public contributions.

## License

Released under the [MIT License](https://en.wikipedia.org/wiki/MIT_License), see [LICENSE.txt](./LICENSE.txt).

Copyright (c) 2026 [Neomantra Corp](https://www.neomantra.com).   

----
Made with :heart: and :fire: by the team behind [Nimble.Markets](https://nimble.markets).
