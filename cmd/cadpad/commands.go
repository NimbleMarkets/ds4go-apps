package main

import (
	"context"
	"fmt"
	"image"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	ds4 "github.com/NimbleMarkets/ds4go"
	"github.com/NimbleMarkets/ds4go-apps/internal/bubble"
	"github.com/NimbleMarkets/ds4go-apps/internal/cadpad/lua"
	"github.com/NimbleMarkets/ds4go-apps/internal/cadpad/render"
	"github.com/NimbleMarkets/ds4go-apps/internal/cadpad/world"
	simplesdf "github.com/soypat/gsdf/gsdfaux/simplesdf"
)

var (
	luaPathJSONRe     = regexp.MustCompile(`"path"\s*:\s*"([^"]+\.lua)"`)
	luaPathActionRe   = regexp.MustCompile(`(?:Wrote|Executed|Appended to|Replaced[^ ]* in)\s+([^\s"]+\.lua)`)
	luaPathFallbackRe = regexp.MustCompile(`([A-Za-z0-9_./-]+\.lua)`)
)

var resPresets = []int{192, 384, 768, 1536}

func (m *model) cycleResolution(delta int) {
	m.resIndex += delta
	if m.resIndex < 0 {
		m.resIndex = 0
	}
	if m.resIndex >= len(resPresets) {
		m.resIndex = len(resPresets) - 1
	}
	m.renderer.SetMaxEdge(resPresets[m.resIndex])
	m.renderer.ClearCache()
}

func (m *model) cycleProjection() {
	switch m.proj {
	case render.ProjAngle:
		m.proj = render.ProjXY
	case render.ProjXY:
		m.proj = render.ProjXZ
	case render.ProjXZ:
		m.proj = render.ProjYZ
	default:
		m.proj = render.ProjAngle
	}
}

func (m *model) moveSelection(delta int) {
	names := m.w.Names()
	if len(names) == 0 {
		return
	}
	m.selected = (m.selected + delta + len(names)) % len(names)
}

// selectObject moves the list selection, makes it current, and refreshes
// the preview so the viewport follows the selection.
func (m *model) selectObject(delta int) tea.Cmd {
	m.moveSelection(delta)
	names := m.w.Names()
	if len(names) == 0 || m.selected >= len(names) {
		return nil
	}
	m.w.SetCurrent(names[m.selected])
	m.status = "current = " + names[m.selected]
	return m.refreshPreview()
}

func (m model) refreshPreviewCmd() tea.Cmd {
	cur := m.w.Current()
	if cur == "" {
		names := m.w.Names()
		if len(names) == 0 {
			return nil
		}
		cur = names[0]
	}
	return func() tea.Msg {
		s, _, ok := m.w.Get(cur)
		if !ok || s.Shader() == nil {
			return previewUpdatedMsg{name: cur, ok: false, err: "no sdf"}
		}
		pc, pr := m.viewportInnerSize()
		var img image.Image
		var rect image.Rectangle
		var err error
		if m.proj == render.ProjAngle {
			cp := render.CameraParams{
				Azimuth:   m.camAzimuth,
				Elevation: m.camElevation,
				Zoom:      m.camZoom,
				PanX:      m.camPanX,
				PanY:      m.camPanY,
			}
			if m.hqRender {
				// High-quality one-shot: smooth SDF sphere trace at full
				// resolution (the slow path), for a final look once the
				// camera is framed. Deliberately CPU — no render-mode badge.
				img, rect, err = m.renderer.RenderAngledScale(s, cur, cp, pc*8, pr*16, 1)
			} else {
				// Live auto path: GPU raymarch when available+transpilable,
				// else CPU mesh-preview fallback. The returned mode drives the
				// viewport's GPU/CPU badge.
				var mode render.RenderMode
				img, rect, mode, err = m.renderer.RenderAngledAuto(s, cur, cp, pc*8, pr*16, 1)
				ok2 := err == nil
				m.w.SetPreview(cur, world.Projection(m.proj), rect.Dx(), rect.Dy(), ok2, errStr(err))
				return previewUpdatedMsg{name: cur, img: img, ok: ok2, err: errStr(err), mode: mode, hasMode: true}
			}
		} else {
			img, rect, err = m.renderer.Render(s, cur, m.proj, pc*8, pr*16)
		}
		ok2 := err == nil
		m.w.SetPreview(cur, world.Projection(m.proj), rect.Dx(), rect.Dy(), ok2, errStr(err))
		return previewUpdatedMsg{name: cur, img: img, ok: ok2, err: errStr(err)}
	}
}

