package translate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// OpenCodeModel describes an LLM model configured in ~/.config/opencode/opencode.json.
type OpenCodeModel struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	FullName     string `json:"full_name"` // e.g. "DS/deepseek-v4-flash"
	ProviderKey  string `json:"provider_key"`
	ProviderName string `json:"provider_name"`
	BaseURL      string `json:"base_url"`
	APIKey       string `json:"api_key"`
}

type openCodeConfig struct {
	Provider map[string]struct {
		API     string `json:"api"`
		Name    string `json:"name"`
		Options struct {
			APIKey  string `json:"apiKey"`
			BaseURL string `json:"baseURL"`
		} `json:"options"`
		Models map[string]struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"models"`
	} `json:"provider"`
}

// LoadOpenCodeModels parses ~/.config/opencode/opencode.json if present.
func LoadOpenCodeModels() []OpenCodeModel {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	path := filepath.Join(home, ".config", "opencode", "opencode.json")
	return ParseOpenCodeConfig(path)
}

// ParseOpenCodeConfig parses the specified opencode.json file path.
// The returned slice is always sorted by FullName (providerKey/modelKey) so
// that Go's non-deterministic map iteration does NOT cause the list to shuffle
// on every render frame.
func ParseOpenCodeConfig(path string) []OpenCodeModel {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var doc openCodeConfig
	if err := json.Unmarshal(b, &doc); err != nil {
		return nil
	}

	// Sort provider keys first so we iterate in a stable order.
	providerKeys := make([]string, 0, len(doc.Provider))
	for k := range doc.Provider {
		providerKeys = append(providerKeys, k)
	}
	sort.Strings(providerKeys)

	var results []OpenCodeModel
	for _, pKey := range providerKeys {
		if strings.HasSuffix(strings.ToUpper(pKey), "-BAK") || strings.HasSuffix(strings.ToUpper(pKey), "_BAK") {
			continue
		}
		pVal := doc.Provider[pKey]
		pName := pVal.Name
		if pName == "" {
			pName = pKey
		}

		// Sort model keys within each provider too.
		modelKeys := make([]string, 0, len(pVal.Models))
		for mk := range pVal.Models {
			modelKeys = append(modelKeys, mk)
		}
		sort.Strings(modelKeys)

		for _, mKey := range modelKeys {
			mVal := pVal.Models[mKey]
			mID := mVal.ID
			if mID == "" {
				mID = mKey
			}
			mName := mVal.Name
			if mName == "" {
				mName = mID
			}
			fullName := pKey + "/" + mKey
			results = append(results, OpenCodeModel{
				ID:           mID,
				Name:         mName,
				FullName:     fullName,
				ProviderKey:  pKey,
				ProviderName: pName,
				BaseURL:      pVal.Options.BaseURL,
				APIKey:       pVal.Options.APIKey,
			})
		}
	}
	return results
}
