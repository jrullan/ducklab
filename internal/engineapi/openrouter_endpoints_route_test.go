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
