package config

import (
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"
)

// SetKey assigns a dotted key in a project config: `verify.timeout_s`,
// `budget.max_usd`, `autonomy` (03 §3.2).
//
// It walks the toml tags rather than a hand-written switch so a new field is
// settable the moment it is declared. A hand-written switch is a list that
// silently falls behind the struct, and a `project set` that accepts a key and
// changes nothing is worse than one that refuses it.
//
// Unknown keys are an error listing what is available. Guessing what someone
// meant is how a typo becomes a setting that was never applied.
//
// Identity fields are not settable. `project set id foo` would rewrite
// project.toml while the engine registry kept the old id, leaving a project
// that answers to one name on disk and another in the daemon.
func SetKey(cfg *Project, key, value string) error {
	if cfg == nil {
		return fmt.Errorf("nil project config")
	}
	if reserved[key] {
		return fmt.Errorf("key %q identifies the project and cannot be changed", key)
	}
	parts := strings.Split(key, ".")
	v := reflect.ValueOf(cfg).Elem()

	for i, part := range parts {
		field, ok := fieldByTOML(v, part)
		if !ok {
			return fmt.Errorf("unknown key %q; try one of: %s",
				key, strings.Join(Keys(), ", "))
		}
		if i == len(parts)-1 {
			return assign(field, key, value)
		}
		if field.Kind() != reflect.Struct {
			return fmt.Errorf("key %q: %q is not a section", key, part)
		}
		v = field
	}
	return fmt.Errorf("empty key")
}

// reserved names the fields that identify a project rather than configure it.
var reserved = map[string]bool{"id": true, "schema": true, "created": true}

// Keys lists every settable dotted key, sorted, for error messages and help.
func Keys() []string {
	var all []string
	walk(reflect.TypeOf(Project{}), "", &all)
	out := make([]string, 0, len(all))
	for _, k := range all {
		if !reserved[k] {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

func walk(t reflect.Type, prefix string, out *[]string) {
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		tag := tomlName(f)
		if tag == "" {
			continue
		}
		name := tag
		if prefix != "" {
			name = prefix + "." + tag
		}
		switch f.Type.Kind() {
		case reflect.Struct:
			walk(f.Type, name, out)
		case reflect.String, reflect.Int, reflect.Int64, reflect.Float64, reflect.Bool:
			*out = append(*out, name)
		}
	}
}

func tomlName(f reflect.StructField) string {
	tag := f.Tag.Get("toml")
	if tag == "" || tag == "-" {
		return ""
	}
	return strings.Split(tag, ",")[0]
}

func fieldByTOML(v reflect.Value, name string) (reflect.Value, bool) {
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		if tomlName(t.Field(i)) == name {
			return v.Field(i), true
		}
	}
	return reflect.Value{}, false
}

func assign(field reflect.Value, key, value string) error {
	if !field.CanSet() {
		return fmt.Errorf("key %q is not settable", key)
	}
	switch field.Kind() {
	case reflect.String:
		field.SetString(value)
	case reflect.Bool:
		b, err := strconv.ParseBool(value)
		if err != nil {
			return fmt.Errorf("key %q wants a boolean, got %q", key, value)
		}
		field.SetBool(b)
	case reflect.Int, reflect.Int64:
		n, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return fmt.Errorf("key %q wants a whole number, got %q", key, value)
		}
		field.SetInt(n)
	case reflect.Float64:
		f, err := strconv.ParseFloat(value, 64)
		if err != nil {
			return fmt.Errorf("key %q wants a number, got %q", key, value)
		}
		field.SetFloat(f)
	default:
		return fmt.Errorf("key %q holds a %s, which `project set` cannot write",
			key, field.Kind())
	}
	return nil
}
