package config

import (
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"
)

// SetKey assigns a dotted key in a project config: `verify.timeout_s`,
// `budget.max_usd`, `autonomy` (03 §3.2). String slices use comma-separated
// values; an empty value clears the slice.
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
// ValueKey returns a settable dotted key's current value in the same encoding
// SetKey accepts. It lets advisory UI show the actual recorded value without a
// second, inevitably incomplete switch over configuration fields.
func ValueKey(cfg *Project, key string) (string, error) {
	if cfg == nil {
		return "", fmt.Errorf("nil project config")
	}
	parts := strings.Split(key, ".")
	v := reflect.ValueOf(cfg).Elem()
	for i, part := range parts {
		field, ok := fieldByTOML(v, part)
		if !ok {
			return "", fmt.Errorf("unknown key %q", key)
		}
		if i == len(parts)-1 {
			return valueString(field), nil
		}
		if field.Kind() == reflect.Map {
			return mapValueKey(field, parts[i+1:], key)
		}
		if field.Kind() != reflect.Struct {
			return "", fmt.Errorf("key %q: %q is not a section", key, part)
		}
		v = field
	}
	return "", fmt.Errorf("empty key")
}

func valueString(v reflect.Value) string {
	if v.Kind() == reflect.Slice {
		items := make([]string, v.Len())
		for i := range items {
			items[i] = fmt.Sprint(v.Index(i).Interface())
		}
		return strings.Join(items, ",")
	}
	return fmt.Sprint(v.Interface())
}

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
		if field.Kind() == reflect.Map {
			return assignMapPath(field, parts[i+1:], key, value)
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
		case reflect.Map:
			walkMap(f.Type, name, out)
		case reflect.String, reflect.Int, reflect.Int64, reflect.Float64, reflect.Bool, reflect.Slice:
			*out = append(*out, name)
		}
	}
}

// walkMap expands maps whose key type has a closed, public vocabulary. Free-form
// maps deliberately remain absent: accepting arbitrary dotted keys would turn a
// typo into persisted configuration.
func walkMap(t reflect.Type, prefix string, out *[]string) {
	if t == modeSeatsMapType {
		for _, mode := range ValidModes() {
			for _, role := range ValidRoles() {
				*out = append(*out, prefix+"."+string(mode)+"."+string(role))
			}
		}
		return
	}
	var keys []string
	switch t.Key() {
	case reflect.TypeOf(Role("")):
		for _, role := range ValidRoles() {
			keys = append(keys, string(role))
		}
	case reflect.TypeOf(Stage("")):
		for _, stage := range ValidStages() {
			keys = append(keys, string(stage))
		}
	default:
		return
	}
	for _, key := range keys {
		*out = append(*out, prefix+"."+key)
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
	case reflect.Slice:
		if field.Type().Elem().Kind() != reflect.String {
			return fmt.Errorf("key %q holds a %s, which `project set` cannot write", key, field.Kind())
		}
		items, err := commaSeparated(value)
		if err != nil {
			return fmt.Errorf("key %q wants comma-separated values, got %q", key, value)
		}
		out := reflect.MakeSlice(field.Type(), len(items), len(items))
		for i, item := range items {
			out.Index(i).SetString(item)
		}
		field.Set(out)
	default:
		return fmt.Errorf("key %q holds a %s, which `project set` cannot write",
			key, field.Kind())
	}
	return nil
}

// commaSeparated is the project-set encoding for string lists. An empty value
// clears the list; otherwise every comma-delimited item must be non-empty.
func commaSeparated(value string) ([]string, error) {
	if value == "" {
		return []string{}, nil
	}
	items := strings.Split(value, ",")
	for _, item := range items {
		if item == "" {
			return nil, fmt.Errorf("empty item")
		}
	}
	return items, nil
}

var (
	modeSeatsMapType = reflect.TypeOf(map[string]map[string][]string{})
	roleSeatsMapType = reflect.TypeOf(map[string][]string{})
)

func mapValueKey(field reflect.Value, parts []string, key string) (string, error) {
	if len(parts) == 0 {
		return "", fmt.Errorf("empty key")
	}
	mapKey := reflect.ValueOf(parts[0]).Convert(field.Type().Key())
	if !mapKeyAllowed(field.Type(), mapKey) {
		return "", fmt.Errorf("unknown key %q", key)
	}
	item := field.MapIndex(mapKey)
	if !item.IsValid() {
		if len(parts) == 1 {
			return "", nil
		}
		return mapValueKey(reflect.Zero(field.Type().Elem()), parts[1:], key)
	}
	if len(parts) == 1 {
		return valueString(item), nil
	}
	if item.Kind() != reflect.Map {
		return "", fmt.Errorf("key %q: %q is not a section", key, parts[0])
	}
	return mapValueKey(item, parts[1:], key)
}

func assignMapPath(field reflect.Value, parts []string, key, value string) error {
	if len(parts) == 0 {
		return fmt.Errorf("empty key")
	}
	if len(parts) == 1 {
		return assignMapValue(field, parts[0], key, value)
	}
	mapKey := reflect.ValueOf(parts[0]).Convert(field.Type().Key())
	if !mapKeyAllowed(field.Type(), mapKey) {
		return fmt.Errorf("unknown key %q; try one of: %s", key, strings.Join(Keys(), ", "))
	}
	if field.Type().Elem().Kind() != reflect.Map {
		return fmt.Errorf("key %q: %q is not a section", key, parts[0])
	}
	entry := reflect.New(field.Type().Elem()).Elem()
	if current := field.MapIndex(mapKey); current.IsValid() {
		entry.Set(current)
	}
	if err := assignMapPath(entry, parts[1:], key, value); err != nil {
		return err
	}
	copy := cloneMap(field)
	copy.SetMapIndex(mapKey, entry)
	field.Set(copy)
	return nil
}

func assignMapValue(field reflect.Value, name, key, value string) error {
	mapKey := reflect.ValueOf(name).Convert(field.Type().Key())
	if !mapKeyAllowed(field.Type(), mapKey) {
		return fmt.Errorf("unknown key %q; try one of: %s", key, strings.Join(Keys(), ", "))
	}
	entry := reflect.New(field.Type().Elem()).Elem()
	if err := assign(entry, key, value); err != nil {
		return err
	}
	// Copy before writing: ProjectUpdate starts with a shallow struct copy, and
	// changing a shared map would violate its all-or-nothing update contract.
	copy := cloneMap(field)
	copy.SetMapIndex(mapKey, entry)
	field.Set(copy)
	return nil
}

func cloneMap(field reflect.Value) reflect.Value {
	copy := reflect.MakeMapWithSize(field.Type(), field.Len()+1)
	iter := field.MapRange()
	for iter.Next() {
		copy.SetMapIndex(iter.Key(), iter.Value())
	}
	return copy
}

func mapKeyAllowed(t reflect.Type, key reflect.Value) bool {
	switch t {
	case reflect.TypeOf(Roster{}), reflect.TypeOf(map[Role][]DucklingID{}), roleSeatsMapType:
		return ValidateRole(Role(key.String())) == nil
	case reflect.TypeOf(Modes{}):
		return ValidateStage(Stage(key.String())) == nil
	case modeSeatsMapType:
		return ValidateMode(Mode(key.String())) == nil
	default:
		return false
	}
}
