package translate

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseOpenCodeConfig(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "opencode.json")
	content := `{
	"provider": {
		"DS": {
			"name": "DeepSeek",
			"options": {
				"apiKey": "sk-123",
				"baseURL": "https://api.deepseek.com"
			},
			"models": {
				"deepseek-v4-flash": {
					"id": "deepseek-v4-flash",
					"name": "DeepSeek V4 Flash"
				}
			}
		},
		"old-BAK": {
			"name": "Backup",
			"models": { "old": { "id": "old" } }
		}
	}
}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	models := ParseOpenCodeConfig(path)
	if len(models) != 1 {
		t.Fatalf("expected 1 model (skipping BAK), got %d", len(models))
	}
	if models[0].ID != "deepseek-v4-flash" {
		t.Errorf("expected model ID deepseek-v4-flash, got %s", models[0].ID)
	}
	if models[0].ProviderName != "DeepSeek" {
		t.Errorf("expected provider DeepSeek, got %s", models[0].ProviderName)
	}
	if models[0].BaseURL != "https://api.deepseek.com" {
		t.Errorf("expected BaseURL https://api.deepseek.com, got %s", models[0].BaseURL)
	}
	if models[0].APIKey != "sk-123" {
		t.Errorf("expected APIKey sk-123, got %s", models[0].APIKey)
	}
}
