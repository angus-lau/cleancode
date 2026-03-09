package agents

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"time"
)

type Orchestrator struct {
	agents []AgentConfig
}

func NewOrchestrator(customAgents []AgentConfig) *Orchestrator {
	agentList := customAgents
	if len(agentList) == 0 {
		for _, a := range PresetAgents {
			if a.Enabled {
				agentList = append(agentList, a)
			}
		}
	}

	return &Orchestrator{agents: agentList}
}

func (o *Orchestrator) Review(formattedContext string) []ReviewResult {
	var wg sync.WaitGroup
	results := make([]ReviewResult, len(o.agents))

	for i, agent := range o.agents {
		wg.Add(1)
		go func(idx int, ag AgentConfig) {
			defer wg.Done()
			results[idx] = o.runAgent(ag, formattedContext)
		}(i, agent)
	}

	wg.Wait()

	// Pass 2: Synthesize if there are findings from multiple agents
	totalFindings := 0
	agentsWithFindings := 0
	for _, r := range results {
		if len(r.Findings) > 0 {
			totalFindings += len(r.Findings)
			agentsWithFindings++
		}
	}

	if agentsWithFindings >= 2 && totalFindings >= 2 {
		synthesized := o.synthesize(results)
		if synthesized != nil {
			return []ReviewResult{*synthesized}
		}
	}

	return results
}

func (o *Orchestrator) runAgent(agent AgentConfig, context string) ReviewResult {
	start := time.Now()

	prompt := fmt.Sprintf(`%s

Review the following code changes and codebase context. Return your findings as a JSON array.

Each finding should have:
- "severity": "critical" | "warning" | "info"
- "file": the file path
- "line": line number (optional, use 0 if unknown)
- "message": clear description of the issue
- "suggestion": how to fix it (optional)

If you find no issues, return an empty array: []

IMPORTANT: Return ONLY the JSON array, no other text.

%s`, agent.Prompt, context)

	cmd := exec.Command("claude", "-p", prompt)
	// Unset CLAUDECODE env var to allow nested invocations
	cmd.Env = append(os.Environ(), "CLAUDECODE=")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return errorResult(agent.Name, start, fmt.Errorf("claude cli: %w\n%s", err, string(output)))
	}

	findings := parseFindings(string(output), agent.Name)
	return ReviewResult{
		Agent:    agent.Name,
		Findings: findings,
		Elapsed:  time.Since(start).Milliseconds(),
	}
}

func parseFindings(text, agentName string) []Finding {
	// Find JSON array in response
	start := -1
	end := -1
	depth := 0
	for i, c := range text {
		if c == '[' && start == -1 {
			start = i
			depth = 1
		} else if c == '[' && start != -1 {
			depth++
		} else if c == ']' && start != -1 {
			depth--
			if depth == 0 {
				end = i + 1
				break
			}
		}
	}

	if start == -1 || end == -1 {
		return nil
	}

	var raw []struct {
		Severity   string `json:"severity"`
		File       string `json:"file"`
		Line       int    `json:"line"`
		Message    string `json:"message"`
		Suggestion string `json:"suggestion"`
	}

	if err := json.Unmarshal([]byte(text[start:end]), &raw); err != nil {
		return nil
	}

	findings := make([]Finding, 0, len(raw))
	for _, f := range raw {
		sev := Info
		switch f.Severity {
		case "critical":
			sev = Critical
		case "warning":
			sev = Warning
		}
		findings = append(findings, Finding{
			Agent:      agentName,
			Severity:   sev,
			File:       f.File,
			Line:       f.Line,
			Message:    f.Message,
			Suggestion: f.Suggestion,
		})
	}
	return findings
}

func (o *Orchestrator) synthesize(results []ReviewResult) *ReviewResult {
	start := time.Now()

	// Collect all findings into JSON for the synthesizer
	var allFindings []Finding
	for _, r := range results {
		allFindings = append(allFindings, r.Findings...)
	}

	findingsJSON, err := json.Marshal(allFindings)
	if err != nil {
		return nil
	}

	prompt := fmt.Sprintf(`You are a code review synthesizer. Multiple review agents have analyzed the same code changes and produced findings independently. Your job is to produce a single, clean, prioritized list.

Here are all the findings from the agents:
%s

Instructions:
1. DEDUPLICATE: If multiple agents flagged the same issue (same file, same concern), keep only the best-written one.
2. RESOLVE CONFLICTS: If agents disagree (e.g., one says "cache this" and another says "don't cache"), pick the right answer and explain why.
3. PRIORITIZE: Order by actual impact — critical bugs first, then warnings, then info.
4. PRESERVE: Keep the original agent attribution in the "agent" field so the developer knows which perspective it came from.

Return a JSON array of findings. Each finding must have:
- "agent": original agent name that found it (or "synthesizer" if you resolved a conflict)
- "severity": "critical" | "warning" | "info"
- "file": the file path
- "line": line number (0 if unknown)
- "message": clear description
- "suggestion": how to fix it (optional)

IMPORTANT: Return ONLY the JSON array, no other text.`, string(findingsJSON))

	cmd := exec.Command("claude", "-p", prompt)
	cmd.Env = append(os.Environ(), "CLAUDECODE=")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil // Fail gracefully — return unsynthesized results
	}

	findings := parseFindings(string(output), "synthesizer")
	if len(findings) == 0 {
		return nil
	}

	return &ReviewResult{
		Agent:    "synthesizer",
		Findings: findings,
		Elapsed:  time.Since(start).Milliseconds(),
	}
}

func errorResult(agentName string, start time.Time, err error) ReviewResult {
	return ReviewResult{
		Agent: agentName,
		Findings: []Finding{{
			Agent:    agentName,
			Severity: Info,
			Message:  fmt.Sprintf("Agent error: %v", err),
		}},
		Elapsed: time.Since(start).Milliseconds(),
	}
}
