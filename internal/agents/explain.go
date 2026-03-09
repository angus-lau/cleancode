package agents

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Explain sends symbol context to Claude and returns a natural language explanation.
func Explain(symbolContext string) (string, error) {
	prompt := fmt.Sprintf(`You are a senior engineer explaining code to a teammate. Given the following symbol and its codebase context, provide a clear, concise explanation.

Cover:
1. **What it does** — purpose and behavior in 1-2 sentences
2. **How it works** — key implementation details (logic, data flow)
3. **Who uses it** — callers and dependents (if provided)
4. **Side effects** — database writes, API calls, mutations, external dependencies
5. **Edge cases** — potential issues, null paths, error conditions worth knowing

Be direct. Skip obvious things. Focus on what a developer needs to know to safely modify this code.

%s`, symbolContext)

	cmd := exec.Command("claude", "-p", prompt)
	cmd.Env = append(os.Environ(), "CLAUDECODE=")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("claude cli: %w\n%s", err, string(output))
	}

	return strings.TrimSpace(string(output)), nil
}
