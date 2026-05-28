// Command ds4go-steer is an interactive terminal dashboard for DeepSeek activation steering.
//
// It enables real-time token logit exploration, manually or programmatically
// tweaking steering configurations (scales, vector files), branching timelines,
// and comparing steer variations side-by-side.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	ds4 "github.com/NimbleMarkets/ds4go"
	"github.com/NimbleMarkets/ds4go-apps/internal/cliopts"
	"github.com/NimbleMarkets/ds4go-apps/internal/steerinspect"
	"github.com/NimbleMarkets/ds4go-apps/internal/steertui"
	"github.com/NimbleMarkets/ds4go/ds4api"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	tea "charm.land/bubbletea/v2"
)

func main() {
	if err := execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

// execute registers the CLI flags, builds Cobra commands, and parses them.
func execute() error {
	fs := pflag.NewFlagSet("ds4go-steer", pflag.ContinueOnError)
	cfg := cliopts.RegisterCLI(fs)

	var dirSteering string
	var scaleFlag string
	var attnScale float64
	var allowAttnSteering bool
	var topK int
	var maxTokens int
	var logFile string

	fs.StringVar(&dirSteering, "dir-steering", "", "registry directory path containing vectors.json")
	fs.StringVar(&scaleFlag, "scale", "", "comma-separated FFN scales (defaults to 0,1,-1 when direction file is present)")
	fs.Float64Var(&attnScale, "attn-scale", 0.0, "default attention steering scale")
	fs.BoolVar(&allowAttnSteering, "allow-attn-steering", false, "allow attention steering")
	fs.IntVar(&topK, "top-k", 20, "number of token alternatives shown")
	fs.IntVar(&maxTokens, "max-tokens", 100, "generation token limit")
	fs.StringVar(&logFile, "log-file", "ds4go-steer.log", "file path to redirect engine and model loading logs")

	cmd := &cobra.Command{
		Use:   "ds4go-steer [options]",
		Short: "Interactive TUI for activation steering and logit inspection",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSteer(cfg, dirSteering, scaleFlag, float32(attnScale), allowAttnSteering, topK, maxTokens, logFile)
		},
	}
	cmd.Flags().AddFlagSet(fs)
	cmd.SilenceUsage = true
	return cmd.Execute()
}

