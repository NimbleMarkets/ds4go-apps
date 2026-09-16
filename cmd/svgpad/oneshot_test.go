package main

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	tea "charm.land/bubbletea/v2"
	ds4 "github.com/NimbleMarkets/ds4go"
	"github.com/NimbleMarkets/ds4go-apps/internal/bubble"
	"github.com/NimbleMarkets/ds4go-apps/internal/engineinit"
)

const oneShotTestSVG = `<svg xmlns="http://www.w3.org/2000/svg" width="10" height="10"><rect width="4" height="4" fill="red"/></svg>`

// cmdYieldsQuit executes cmd (recursing through batches) and reports whether
// any produced message is tea.QuitMsg.
func cmdYieldsQuit(t *testing.T, cmd tea.Cmd) bool {
	t.Helper()
	if cmd == nil {
		return false
	}
	switch msg := cmd().(type) {
	case tea.QuitMsg:
		return true
	case tea.BatchMsg:
		for _, c := range msg {
			if cmdYieldsQuit(t, c) {
				return true
			}
		}
	}
	return false
}

func TestOneShotOptionsValidate(t *testing.T) {
	if err := (oneShotOptions{Outfile: "x.svg"}).validate(); err == nil {
		t.Error("--outfile without --prompt must be rejected")
	}
	if err := (oneShotOptions{Prompt: "draw", Outfile: "x.svg"}).validate(); err != nil {
		t.Errorf("prompt+outfile: %v", err)
	}
	if err := (oneShotOptions{}).validate(); err != nil {
		t.Errorf("TUI mode (no flags): %v", err)
	}
}

func TestOneShotStartOpensDormantEngine(t *testing.T) {
	m := testModel()
	m.oneShot = oneShotOptions{Prompt: "draw a fox"}

	m = update(t, m, oneShotStartMsg{})

	if m.lifecycle.status != engineinit.StatusOpening {
		t.Errorf("lifecycle.status = %v, want StatusOpening", m.lifecycle.status)
	}
	if !m.lifecycle.pendingSubmit {
		t.Error("pendingSubmit = false, want queued submit for engineReadyMsg")
	}
	if m.input.Value() != "draw a fox" {
		t.Errorf("input = %q, want the one-shot prompt (engineReadyMsg submits from it)", m.input.Value())
	}
}

func TestOneShotEngineOpenFailureQuits(t *testing.T) {
	m := testModel()
	m.oneShot = oneShotOptions{Prompt: "draw"}
	m.lifecycle.status = engineinit.StatusOpening
	m.lifecycle.pendingSubmit = true

	next, cmd := m.Update(engineReadyMsg{Err: errors.New("no model installed")})
	m = next.(model)

	if m.oneShotErr == nil {
		t.Error("oneShotErr = nil, want the open failure recorded")
	}
	if !cmdYieldsQuit(t, cmd) {
		t.Error("no tea.Quit; a headless run would hang in the Error state")
	}
}

func TestOneShotSavesToOutfileAndWaitsForMetadata(t *testing.T) {
	m := testModel()
	dir := t.TempDir()
	m.workDir = dir
	m.oneShot = oneShotOptions{Prompt: "draw", Outfile: "exp1.svg"}
	m.generating = true
	m.metadataWG = &sync.WaitGroup{}
	m.history = []ds4.ChatMessage{{Role: "user", Content: "draw"}}
	if err := os.WriteFile(filepath.Join(dir, "draft.svg"), []byte(oneShotTestSVG), 0644); err != nil {
		t.Fatal(err)
	}

	next, cmd := m.Update(turnDoneMsg{result: bubble.RunResult{
		Assistant: ds4.ChatMessage{Role: "assistant"},
		History:   m.history,
	}})
	m = next.(model)

	want := filepath.Join(dir, "exp1.svg")
	if _, err := os.Stat(want); err != nil {
		t.Fatalf("outfile not written: %v", err)
	}
	if m.oneShotPath != want {
		t.Errorf("oneShotPath = %q, want %q", m.oneShotPath, want)
	}
	if m.oneShotErr != nil {
		t.Errorf("oneShotErr = %v, want nil", m.oneShotErr)
	}
	if !m.metadataInFlight {
		t.Error("metadataInFlight = false, want enrichment before quitting")
	}
	if cmdYieldsQuit(t, cmd) {
		t.Error("quit before metadata enrichment finished")
	}
}

func TestOneShotQuitsAfterMetadataDone(t *testing.T) {
	m := testModel()
	m.oneShot = oneShotOptions{Prompt: "draw", Outfile: "exp1.svg"}
	m.metadataInFlight = true

	next, cmd := m.Update(metadataDoneMsg{filename: "exp1.svg"})
	m = next.(model)

	if m.metadataInFlight {
		t.Error("metadataInFlight = true, want cleared")
	}
	if !cmdYieldsQuit(t, cmd) {
		t.Error("no tea.Quit after metadata; a headless run would hang")
	}
}

func TestOneShotQuitsOnGenerationError(t *testing.T) {
	m := testModel()
	m.workDir = t.TempDir()
	m.oneShot = oneShotOptions{Prompt: "draw"}
	m.generating = true

	next, cmd := m.Update(turnDoneMsg{err: errors.New("boom")})
	m = next.(model)

	if m.oneShotErr == nil {
		t.Error("oneShotErr = nil, want generation error recorded")
	}
	if !cmdYieldsQuit(t, cmd) {
		t.Error("no tea.Quit on generation error")
	}
}

func TestOneShotQuitsWhenNoSVGProduced(t *testing.T) {
	m := testModel()
	m.workDir = t.TempDir() // no draft.svg
	m.oneShot = oneShotOptions{Prompt: "draw"}
	m.generating = true
	m.autoCorrectCount = m.maxAutoCorrect // correction budget exhausted

	next, cmd := m.Update(turnDoneMsg{result: bubble.RunResult{
		Assistant: ds4.ChatMessage{Role: "assistant"},
	}})
	m = next.(model)

	if m.oneShotErr == nil {
		t.Error("oneShotErr = nil, want a no-SVG failure")
	}
	if !cmdYieldsQuit(t, cmd) {
		t.Error("no tea.Quit when the turn produced no SVG")
	}
}
