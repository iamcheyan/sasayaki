package translate

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/iamcheyan/sasayaki/internal/config"
)

func TestTranslateOpenAICompatible(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Path; got != "/v1/chat/completions" {
			t.Fatalf("path = %s", got)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer secret" {
			t.Fatalf("authorization = %q", got)
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"こんにちは"}}]}`))
	}))
	defer server.Close()
	got, err := Translate(context.Background(), config.TranslationConfig{Enabled: true, BaseURL: server.URL + "/v1", Model: "fast", APIKey: "secret", TargetLanguage: "Japanese"}, "你好")
	if err != nil || got != "こんにちは" {
		t.Fatalf("Translate = %q, %v", got, err)
	}
}
