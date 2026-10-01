package engineapi

import "testing"

func TestOpenRouterModelEndpointsRouteIsInTheGeneratedContract(t *testing.T) {
	for _, route := range routeTable() {
		if route.Method == "GET" && route.Path == "/v1/providers/{id}/model-endpoints" {
			if route.ClientMethod != "ProviderModelEndpoints" {
				t.Fatalf("client method = %q", route.ClientMethod)
			}
			return
		}
	}
	t.Fatal("OpenRouter model endpoint route is absent")
}

func TestProviderModelsRouteIsInTheGeneratedContract(t *testing.T) {
	for _, route := range routeTable() {
		if route.Method == "GET" && route.Path == "/v1/providers/{id}/models" {
			if route.ClientMethod != "ProviderModels" {
				t.Fatalf("client method = %q", route.ClientMethod)
			}
			return
		}
	}
	t.Fatal("provider models route is absent")
}
