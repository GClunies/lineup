package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/gclunies/lineup/internal/config"
	"github.com/gclunies/lineup/internal/route"
)

const (
	judgeModel   = "haiku"
	judgeTimeout = time.Minute
	// summaryBytes is how much of a Repo's README or CLAUDE.md the model reads.
	summaryBytes = 1500
	notSure      = "unsure"
)

// claudeJudge asks a small model which candidate Repo an Issue's work
// happens in. The JSON schema of its answer allows only the candidates and
// "unsure". The model runs with no tools, so the Issue's text can make it
// pick a wrong candidate at worst.
type claudeJudge struct {
	cfg *config.Global
	log *slog.Logger
}

func (j claudeJudge) pick(ctx context.Context, c route.Clues, candidates []string) string {
	key, err := j.ask(ctx, c, candidates)
	if err != nil {
		j.log.Warn("the model could not pick a Repo", "candidates", candidates, "err", err)
		return ""
	}
	return key
}

func (j claudeJudge) ask(ctx context.Context, c route.Clues, candidates []string) (string, error) {
	bin, err := j.cfg.ClaudePath()
	if err != nil {
		return "", err
	}
	schema, err := json.Marshal(map[string]any{
		"type":       "object",
		"properties": map[string]any{"repo": map[string]any{"type": "string", "enum": append(slices.Clone(candidates), notSure)}},
		"required":   []string{"repo"},
	})
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, judgeTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "-p", "--safe-mode", "--model", judgeModel, "--tools", "",
		"--no-session-persistence", "--output-format", "json", "--json-schema", string(schema))
	cmd.Dir = os.TempDir()
	cmd.Stdin = strings.NewReader(j.prompt(c, candidates))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	var res struct {
		IsError bool   `json:"is_error"`
		Result  string `json:"result"`
		Output  struct {
			Repo string `json:"repo"`
		} `json:"structured_output"`
	}
	if err := json.Unmarshal(out, &res); err != nil {
		return "", fmt.Errorf("read the model's answer: %w", err)
	}
	if res.IsError {
		return "", fmt.Errorf("the model failed: %s", res.Result)
	}
	return res.Output.Repo, nil
}

func (j claudeJudge) prompt(c route.Clues, candidates []string) string {
	var b strings.Builder
	b.WriteString("Pick the one Repo where the work of this Linear Issue happens. Read only the Issue and the Repo summaries below. ")
	b.WriteString("Answer \"" + notSure + "\" when they do not make one Repo clear.\n\n")
	fmt.Fprintf(&b, "<issue>\nTitle: %s\n\n%s\n</issue>\n\n", c.Title, c.Description)
	for _, k := range candidates {
		summary := ""
		if dir, ok := j.cfg.CheckoutFor(k); ok {
			summary = repoSummary(dir)
		}
		fmt.Fprintf(&b, "<repo key=%q>\n%s\n</repo>\n\n", k, summary)
	}
	return b.String()
}

// repoSummary is the start of the checkout's README.md, else of its CLAUDE.md.
func repoSummary(dir string) string {
	for _, name := range []string{"README.md", "CLAUDE.md"} {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		if len(data) > summaryBytes {
			data = data[:summaryBytes]
		}
		return strings.ToValidUTF8(string(data), "")
	}
	return ""
}