// runSteer coordinates model loading, runner initialization, and starting the bubble tea program loop.
func runSteer(cfg *cliopts.CLIConfig, dirSteering, scaleFlag string, attnScale float32, allowAttnSteering bool, topK, maxTokens int, logFile string) error {
	if cfg.Model == "" {
		return fmt.Errorf("model path must be specified via --model or -m")
	}
	if _, err := os.Stat(cfg.Model); err != nil {
		return fmt.Errorf("model not found at %s: %w", cfg.Model, err)
	}

	var lib *ds4.Library
	var err error
	if cfg.Lib != "" {
		lib, err = ds4.Load(cfg.Lib)
		if err != nil {
			return fmt.Errorf("failed to load libds4 from %s: %w", cfg.Lib, err)
		}
		ds4.SetDefaultLibrary(lib)
	} else {
		lib, err = ds4.Load("")
		if err != nil {
			return fmt.Errorf("failed to load default libds4 library: %w", err)
		}
		ds4.SetDefaultLibrary(lib)
	}

	logBuf, err := steerinspect.NewLogBuffer(logFile, 500)
	if err != nil {
		return fmt.Errorf("failed to initialize log buffer: %w", err)
	}
	defer logBuf.Close()

	if err := ds4.SetLogFunc(logBuf.WriteLog); err != nil {
		return fmt.Errorf("failed to set log callback: %w", err)
	}

	reg, err := steerinspect.LoadRegistry(dirSteering)
	if err != nil {
		return fmt.Errorf("failed to load steering registry: %w", err)
	}

	var defaultVector string
	if cfg.DirSteeringFile != "" {
		baseName := filepath.Base(cfg.DirSteeringFile)
		name := strings.TrimSuffix(baseName, filepath.Ext(baseName))
		if name == "" {
			name = "custom"
		}
		defaultVector = name

		reg.Vectors[name] = steerinspect.RegistryVector{
			File:          cfg.DirSteeringFile,
			Description:   "Command line steering file: " + cfg.DirSteeringFile,
			DefaultFFN:    1.0,
			MaxFFN:        0,
			ModelCallable: false,
			AllowedModes:  []string{"ablation", "threshold", "additive"},
		}
	}

	var scales []float32
	if scaleFlag != "" {
		parts := strings.Split(scaleFlag, ",")
		for _, p := range parts {
			val, err := strconv.ParseFloat(strings.TrimSpace(p), 32)
			if err != nil {
				return fmt.Errorf("invalid scale value %q: %w", p, err)
			}
			scales = append(scales, float32(val))
		}
	} else {
		if cfg.DirSteeringFile != "" || len(reg.Vectors) > 0 {
			scales = []float32{0.0, 1.0, -1.0}
		} else {
			scales = []float32{0.0}
		}
	}

	promptText, err := cfg.PromptText()
	if err != nil {
		return err
	}

	// Redirect fd 2 (stderr) to /dev/null to prevent stray FFI library prints (like "done") from polluting the TUI.
	devNull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err == nil {
		if origFd, dupErr := syscall.Dup(2); dupErr == nil {
			syscall.Dup2(int(devNull.Fd()), 2)
			defer func() {
				syscall.Dup2(origFd, 2)
				syscall.Close(origFd)
			}()
		}
		devNull.Close()
	}

	engineOpts := cfg.EngineOptions()

	// If the loaded libds4 does not support dynamic per-session steering, fall
	// back to engine-level static steering. The first non-zero --scale value
	// becomes the engine's DirectionalSteeringFFN, and the scales slice is
	// collapsed so the TUI creates a single lane reflecting that static config.
	if !lib.SupportsDynamicSteering() && cfg.DirSteeringFile != "" {
		var staticScale float32
		for _, s := range scales {
			if s != 0 {
				staticScale = s
				break
			}
		}
		if staticScale != 0 {
			engineOpts.DirectionalSteeringFFN = staticScale
			engineOpts.DirectionalSteeringAttn = attnScale
			logBuf.WriteLog(ds4api.LogWarning, fmt.Sprintf(
				"libds4 has no dynamic steering; using static engine-level FFN=%g (only first non-zero scale honored)\n",
				staticScale))
			scales = []float32{staticScale}
		}
	}

	engine, err := ds4.NewEngine(engineOpts)
	if err != nil {
		return fmt.Errorf("failed to open engine: %w", err)
	}
	defer engine.Close()

	if name := engine.ModelName(); name != "" {
		logBuf.WriteLog(ds4api.LogOK, fmt.Sprintf("Loaded model: %s (id=%d) from %s\n", name, engine.ModelID(), cfg.Model))
	}

	runnerOpts := steerinspect.RunnerOptions{
		TopK:              topK,
		Temperature:       cfg.Temp,
		TopP:              cfg.TopP,
		MinP:              cfg.MinP,
		Seed:              cfg.ResolvedSeed(),
		AllowAttnSteering: allowAttnSteering,
		CtxSize:           cfg.Ctx,
	}
	runner := steerinspect.NewRunner(engine, reg, runnerOpts)
	defer runner.Close()

	promptConfig := steertui.PromptConfig{
		System:        cfg.System,
		ThinkMode:     cfg.ThinkMode(),
		DefaultVector: defaultVector,
		Scales:        scales,
		AttnScale:     attnScale,
	}
	m := steertui.NewModel(runner, cfg.Model, promptText, logBuf, promptConfig)
	m.MaxTokens = maxTokens

	p := tea.NewProgram(m)
	if _, err := p.Run(); err != nil {
		return fmt.Errorf("failed to run TUI: %w", err)
	}

	return nil
}
