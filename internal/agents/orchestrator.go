package agents

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"
	"time"
)

type Orchestrator struct {
	apiKey string
	agents []AgentConfig
	client *http.Client
}

func NewOrchestrator(customAgents []AgentConfig) *Orchestrator {
	apiKey := os.Getenv("ANTHROPIC_API_KEY")

	agentList := customAgents
	if len(agentList) == 0 {
		for _, a := range PresetAgents {
			if a.Enabled {
				agentList = append(agentList, a)
			}
		}
	}

	return &Orchestrator{
		apiKey: apiKey,
		agents: agentList,
		client: &http.Client{Timeout: 120 * time.Second},
	}
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
	return results
}

func (o *Orchestrator) runAgent(agent AgentConfig, context string) ReviewResult {
	start := time.Now()

	userMessage := fmt.Sprintf(`Review the following code changes and codebase context. Return your findings as a JSON array.

Each finding should have:
- "severity": "critical" | "warning" | "info"
- "file": the file path
- "line": line number (optional, use 0 if unknown)
- "message": clear description of the issue
- "suggestion": how to fix it (optional)

If you find no issues, return an empty array: []

IMPORTANT: Return ONLY the JSON array, no other text.

%s`, context)

	body := map[string]any{
		"model":      "claude-sonnet-4-6",
		"max_tokens": 4096,
		"system":     agent.Prompt,
		"messages": []map[string]string{
			{"role": "user", "content": userMessage},
		},
	}

	jsonBody, err := json.Marshal(body)
	if err != nil {
		return errorResult(agent.Name, start, err)
	}

	req, err := http.NewRequest("POST", "https://api.anthropic.com/v1/messages", bytes.NewReader(jsonBody))
	if err != nil {
		return errorResult(agent.Name, start, err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", o.apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	resp, err := o.client.Do(req)
	if err != nil {
		return errorResult(agent.Name, start, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return errorResult(agent.Name, start, err)
	}

	if resp.StatusCode != 200 {
		return errorResult(agent.Name, start, fmt.Errorf("API returned %d: %s", resp.StatusCode, string(respBody)))
	}

	// Parse Anthropic response
	var apiResp struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		return errorResult(agent.Name, start, err)
	}

	text := ""
	for _, block := range apiResp.Content {
		if block.Type == "text" {
			text = block.Text
			break
		}
	}

	findings := parseFindings(text, agent.Name)
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
