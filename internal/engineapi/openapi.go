package engineapi

import (
	"reflect"
	"sort"
	"strings"
)

// BuildOpenAPI produces the OpenAPI 3.1 document the clients are generated
// from (07 §7.3).
//
// Schemas are REFLECTED from the Go DTOs the handlers actually encode, not
// written by hand. A hand-authored schema is a second description of the same
// thing, and two descriptions drift; this one cannot disagree with the struct
// without the struct itself changing.
func BuildOpenAPI(version string) map[string]any {
	schemas := map[string]any{}
	paths := map[string]any{}

	for _, r := range routeTable() {
		item, _ := paths[r.Path].(map[string]any)
		if item == nil {
			item = map[string]any{}
			paths[r.Path] = item
		}

		op := map[string]any{
			"summary":     r.Summary,
			"operationId": operationID(r),
			"responses":   responsesFor(r, schemas),
		}
		if params := pathParams(r.Path); len(params) > 0 {
			op["parameters"] = params
		}
		if r.Request != nil {
			op["requestBody"] = map[string]any{
				"required": true,
				"content": map[string]any{
					"application/json": map[string]any{"schema": schemaRef(r.Request, schemas)},
				},
			}
		}
		if r.Auth {
			op["security"] = []any{map[string]any{"bearerAuth": []any{}}}
		}
		item[strings.ToLower(r.Method)] = op
	}

	return map[string]any{
		"openapi": "3.1.0",
		"info": map[string]any{
			"title":       "ducklab engine",
			"version":     version,
			"description": "Local engine API. Loopback only, bearer-token authenticated (07).",
		},
		"servers":    []any{map[string]any{"url": "http://127.0.0.1:{port}", "variables": map[string]any{"port": map[string]any{"default": "0"}}}},
		"paths":      paths,
		"components": map[string]any{"schemas": schemas, "securitySchemes": securitySchemes()},
	}
}

func securitySchemes() map[string]any {
	return map[string]any{
		"bearerAuth": map[string]any{"type": "http", "scheme": "bearer"},
	}
}

func operationID(r Route) string {
	if r.ClientMethod != "" {
		return r.ClientMethod
	}
	clean := strings.NewReplacer("/", "_", "{", "", "}", "", ".", "_").Replace(strings.TrimPrefix(r.Path, "/v1/"))
	return strings.ToLower(r.Method) + "_" + clean
}

func pathParams(path string) []any {
	var out []any
	for _, seg := range strings.Split(path, "/") {
		if strings.HasPrefix(seg, "{") && strings.HasSuffix(seg, "}") {
			out = append(out, map[string]any{
				"name": strings.Trim(seg, "{}"), "in": "path", "required": true,
				"schema": map[string]any{"type": "string"},
			})
		}
	}
	return out
}

func responsesFor(r Route, schemas map[string]any) map[string]any {
	if r.Response == nil {
		return map[string]any{
			"204": map[string]any{"description": "no content"},
			"401": map[string]any{"description": "missing or invalid credentials"},
		}
	}
	return map[string]any{
		"200": map[string]any{
			"description": "ok",
			"content": map[string]any{
				"application/json": map[string]any{"schema": schemaRef(r.Response, schemas)},
			},
		},
		"401": map[string]any{"description": "missing or invalid credentials"},
	}
}

// schemaRef registers a named schema for a struct and returns a $ref; inline
// shapes (envelopes, maps) are emitted directly.
func schemaRef(v any, schemas map[string]any) map[string]any {
	t := reflect.TypeOf(v)
	if t == nil {
		return map[string]any{"type": "object"}
	}
	// The list envelope carries its element type as a runtime value, because
	// `Items any` erases it at the type level. Reflecting the VALUE recovers
	// it; without this every list endpoint documents `items` as untyped and
	// the generated clients lose the element type entirely.
	if lo, ok := v.(listOf); ok {
		return map[string]any{
			"type": "object",
			"properties": map[string]any{
				"items": elemSchema(reflect.TypeOf(lo.Items), schemas),
				"total": map[string]any{"type": "integer"},
			},
			"required": []string{"items", "total"},
		}
	}
	if t.Kind() == reflect.Struct && t.Name() != "" && !strings.HasPrefix(t.Name(), "listOf") {
		name := schemaName(t)
		if _, done := schemas[name]; !done {
			schemas[name] = map[string]any{} // placeholder guards recursive types
			schemas[name] = schemaFor(t, schemas)
		}
		return map[string]any{"$ref": "#/components/schemas/" + name}
	}
	return schemaFor(t, schemas)
}

func schemaName(t reflect.Type) string {
	pkg := t.PkgPath()
	if i := strings.LastIndex(pkg, "/"); i >= 0 {
		pkg = pkg[i+1:]
	}
	if pkg == "" {
		return t.Name()
	}
	return strings.Title(pkg) + t.Name() //nolint:staticcheck // ASCII package names only
}

func schemaFor(t reflect.Type, schemas map[string]any) map[string]any {
	// time.Time is a struct in Go but an RFC3339 string on the wire.
	// Reflecting its fields would document a shape no client ever sees.
	if t.PkgPath() == "time" && t.Name() == "Time" {
		return map[string]any{"type": "string", "format": "date-time"}
	}
	switch t.Kind() {
	case reflect.Pointer:
		return schemaFor(t.Elem(), schemas)
	case reflect.String:
		return map[string]any{"type": "string"}
	case reflect.Bool:
		return map[string]any{"type": "boolean"}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return map[string]any{"type": "integer"}
	case reflect.Float32, reflect.Float64:
		return map[string]any{"type": "number"}
	case reflect.Slice, reflect.Array:
		return map[string]any{"type": "array", "items": elemSchema(t.Elem(), schemas)}
	case reflect.Map:
		return map[string]any{"type": "object", "additionalProperties": elemSchema(t.Elem(), schemas)}
	case reflect.Interface:
		// A genuinely dynamic field. Saying so is more honest than pretending
		// to a shape the handler does not guarantee.
		return map[string]any{}
	case reflect.Struct:
		props := map[string]any{}
		var required []string
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			if !f.IsExported() {
				continue
			}
			name, omitempty, skip := jsonName(f)
			if skip {
				continue
			}
			props[name] = elemSchema(f.Type, schemas)
			if !omitempty {
				required = append(required, name)
			}
		}
		sort.Strings(required)
		out := map[string]any{"type": "object", "properties": props}
		if len(required) > 0 {
			out["required"] = required
		}
		return out
	}
	return map[string]any{}
}

func elemSchema(t reflect.Type, schemas map[string]any) map[string]any {
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.PkgPath() == "time" && t.Name() == "Time" {
		return map[string]any{"type": "string", "format": "date-time"}
	}
	if t.Kind() == reflect.Struct && t.Name() != "" && t.PkgPath() != "" {
		name := schemaName(t)
		if _, done := schemas[name]; !done {
			schemas[name] = map[string]any{}
			schemas[name] = schemaFor(t, schemas)
		}
		return map[string]any{"$ref": "#/components/schemas/" + name}
	}
	return schemaFor(t, schemas)
}

func jsonName(f reflect.StructField) (name string, omitempty, skip bool) {
	tag := f.Tag.Get("json")
	if tag == "-" {
		return "", false, true
	}
	parts := strings.Split(tag, ",")
	name = parts[0]
	if name == "" {
		name = f.Name
	}
	for _, p := range parts[1:] {
		if p == "omitempty" {
			omitempty = true
		}
	}
	return name, omitempty, false
}
