package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	tea "charm.land/bubbletea/v2"
	ds4 "github.com/NimbleMarkets/ds4go"
	"github.com/NimbleMarkets/ds4go-apps/internal/engineinit"
)

type startupModelSelectionMsg struct{}

type switchSpinnerTickMsg struct{ started time.Time }

func switchSpinnerTick(started time.Time) tea.Cmd {
	return tea.Tick(200*time.Millisecond, func(time.Time) tea.Msg { return switchSpinnerTickMsg{started: started} })
}

func (m model) needsVisionModelSelection() bool {
	return m.visual.Mode == "on" && (m.modelPath == "" || m.engOpts.VisionPath == "" ||
		(m.modelInfo != nil && !m.modelInfo.Vision) || (m.engine != nil && !m.engine.HasVision()))
}

func (m *model) chooseRequiredVisionModel() tea.Cmd {
	m.statusText = "Choose a vision model with its encoder installed"
	return m.openModelPicker()
}

func (m model) modelSwitchBusy() bool {
	return m.generating || m.metadataInFlight || m.switchingModel || m.releasingEngine || m.lifecycle.status == engineinit.StatusOpening
}

// Resolve companion policy anew; paths belonging to the previous checkpoint
// must never be carried into a different model family.
func switchedModelOptions(base ds4.EngineOptions, info ds4.ModelInfo, visionMode string, mtpEnabled bool) (ds4.EngineOptions, error) {
	if !info.Installed || !info.IsChatModel() {
		return base, fmt.Errorf("select an installed chat model")
	}
	if st, err := os.Stat(info.Path); err != nil || !st.Mode().IsRegular() || st.Size() == 0 {
		return base, fmt.Errorf("model is no longer installed: %s", info.Alias)
	}
	base.ModelPath = info.Path
	base.VisionPath, base.MTPPath = "", ""
	if mtpEnabled {
		ds4.ApplyMTPDefaults(&base)
	}
	if visionMode != "off" {
		ds4.ApplyVisionDefaults(&base)
	}
	if visionMode == "on" && base.VisionPath == "" {
		return base, fmt.Errorf("visual review is required; choose a vision model with its encoder installed")
	}
	return base, nil
}

func (m model) switchModel(info ds4.ModelInfo) (tea.Model, tea.Cmd) {
	if m.modelSwitchBusy() {
		m.errText = "Wait for generation, enrichment, or engine loading/release to finish before switching models."
		return m, nil
	}

	opts, err := switchedModelOptions(m.engOpts, info, m.visual.Mode, m.mtpEnabled)
	if err != nil {
		m.errText = err.Error()
		return m, nil
	}
	if m.engine != nil && (opts.VisionPath == "" || m.engine.HasVision()) && opts.VisionPath == m.engOpts.VisionPath && (info.Path == m.modelPath || (m.modelInfo != nil && m.modelInfo.Alias == info.Alias)) {
		m.statusText = "Already using " + info.Alias
		return m, nil
	}
	draftPath := filepath.Join(m.workDir, "draft.svg")
	history, err := checkpointSVGHistory(m.history, draftPath)
	if err != nil {
		m.errText = err.Error()
		return m, nil
	}
	st, _ := os.Stat(draftPath)
	m.resumeDraft = st != nil && st.Size() > 0
	eng, sess := m.engine, m.session
	m.engine, m.session = nil, nil
	m.engOpts, m.modelPath, m.modelInfo = opts, info.Path, &info
	m.mtpPath = opts.MTPPath
	m.visual.EncoderPath = "" // future switches also use each model's installed companion
	m.history = history
	m.ctxPos, m.hasMTP, m.mtpDraft = 0, false, 0
	m.lastErr, m.engineErr, m.errText = nil, nil, ""
	m.visualPasses, m.autoCorrectCount, m.toolRounds = 0, 0, 0
	m.liveCalls, m.pendingCalls = nil, nil
	m.roundCallStart = len(m.toolCalls)
	m.yoloMode = false
	m.switchingModel = true
	m.switchStarted = time.Now()
	m.spinnerFrame = 0
	m.lifecycle = engineLifecycle{status: engineinit.StatusOpening}
	m.statusText = "Switching to " + info.Alias + "…"
	m.logger.Printf("[MODEL] switching to=%s vision=%s ssd-streaming=%v", info.Alias, opts.VisionPath, opts.SSDStreaming)
	return m, tea.Batch(switchEngineCmd(eng, sess, m.lib, opts, m.ctxSize), switchSpinnerTick(m.switchStarted))
}

// One command owns the entire transition: close session, close engine, then
// open its replacement. The host cannot accidentally load both huge models.
func switchEngineCmd(eng *ds4.Engine, sess *ds4.Session, lib *ds4.Library, opts ds4.EngineOptions, ctxSize int) tea.Cmd {
	return func() tea.Msg {
		if sess != nil {
			sess.Close()
		}
		if eng != nil {
			eng.Close()
		}
		return engineReadyMsg(engineinit.Open(lib, opts, ctxSize))
	}
}

func (m *model) openModelPicker() tea.Cmd {
	if m.modelSwitchBusy() {
		m.errText = "Wait for generation, enrichment, or engine loading/release to finish before switching models."
		return nil
	}
	mode := m.visual.Mode
	return m.modelPicker.Open(m.modelPath, func(info ds4.ModelInfo) string {
		if mode == "on" && ds4.DefaultVisionPath(info.Path) == "" {
			if info.Vision && info.Encoder != "" {
				return "Install encoder: ds4go model download " + info.Encoder
			}
			return "This session requires vision: choose a model with its encoder installed."
		}
		return ""
	})
}
