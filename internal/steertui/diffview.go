package steertui

import (
	"fmt"
	"math"

	tea "charm.land/bubbletea/v2"
	"github.com/NimbleMarkets/ds4go-apps/internal/steerinspect"
	"github.com/NimbleMarkets/ntdiff/diff"
)

// DiffView wraps an ntdiff.RichModel together with the lane pair currently
// being diffed and the steertui-specific MetaRenderer.
type DiffView struct {
	rich         diff.RichModel
	left, right  *steerinspect.Lane
	fork         int
	collapsed    bool
	showSteering bool
	width        int
	height       int
}

// NewDiffView returns a DiffView with default settings (collapsed prefix,
// steering glyph visible).
func NewDiffView() *DiffView {
	d := &DiffView{
		rich:         diff.NewRichModel(),
		collapsed:    true,
		showSteering: true,
	}
	d.rich.SetMetaRenderer(d.renderMeta)
	return d
}

// Open replaces the current pair and rebuilds the RichDiff.
func (d *DiffView) Open(left, right *steerinspect.Lane, fork int) {
	d.left = left
	d.right = right
	d.fork = fork
	d.collapsed = true
	d.rebuild()
}

// ToggleCollapsed flips the collapsed-prefix flag and rebuilds.
func (d *DiffView) ToggleCollapsed() {
	d.collapsed = !d.collapsed
	d.rebuild()
}

// ToggleSteeringGlyph flips whether ⚑ markers appear on rows with steering
// transitions.
func (d *DiffView) ToggleSteeringGlyph() { d.showSteering = !d.showSteering }

// SetSize updates the model's render dimensions.
func (d *DiffView) SetSize(w, h int) {
	d.width = w
	d.height = h
	d.rich.SetSize(w, h)
}

// LineCount returns the number of RichLines currently displayed.
func (d *DiffView) LineCount() int { return d.rich.LineCount() }

// HasPair reports whether Open has been called.
func (d *DiffView) HasPair() bool { return d.left != nil && d.right != nil }

// Update forwards keys to the embedded RichModel.
func (d *DiffView) Update(msg tea.Msg) tea.Cmd {
	rich, cmd := d.rich.Update(msg)
	d.rich = rich
	return cmd
}

// View returns the rendered diff string.
func (d *DiffView) View() string { return d.rich.View() }

func (d *DiffView) rebuild() {
	if d.left == nil || d.right == nil {
		return
	}
	rd := steerinspect.BuildRichDiff(d.left, d.right, d.fork, d.collapsed)
	d.rich.LoadRichDiff(rd)
}

func (d *DiffView) renderMeta(l diff.RichLine) string {
	switch m := l.Meta.(type) {
	case steerinspect.CollapsedMeta:
		return fmt.Sprintf("%d shared", m.Count)
	case steerinspect.StepMeta:
		return d.formatStepMeta(m, l.Status)
	}
	return ""
}

func (d *DiffView) formatStepMeta(m steerinspect.StepMeta, status diff.LineStatus) string {
	flag := ""
	if d.showSteering && (m.LeftSteeringChanged || m.RightSteeringChanged) {
		flag = "⚑ "
	}
	if status == diff.Shared {
		if m.LeftLogprob > -math.MaxFloat32 {
			return fmt.Sprintf("%slp:%.2f", flag, m.LeftLogprob)
		}
		return flag + "lp:n/a"
	}
	rankL := rankString(m.LeftRankInRightTopK)
	rankR := rankString(m.RightRankInLeftTopK)
	delta := "n/a"
	if m.LeftLogprob > -math.MaxFloat32 && m.RightLogprob > -math.MaxFloat32 {
		delta = fmt.Sprintf("%+.2f", m.LeftLogprob-m.RightLogprob)
	}
	return fmt.Sprintf("%sr:%s r:%s Δlp:%s", flag, rankL, rankR, delta)
}

func rankString(r int) string {
	if r < 0 {
		return ">K"
	}
	return fmt.Sprintf("%d", r)
}
