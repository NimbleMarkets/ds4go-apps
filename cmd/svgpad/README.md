# svgpad

An SVG drawing scratchpad. Describe an image and the model writes SVG, which svgpad renders in the terminal as it goes.

```bash
task run:svgpad
# or directly
./bin/ds4go-svgpad --model v41-q2 --backend metal --ssd-streaming \
  --visual-review on --visual-rounds 3 --tool-rounds 20
```

<img src="../../screenshots/svgpad.png" width="600" alt="svgpad viewing a saved cyberpunk pelican-on-a-bicycle drawing, with its prompt, tags, SVG element counts, and the tool list">

Describe an image, for example *"a blue circle inside a rounded rectangle"*. The engine loads lazily, so you can browse saved drawings and sketch before you load a model.

The drafting tools reject appends after the outer `</svg>` has closed and tell the model to edit inside the existing root. Validation reports content outside the root at the line where the root closed, so a misplaced `</svg>` gets fixed in place instead of by adding or deleting closing tags at the end of the file.

## Visual review

With a vision model, svgpad checks its own output: it renders a PNG, inspects it, edits the SVG, and renders again. The model looks at labels, clipping, spacing, contrast, and composition after the normal drafting loop. The images come from the same renderer as the viewer, keep the canvas proportions, and put transparency on white. This is feedback from the model, not a guarantee that the drawing is correct.

| Flag | Default | Meaning |
|------|---------|---------|
| `--visual-review` | `auto` | `auto` reviews when the loaded engine has vision and otherwise runs only syntax and render checks. `on` requires vision. `off` disables image review. `--vision-review` is an alias. |
| `--visual-rounds` | `3` | Review passes per request, 1 to 5. Syntax-correction retries share this budget. |
| `--vision` | installed companion | Vision encoder: an installed alias or a GGUF path. |

- Review stops early if a pass leaves the draft unchanged. If the last pass changes the drawing, the status says the limit was reached after edits.
- Each pass keeps its text feedback and sends only the latest review image, so image context does not pile up.
- Cancelling or running out of context keeps the draft so you can recover.
- Vision needs a vision-capable libds4 runtime and a matching model and encoder. Upgrading the Go package does not add vision to a text-only model.
- The encoder is found through ds4go's model catalog. For example, `--model v41-q2` selects the installed `v41-vision` encoder, and `--model glm53-q2` selects `glm53-vision`.
- To change the review limit while idle, press **Esc**, then **`[`** or **`]`**. The footer shows `reviews:N`. The change lasts for the session. It does not change the separate syntax-correction limit.

The model can also call `svg_preview()` to look at the current draft while it works. It returns a PNG from the same renderer, or text diagnostics if the draft is empty or invalid, and it does not change the SVG. It needs a vision model with its encoder loaded and review set to `auto` or `on`. Otherwise it returns an explanation. Previews use the normal tool-round budget, not `--visual-rounds`, and only the latest preview or review image stays in the model's context.

## Models

`--model` takes an installed catalog alias or a GGUF path. If a catalog model is missing, svgpad prints the command to download it.

Press **Ctrl+O** while idle to pick a different installed model. Type to search by alias or family, use **↑/↓**, and press **Enter** to load it or **Esc** to cancel. The picker shows model size and whether a vision encoder is installed. It re-reads the catalog each time it opens. It does not download models or change the CLI default. The picker is a reusable widget in `internal/modelpicker`.

With `--visual-review on`, the picker opens at startup if the model is missing, text-only, or has no vision encoder. Cancelling keeps the app open, and submitting a prompt reopens the picker until you choose a suitable model.

Switching models:

- Switching is allowed only while generation and metadata enrichment are idle.
- The old engine is closed before the new one loads.
- The prompt and `draft.svg` are kept. The old transcript is replaced by a draft checkpoint.
- Backend, context size, power, SSD streaming, and review budgets carry over.
- Vision and MTP companions are chosen again for the new model. A companion path given at startup is not reused.
- With `--visual-review on`, the new model needs an installed matching vision encoder.
- If loading fails, the draft stays available. Pick another model or retry.
- After loading, press **Esc** and then **`c`** to continue the draft, or enter a new prompt.

## Keys

| Key | Action |
|-----|--------|
| **Ctrl+R** | Cycle reasoning while typing. The cursor does not move. A change made during generation is marked **next**, and the running request keeps its setting. |
| **F1** | Help |
| **F2** | Run settings |
| **F3** | Saved-drawing browser (while idle) |
| **Ctrl+O** | Model picker |
| **Ctrl+N** | Logs. Works during model loading. |
| **Ctrl+Y** | Copy the focused pane |
| **Tab / Shift+Tab** | Cycle panels and the prompt |
| **Esc** | Close help, settings, details, drawings, and logs |

