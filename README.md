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
| **steering** | `ds4go-steering` | DeepSeek activation-steering dashboard. Tweak FFN and attention steering vectors in real time, branch comparison timelines, and explore token logits interactively. |

---

## Prerequisites

These apps require `ds4go` to be installed, with a `ds4` dynamic library and associated model downloaded.  `ds4` requires 128G or more of GPU memory.

On Ubuntu, **cadpad** requires the OpenGL development library to be installed:
```bash
sudo apt install --no-install-recommends libgl1-mesa-dev libxcursor-dev libxrandr-dev libxinerama-dev libxi-dev libxxf86vm-dev
```

---

## Build

Build everything at once:

```bash
task build
```

Or build individual apps:

```bash
task build:glyhpad
task build:svgpad
task build:cadpad
task build:steering
```

Binaries are written to `./bin/`.

---

## Quick Start

### glyhpad — Glyph Scratchpad

```bash
task run:glyphpad
# or directly
./bin/ds4go-glyphpad --backend metal --ctx 32768
```

Type natural-language prompts to generate Unicode patterns, box-drawing diagrams, or pixel-art-style blocks. Use the modal edit box (`Ctrl+E`) to refine selections.

### svgpad — SVG Scratchpad

```bash
task run:svgpad
```

Describe an image (e.g., *“a blue circle inside a rounded rectangle”*) and the model emits SVG markup rendered live in the terminal. The engine is lazily loaded, so you can sketch offline and summon the LLM only when needed.

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
