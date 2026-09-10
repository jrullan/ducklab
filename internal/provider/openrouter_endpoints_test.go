package provider

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestModelEndpointsReadsPriceQuantizationAndDataPolicyWithoutInventingUnknowns(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/models/z-ai/glm-5.2/endpoints", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"data":{"endpoints":[{"provider_name":"DeepInfra","tag":"deepinfra/fp4","quantization":"fp4","context_length":131072,"max_completion_tokens":65536,"pricing":{"prompt":"0.00000049","completion":"0.00000156"}},{"provider_name":"Example","tag":"example/fp8","quantization":"fp8","pricing":{"prompt":"0.000001","completion":"0.000002"},"is_moderated":true,"data_policy":{"prompt_training":false,"retention":"30 days"}}]}}`)
	})
	mux.HandleFunc("/endpoints/zdr", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"data":[{"tag":"deepinfra/fp4"}]}`)
	})
	server := httptest.NewServer(mux)
	defer server.Close()
	p := NewOpenAICompat("openrouter", server.URL, "")

	items, err := p.ModelEndpoints(context.Background(), "z-ai/glm-5.2")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || items[0].Tag != "deepinfra/fp4" || items[0].Quantization != "fp4" {
		t.Fatalf("endpoints = %#v", items)
	}
	if items[0].InputPerMTok != 0.49 || items[0].OutputPerMTok != 1.56 {
		t.Fatalf("specific price = %#v", items[0])
	}
	if items[0].ZeroDataRetention == nil || !*items[0].ZeroDataRetention {
		t.Fatalf("ZDR = %#v", items[0].ZeroDataRetention)
	}
	if items[0].PromptTraining != nil || items[0].Moderated != nil {
		t.Fatalf("undisclosed policies were invented: %#v", items[0])
	}
	if items[1].PromptTraining == nil || *items[1].PromptTraining || items[1].Moderated == nil || !*items[1].Moderated {
		t.Fatalf("disclosed policies = %#v", items[1])
	}
}
