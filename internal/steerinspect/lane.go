package steerinspect

import (
	"fmt"
	"sync"

	"github.com/NimbleMarkets/ds4go/ds4api"
)

// Lane represents a single generation timeline branch.
type Lane struct {
	mu         sync.Mutex
	ID         string          `json:"id"`
	ParentID   string          `json:"parent_id,omitempty"`
	ParentPos  int             `json:"parent_pos,omitempty"`
	Session    *ds4api.Session `json:"-"`
	Steps      []Step          `json:"steps"`
	InitialPos int             `json:"initial_pos"` // Position where this lane started (prefill end)
}

// NewLane creates a new initialized Lane.
func NewLane(id string, parentID string, parentPos int, session *ds4api.Session, initialPos int) *Lane {
	return &Lane{
		ID:         id,
		ParentID:   parentID,
		ParentPos:  parentPos,
		Session:    session,
		InitialPos: initialPos,
		Steps:      make([]Step, 0),
	}
}

// AddStep appends a generation step to the lane's timeline.
func (l *Lane) AddStep(step Step) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.Steps = append(l.Steps, step)
}

// GetSteps returns a copy of the steps currently in the lane.
func (l *Lane) GetSteps() []Step {
	l.mu.Lock()
	defer l.mu.Unlock()
	steps := make([]Step, len(l.Steps))
	copy(steps, l.Steps)
	return steps
}

// Rewind rewinds the lane's session to the specified position and truncates the steps timeline.
func (l *Lane) Rewind(pos int) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.Session == nil {
		return fmt.Errorf("lane %s session is closed", l.ID)
	}

	if err := l.Session.RewindSynced(pos); err != nil {
		return fmt.Errorf("rewind lane %s: %w", l.ID, err)
	}

	// Keep only steps where Step.Pos < pos
	var newSteps []Step
	for _, s := range l.Steps {
		if s.Pos < pos {
			newSteps = append(newSteps, s)
		}
	}
	l.Steps = newSteps
	return nil
}