// warmUpGPUCmd does a one-shot tiny RenderAngledAuto on a throwaway sphere so
// the first real frame doesn't pay synchronous GPU device + pipeline creation.
// It runs on a tea.Cmd goroutine (GPU work is serialized on the render
// package's executor, so this is safe) and discards its output; the device and
// pipeline caches it primes live in the render package.
func (m model) warmUpGPUCmd() tea.Cmd {
	r := m.renderer
	return func() tea.Msg {
		cp := render.CameraParams{Zoom: 1}
		// Small dimensions keep the probe cheap; we only care about the
		// side-effect of creating the device/pipeline.
		_, _, _, _ = r.RenderAngledAuto(simplesdf.Sphere(1), "__gpu_warmup__", cp, 8, 8, 1)
		return gpuWarmedUpMsg{}
	}
}

func errStr(e error) string {
	if e == nil {
		return ""
	}
	return e.Error()
}

func (m model) submitInputCmd() tea.Cmd {
	text := strings.TrimSpace(m.input.Value())
	m.input.SetValue("")
	if text == "" {
		return nil
	}
	m.logger.Printf("[USER] %s", text)

	if strings.HasPrefix(text, "/") {
		return m.handleSlash(text)
	}

	if m.session == nil || m.engine == nil {
		m.status = "LLM engine not ready — using local echo"
		return func() tea.Msg {
			return toolDoneMsg{text: "echo: " + text + " (start with model for real LLM)"}
		}
	}

	return tea.Batch(
		func() tea.Msg { return bubble.InferencingStartMsg{} },
		func() tea.Msg {
			ch := make(chan tea.Msg, 128)

			go func() {
				defer close(ch)

				ctx, cancel := context.WithTimeout(context.Background(), 600*time.Second)
				defer cancel()

				// Assign a single timestamped file for this prompt cycle.
				tsFile := fmt.Sprintf("cadpad.%s.lua", time.Now().Format("20060102_150405"))
				*m.currentLuaFile = tsFile
				m.lastActiveLua = filepath.Join(m.luaWorkspace, tsFile)

				system := fmt.Sprintf(`You are a CAD modeling assistant. You can ONLY build models by writing and running Lua code. There are NO direct geometry-creation tools.

The Lua environment provides a module "sdf" with these constructors (all arguments are NUMBERS, not strings):
- sdf.sphere(radius)
- sdf.box(x, y, z, round)
- sdf.cylinder(radius, height, round)
- sdf.torus(majorRadius, minorRadius)
- sdf.hexprism(face2face, height)
- sdf.triprism(triangleHeight, extrude)
- sdf.boxframe(x, y, z, thickness)

Chainable methods (all args NUMBERS):
- :union(other, ...)   boolean union
- :diff(other)         boolean difference
- :intersect(other)    boolean intersection
- :translate(x,y,z)    move
- :scale(factor)       uniform scale
- :rotate_x(rad)       rotate around X axis
- :rotate_y(rad)       rotate around Y axis
- :rotate_z(rad)       rotate around Z axis
- :k(blendRadius)      set smooth blend for NEXT boolean

After building, publish with: sdf.register("name", object)

EXAMPLE 1 - single object:
  local sdf = require("sdf")
  local s = sdf.sphere(5)
  sdf.register("ball", s)

EXAMPLE 2 - boolean + transform:
  local sdf = require("sdf")
  local b = sdf.box(10, 8, 3, 0)
  local s = sdf.sphere(3)
  local hole = b:diff(s:translate(0, 0, 1.5))
  sdf.register("bracket", hole)

EXAMPLE 3 - loop / array:
  local sdf = require("sdf")
  local all = sdf.sphere(1):translate(0,0,0)
  for i = 1, 4 do
    local s = sdf.sphere(1):translate(i*3, 0, 0)
    all = all:union(s)
  end
  sdf.register("row", all)

You MUST write all code to "%s". Use lua_read, lua_write, lua_append, lua_replace, lua_run. Do not create other files.

If lua_run errors, read the file, fix the code, and run again. Keep responses concise.`, tsFile)

				var lastAssistant ds4.ChatMessage
				var driver *bubble.GenerationDriver
				driver = bubble.NewGenerationDriver(bubble.DriverOptions{
					Engine:    m.engine,
					Session:   m.session,
					Tools:     m.tools,
					ThinkMode: m.thinkMode,
					MaxRounds: m.maxRounds,
					ExecuteTools: func(ctx context.Context, calls []ds4.ToolCall) ([]ds4.ChatMessage, error) {
						driver.Emit(bubble.LogEvent{
							Level:   "info",
							Message: fmt.Sprintf("executing %d tool call(s)", len(calls)),
						})
						for _, c := range calls {
							driver.Emit(bubble.LogEvent{
								Level:   "info",
								Message: fmt.Sprintf("tool: %s args=%s", c.Name, truncateForLog(c.Arguments, 200)),
							})
						}

						results, err := m.tools.ExecuteToolCalls(ctx, calls)
						if err != nil {
							driver.Emit(bubble.LogEvent{Level: "error", Message: "tool exec error: " + err.Error()})
							return nil, err
						}

						for _, r := range results {
							driver.Emit(bubble.LogEvent{
								Level:   "info",
								Message: "tool result: " + truncateForLog(r.Content, 200),
							})
						}
						return results, nil
					},
					OnEvent: func(e bubble.Event) {
						if am, ok := e.(bubble.AssistantMessageEvent); ok {
							lastAssistant = am.Message
						}
						select {
						case ch <- driverEventMsg{e: e}:
						default:
						}
					},
				})

				res, err := driver.RunWithPrompt(ctx, system, []ds4.ChatMessage{{Role: "user", Content: text}})
				if err != nil {
					// On timeout/error, preserve whatever assistant output we have.
					if lastAssistant.Content != "" || lastAssistant.ReasoningContent != "" {
						summary := lastAssistant.Content
						reasoning := lastAssistant.ReasoningContent
						if reasoning == "" {
							reasoning, summary = extractThinkFromContent(summary)
						}
						ch <- toolDoneMsg{
							text:      summary,
							reasoning: reasoning,
							err:       err,
						}
					} else {
						ch <- toolDoneMsg{err: err}
					}
					return
				}

				summary := res.Assistant.Content
				reasoning := res.Assistant.ReasoningContent
				if reasoning == "" {
					// Fallback: manually extract <think> tags that ParseAssistant
					// may have left in Content when thinkMode is ThinkNone.
					reasoning, summary = extractThinkFromContent(summary)
				}
				if summary == "" {
					summary = fmt.Sprintf("LLM used %d tool rounds", res.ToolRounds)
				}
				ch <- toolDoneMsg{
					text:      summary,
					reasoning: reasoning,
				}
			}()

			return generationStartedMsg{ch: ch}
		},
	)
}

