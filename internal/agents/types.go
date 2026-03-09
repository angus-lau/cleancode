package agents

type Severity string

const (
	Critical Severity = "critical"
	Warning  Severity = "warning"
	Info     Severity = "info"
)

type AgentConfig struct {
	Name    string `json:"name"`
	Mandate string `json:"mandate"`
	Prompt  string `json:"prompt"`
	Enabled bool   `json:"enabled"`
}

type Finding struct {
	Agent      string   `json:"agent"`
	Severity   Severity `json:"severity"`
	File       string   `json:"file"`
	Line       int      `json:"line,omitempty"`
	Message    string   `json:"message"`
	Suggestion string   `json:"suggestion,omitempty"`
}

type ReviewResult struct {
	Agent    string    `json:"agent"`
	Findings []Finding `json:"findings"`
	Elapsed  int64     `json:"elapsed"` // milliseconds
}
