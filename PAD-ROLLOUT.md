# Pad rollout from SVGPad

## Implemented in the first rollout

- Shared cancellation-aware event delivery and shutdown draining. Glyph content
  is no longer dropped when the UI channel fills. CAD owns its generation and
  slash-command tasks, blocks overlapping submissions, and cancels with Escape.
- CAD prompt typing no longer treats `q` as quit. Both apps defer quitting while
  engine loading owns the resources. Model replacement closes the old session
  and engine before opening the replacement.
- Shared F1 help, F2 settings, Ctrl+R reasoning, Ctrl+O installed-model picker,
  Ctrl+Y pane copying, and Tab/Shift+Tab focus including the prompt. Settings
  preserve cursor/focus and are frozen while a model loads. Run changes apply
  to subsequent requests through worker snapshots.
- Lazy engine loading with queued prompts, recoverable load failures, and a
  bicycle animation. CAD retains geometry-only startup and commands.
- Shared sampling flags (`--temp`, `--top-p`, `--seed`). CAD keeps its existing
  36 tool-capable rounds, exposes `--tool-rounds`, and reserves a final response.
- CAD opts into measured context feedback and response-space preflight, sends
  remaining-round guidance, and retains the partial driver history on errors.
- Glyph measures its prompt before generation and can rebuild context from
  user requirements plus the current drawing program after context exhaustion.

Reusable packages: `internal/padui`, `internal/runconfig`, and additions to
`internal/bubble` and `internal/engineinit`. The model picker is the existing
`internal/modelpicker`. The current SVGPad and benchmark implementations were
left intact while these components gained their second and third consumers.

## Subsequent rollout stages

1. Finish unifying legacy pane navigation and action definitions, introduce the
   dedicated saved-work browser in both apps, and make inspect/adopt distinct.
   Glyph currently has only basic focus selection for its existing panes;
   CAD retains its existing Lua browsing while idle.
2. Add durable Glyph snapshots and explicit CAD checkpoint continuation using
   the Lua file and world state. Partial CAD transcripts are retained now but
   are not yet used by a continuation workflow. Unify engine release controls.
3. Add image-bearing CAD previews and bounded review after geometry validation;
   render Glyph with defined font/cell metrics before adding its review loop.
   Gate on actual encoder availability and prune superseded observations.
4. Add headless generation/save parity and app-specific quality fixtures once
   the SVGPad benchmark interface settles. Record settings, stage timings,
   validity, and retained failure artifacts; serialize inference runs.

Validation must cover prompt/cursor preservation, hidden panels, empty-copy
behavior, full event queues, cancellation, quitting during loading, missing
models, checkpoint recovery, and stale renders. GPU rendering and live inference
need separate platform smoke tests; they are not replaced by navigation tests.