func (m model) handleSlash(text string) tea.Cmd {
	parts := strings.Fields(text)
	cmd := strings.ToLower(strings.TrimPrefix(parts[0], "/"))
	args := parts[1:]

	return func() tea.Msg {
		var out string
		var err error
		defer func() {
			if err != nil {
				m.logger.Printf("[SLASH] /%s error=%q", cmd, err.Error())
			} else {
				m.logger.Printf("[SLASH] /%s -> %s", cmd, out)
			}
		}()
		switch cmd {
		case "create":
			if len(args) < 2 {
				err = fmt.Errorf("usage: /create <shape> <name> [k=v ...]")
				break
			}
			shape := args[0]
			name := args[1]
			params := parseKV(args[2:])
			_, err = m.w.Create(name, shape, params)
			if err == nil {
				m.renderer.Invalidate(name)
			}
			out = "created " + name
		case "boolean", "bool":
			if len(args) < 3 {
				err = fmt.Errorf("usage: /boolean <op> <target> <source> [blend=0.1]")
				break
			}
			blend := 0.0
			if len(args) > 3 {
				fmt.Sscanf(args[3], "blend=%f", &blend)
			}
			err = m.w.Boolean(args[0], args[1], args[2], blend)
			if err == nil {
				m.renderer.Invalidate(args[1])
			}
			out = "boolean ok"
		case "transform", "xform":
			if len(args) < 2 {
				err = fmt.Errorf("usage: /transform <name> <op> [args...]")
				break
			}
			params := parseKV(args[2:])
			err = m.w.Transform(args[0], args[1], params)
			if err == nil {
				m.renderer.Invalidate(args[0])
			}
			out = "transform ok"
		case "group":
			if len(args) < 2 {
				err = fmt.Errorf("usage: /group <newname> item1 item2 ...")
				break
			}
			err = m.w.Group(args[0], args[1:])
			if err == nil {
				m.renderer.Invalidate(args[0])
			}
			out = "group ok"
		case "export":
			if len(args) < 1 {
				err = fmt.Errorf("usage: /export <name> [file.stl|file.3mf]")
				break
			}
			name := args[0]
			file := name + ".stl"
			if len(args) > 1 {
				file = args[1]
			}
			out, err = exportObject(m.w, name, file, 256)
		case "save":
			fn := "cadpad-session.cad.json"
			if len(args) > 0 {
				fn = args[0]
			}
			err = m.w.Save(fn)
			out = "saved " + fn
		case "load":
			if len(args) == 0 {
				err = fmt.Errorf("usage: /load file.cad.json")
				break
			}
			err = m.w.Load(args[0])
			if err == nil {
				m.renderer.ClearCache()
			}
			out = "loaded"
		case "clear":
			m.w.Clear()
			m.renderer.ClearCache()
			out = "world cleared"
		case "help":
			m.showHelp = true
			out = "help shown"
		default:
			err = fmt.Errorf("unknown slash cmd %q — try /help", cmd)
		}
		if err != nil {
			return toolDoneMsg{err: err}
		}
		return toolDoneMsg{text: out}
	}
}

