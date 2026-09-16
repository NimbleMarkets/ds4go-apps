package bubble

import (
	"context"
	"testing"

	ds4 "github.com/NimbleMarkets/ds4go"
	"github.com/NimbleMarkets/ds4go/dsml"
)

func TestSamplingOptionsReachGeneration(t *testing.T) {
	eng, _, reg := mockDriverEnv(t)
	sess, err := eng.NewSession(3072)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()

	var got ds4.GenerateOptions
	d := NewGenerationDriver(DriverOptions{
		Engine: eng, Session: sess, Tools: reg,
		Temperature: 0.8,
		TopP:        0.95,
		Seed:        42,
		CompletePrompt: func(_ *ds4.Prompt, opts ds4.GenerateOptions, _ func(dsml.StreamEvent)) (string, error) {
			got = opts
			return "done", nil
		},
	})
	if _, err := d.RunWithPrompt(context.Background(), "sys", []ds4.ChatMessage{{Role: "user", Content: "draw"}}); err != nil {
		t.Fatal(err)
	}

	if got.Temperature != 0.8 {
		t.Errorf("Temperature = %v, want 0.8", got.Temperature)
	}
	if got.TopP != 0.95 {
		t.Errorf("TopP = %v, want 0.95", got.TopP)
	}
	if got.Seed != 42 {
		t.Errorf("Seed = %v, want 42", got.Seed)
	}
}

// Zero-value options must preserve the existing greedy default.
func TestSamplingDefaultsStayGreedy(t *testing.T) {
	eng, _, reg := mockDriverEnv(t)
	sess, err := eng.NewSession(3072)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()

	var got ds4.GenerateOptions
	d := NewGenerationDriver(DriverOptions{
		Engine: eng, Session: sess, Tools: reg,
		CompletePrompt: func(_ *ds4.Prompt, opts ds4.GenerateOptions, _ func(dsml.StreamEvent)) (string, error) {
			got = opts
			return "done", nil
		},
	})
	if _, err := d.RunWithPrompt(context.Background(), "sys", []ds4.ChatMessage{{Role: "user", Content: "draw"}}); err != nil {
		t.Fatal(err)
	}

	if got.Temperature != 0 || got.TopP != 0 || got.Seed != 0 {
		t.Errorf("sampling not zero-valued: temp=%v topp=%v seed=%v", got.Temperature, got.TopP, got.Seed)
	}
}