**F2** run settings cover reasoning, visual-review mode, review passes, tool rounds, and whether context carries over between prompts. Use **↑/↓** to pick a setting, **←/→** to change it, and **Esc** to close. Changes last for the session. Review and tool budgets are locked during generation, enrichment, and engine loading. Reasoning and context apply to the next request. Settings are read-only while a model is switching or the engine is releasing. If vision is not loaded, close settings and use **Ctrl+O**. Choosing the current model reloads it with its installed encoder.

**F3** opens a searchable list of saved drawings. Type to filter by title, prompt, filename, or keywords, and press **Enter** to inspect one. Browsing keeps your pending prompt, cursor, and working `draft.svg`. Press **`c`** to continue the working draft. **Page Up/Down** scroll or pan the focused panel, and **j/k** scroll when the prompt is not focused.

**Ctrl+Y** copies the whole focused pane, including lines scrolled out of view, without terminal styling. The pane can be the prompt, SVG source, activity text, or tool panel, or the logs, help, or model details when one is open. The SVG source follows the selected drawing or live preview. Focus, cursor, and scroll position stay put, and an empty pane leaves the clipboard unchanged. This uses OSC 52, so your terminal must allow clipboard writes. Over SSH it writes to the local terminal's clipboard.

Shortcut routing, footer hints, and help share one action registry in `internal/editmode`.

## Sampling and tool rounds

| Flag | Default | Meaning |
|------|---------|---------|
| `--temp` | `0.7` | Sampling temperature. `0` is greedy. |
| `--top-p` | `0.95` | Nucleus cutoff, used when sampling. |
| `--seed` | `0` | Sampler seed. `0` draws a new seed each turn, so retries try different output. |
| `--tool-rounds` | `20` | Tool rounds per drafting or review phase (minimum 1). |

Tool-call markup is always decoded greedily, so temperature only affects free text. Fully greedy decoding is deterministic but can loop on tool-call markers, and it makes auto-correct retries repeat the same failure.

A round is one batch of tool calls from the model. Several calls can share a round, and syntax-repair retries use the same budget. An extra turn is reserved for a final answer without tools. Before each turn svgpad tells the model how many rounds remain, asks it to finish when three remain, and requires a final response at zero. These messages do not replace your prompt in saved metadata. The hard cap applies even if the model ignores them. Press **`m`** while idle to see the configured limit.

## Context

Before each turn, svgpad measures the rendered context, including tool definitions and image tokens, and tells the model how much space is left. Warnings get stronger around 75% and 90% usage, or sooner if little response space remains. The context meter and `[CONTEXT]` log entries refresh at these checks. Drafting and review share this capacity even when their tool-round budgets reset.

svgpad pauses before a turn that cannot fit at least 1,024 response tokens plus one spare position. A long single response can still hit the runtime limit. After either stop, press **Esc** and then **`c`**. This keeps your requests and `draft.svg`, discards the earlier assistant and tool transcript and the review images, and continues by inspecting the existing drawing. It does not write a summary, so earlier plans and review conclusions are lost and the model has to look at the drawing again. If your own requests are too large, shorten the prompt or restart with a larger `--ctx`.

## Headless mode

`--prompt "..."` runs without the TUI. The full drafting, auto-correct, and review pipeline runs once, the saved SVG path is printed to stdout, and the exit code reports success. `--outfile out.svg` sets the output name instead of `svgpad.<timestamp>.svg`. Other flags work as usual:

```bash
ds4go-svgpad --prompt "a red fox" --outfile fox.svg --model vision-q2 --visual-review on
```

## Saved metadata

After saving, a separate session writes a title, a screen-reader description, and keywords. With a vision engine it receives a rendered preview along with the original prompt and the SVG markup. A text-only engine uses the markup, and so does a rendering failure, which is logged. This follows the engine's vision support, independent of the review budget. The `[META]` log line records `vision=true` or `vision=false`. If enrichment fails, the saved SVG is kept.

## Tests

Ordinary tests use mocks. One optional test runs live image inference and SVG editing without a terminal:

```bash
SVG_VISION_MODEL="$HOME/.ds4/models/DeepSeek-V4-Flash-Vision-Exp-IQ2XXS-w2Q2K-AProjQ8-SExpQ8-OutQ8.gguf" \
  go test ./cmd/svgpad -run TestVisualReviewLive -v -count=1 -timeout=10m
```

On Linux, also set `CGO_ENABLED=0 GOFLAGS=-tags=nofakecgo`. `SVG_VISION_ENCODER` and `SVG_VISION_LIB` override the encoder and runtime paths.

`scripts/svgpad-bench.sh` times a fixed set of prompts so runs on different machines can be compared.