// exportObject writes a world object to filename, choosing the format by
// extension: ".3mf" -> 3MF mesh, anything else -> STL.
func exportObject(w *world.World, name, filename string, divisions int) (string, error) {
	if divisions <= 0 {
		divisions = 256
	}
	s, _, ok := w.Get(name)
	if !ok {
		return "", fmt.Errorf("object %q not found", name)
	}
	if strings.EqualFold(filepath.Ext(filename), ".3mf") {
		f, err := os.Create(filename)
		if err != nil {
			return "", err
		}
		defer f.Close()
		if err := render.WriteSDF3MF(f, name, s, divisions); err != nil {
			return "", err
		}
		return "exported " + filename, nil
	}
	cfg := simplesdf.STLConfig{ResolutionDivisions: uint(divisions)}
	if err := s.SaveSTL(filename, cfg); err != nil {
		return "", err
	}
	return "exported " + filename, nil
}

// extractThinkFromContent extracts <think>...</think> reasoning from raw assistant
// content and returns the reasoning + the content with the think block removed.
func extractThinkFromContent(content string) (reasoning, cleaned string) {
	start := strings.Index(content, "<think>")
	if start == -1 {
		return "", content
	}
	end := strings.Index(content[start:], "</think>")
	if end == -1 {
		// Unclosed <think> tag — everything after it is reasoning.
		reasoning = strings.TrimSpace(content[start+len("<think>"):])
		cleaned = strings.TrimSpace(content[:start])
		return reasoning, cleaned
	}
	reasoning = strings.TrimSpace(content[start+len("<think>") : start+end])
	before := strings.TrimSpace(content[:start])
	after := strings.TrimSpace(content[start+end+len("</think>"):])
	if before != "" && after != "" {
		cleaned = before + "\n\n" + after
	} else if before != "" {
		cleaned = before
	} else {
		cleaned = after
	}
	return reasoning, cleaned
}

func parseKV(pairs []string) map[string]float64 {
	out := map[string]float64{}
	for _, p := range pairs {
		if k, v, ok := strings.Cut(p, "="); ok {
			var f float64
			fmt.Sscanf(v, "%f", &f)
			out[k] = f
		}
	}
	return out
}

func (m *model) loadLuaEntryCmd() tea.Cmd {
	if m.luaEntryIndex < 0 || m.luaEntryIndex >= len(m.luaEntries) {
		return nil
	}
	entry := m.luaEntries[m.luaEntryIndex]

	backup := m.w.Clone()
	m.w.Clear()
	m.renderer.ClearCache()

	st := lua.NewState(m.w, m.renderer)
	err := st.DoFile(entry.path)
	st.Close()

	if err != nil {
		m.w = backup
		m.lastErr = fmt.Sprintf("failed to load %s: %v", entry.filename, err)
		m.status = m.lastErr
		return nil
	}

	m.status = "loaded " + entry.filename
	return m.refreshPreview()
}

func truncateForLog(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

func extractLuaPath(msg string) string {
	if strings.Contains(msg, `"path":`) {
		if m := luaPathJSONRe.FindStringSubmatch(msg); len(m) == 2 {
			return m[1]
		}
	}
	if m := luaPathActionRe.FindStringSubmatch(msg); len(m) == 2 {
		return m[1]
	}
	for _, p := range luaPathFallbackRe.FindAllString(msg, -1) {
		if strings.HasSuffix(p, ".lua") && !strings.Contains(p, " ") {
			return p
		}
	}
	return ""
}

func (m *model) saveTimestampedLua() string {
	if m.lastActiveLua == "" || m.luaWorkspace == "" {
		return ""
	}
	data, err := os.ReadFile(m.lastActiveLua)
	if err != nil {
		return ""
	}
	ts := time.Now().Format("20060102_150405")
	fname := fmt.Sprintf("cadpad.%s.lua", ts)
	dst := filepath.Join(m.luaWorkspace, fname)
	if err := os.WriteFile(dst, data, 0644); err != nil {
		m.logger.Printf("timestamped lua save failed: %v", err)
		return ""
	}
	m.logger.Printf("[SAVE] %s (%d bytes)", fname, len(data))

	// Add to in-memory list so it appears in the UI immediately.
	info, _ := os.Stat(dst)
	modTime := time.Now()
	if info != nil {
		modTime = info.ModTime()
	}
	newEntry := luaEntry{filename: fname, path: dst, modTime: modTime}
	m.luaEntries = append([]luaEntry{newEntry}, m.luaEntries...)
	m.luaEntryIndex = 0

	return fname
}
