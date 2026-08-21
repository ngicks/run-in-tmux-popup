package runinpopup

import (
	"reflect"
	"slices"
	"strings"
	"testing"
)

// Config, PartialConfig and Apply each spell the same field list out by hand,
// which is what keeps the merge path obvious and reflection-free at runtime.
// These tests are what make the three lists provably one schema: reflection
// lives here, so a field added to one and forgotten in another fails loudly
// instead of silently dropping a config key.

// configLeaf is one scalar field of a config tree, addressed every way the
// contracts care about: where reflect finds it, how a --format template names
// it, how the config file spells it, and which variable the env layer reads.
type configLeaf struct {
	goPath   string // ".Timeouts.Overall"
	jsonPath string // "timeouts.overall"
	env      string // "TIMEOUTS_OVERALL", without EnvPrefix; sparse trees only
	typeName string // "time.Duration" — the pointed-to type on a sparse tree
	index    []int  // reflect index path from the root struct
}

// walkConfigTree flattens a config struct into its leaves in declaration order,
// checking the tag conventions on the way down: json and yaml agree on every
// key, a sparse mirror marks every field optional and names its env variable,
// and the concrete config marks nothing optional.
func walkConfigTree(t *testing.T, typ reflect.Type, sparse bool) []configLeaf {
	t.Helper()
	return appendConfigLeaves(t, nil, typ, sparse, configLeaf{})
}

func appendConfigLeaves(
	t *testing.T,
	leaves []configLeaf,
	typ reflect.Type,
	sparse bool,
	parent configLeaf,
) []configLeaf {
	t.Helper()
	for field := range typ.Fields() {
		jsonKey := configTagKey(t, typ, field, "json", "omitzero", sparse)
		yamlKey := configTagKey(t, typ, field, "yaml", "omitempty", sparse)
		if jsonKey != yamlKey {
			t.Errorf(
				"%s.%s: json key %q and yaml key %q disagree,"+
					" but the tag sets are kept field-for-field in sync",
				typ, field.Name, jsonKey, yamlKey,
			)
		}

		leaf := configLeaf{
			goPath:   parent.goPath + "." + field.Name,
			jsonPath: strings.TrimPrefix(parent.jsonPath+"."+jsonKey, "."),
			index:    slices.Concat(parent.index, field.Index),
		}

		switch {
		case sparse && field.Type.Kind() == reflect.Pointer:
			leaf.env = parent.env + configEnvTag(t, typ, field, "env")
			leaf.typeName = field.Type.Elem().String()
			leaves = append(leaves, leaf)
		case field.Type.Kind() == reflect.Struct:
			if sparse {
				leaf.env = parent.env + configEnvTag(t, typ, field, "envPrefix")
			}
			leaves = appendConfigLeaves(t, leaves, field.Type, sparse, leaf)
		case sparse:
			t.Errorf(
				"%s.%s is a %s: a sparse field is either a pointer"+
					" or a nested sparse sub-config, so absence stays expressible",
				typ, field.Name, field.Type,
			)
		default:
			leaf.typeName = field.Type.String()
			leaves = append(leaves, leaf)
		}
	}
	return leaves
}

// configTagKey reads one serialization tag and checks the option the sparse or
// concrete contract requires of it: a sparse field stays omissible so a
// marshaled partial is sparse, while a concrete field always writes its key.
func configTagKey(
	t *testing.T,
	typ reflect.Type,
	field reflect.StructField,
	tag, sparseOpt string,
	sparse bool,
) string {
	t.Helper()
	value, ok := field.Tag.Lookup(tag)
	if !ok {
		t.Errorf("%s.%s: no %s tag; every config field carries all three tag sets",
			typ, field.Name, tag)
		return ""
	}
	key, opts, _ := strings.Cut(value, ",")
	switch {
	case sparse && opts != sparseOpt:
		t.Errorf(`%s.%s: %s tag options are %q, want %q`,
			typ, field.Name, tag, opts, sparseOpt)
	case !sparse && opts != "":
		t.Errorf(`%s.%s: %s tag carries options %q, but a merged config writes every key`,
			typ, field.Name, tag, opts)
	}
	return key
}

func configEnvTag(t *testing.T, typ reflect.Type, field reflect.StructField, tag string) string {
	t.Helper()
	value, ok := field.Tag.Lookup(tag)
	if !ok || value == "" {
		t.Errorf("%s.%s: no %s tag; the env layer would not reach this field",
			typ, field.Name, tag)
	}
	return value
}

// configTreeLines renders leaves as the comparable shape of a config tree: the
// template-visible path, the serialized key, and the underlying type.
func configTreeLines(leaves []configLeaf) []string {
	lines := make([]string, len(leaves))
	for i, leaf := range leaves {
		lines[i] = leaf.goPath + " (" + leaf.jsonPath + ") " + leaf.typeName
	}
	return lines
}

func TestPartialConfig_mirrorsConfigFieldForField(t *testing.T) {
	concrete := configTreeLines(walkConfigTree(t, reflect.TypeFor[Config](), false))
	sparse := configTreeLines(walkConfigTree(t, reflect.TypeFor[PartialConfig](), true))

	if !slices.Equal(concrete, sparse) {
		t.Errorf(
			"Config and PartialConfig disagree on the schema:\n\tConfig:\n\t\t%s"+
				"\n\tPartialConfig:\n\t\t%s",
			strings.Join(concrete, "\n\t\t"),
			strings.Join(sparse, "\n\t\t"),
		)
	}
}

func TestPartialConfig_applyReachesEveryField(t *testing.T) {
	def := DefaultConfig()
	concrete := walkConfigTree(t, reflect.TypeFor[Config](), false)
	sparse := walkConfigTree(t, reflect.TypeFor[PartialConfig](), true)

	byJSONPath := make(map[string]configLeaf, len(concrete))
	for _, leaf := range concrete {
		byJSONPath[leaf.jsonPath] = leaf
	}

	for _, set := range sparse {
		t.Run(set.jsonPath, func(t *testing.T) {
			target, ok := byJSONPath[set.jsonPath]
			if !ok {
				t.Fatalf("PartialConfig has %s but Config does not", set.jsonPath)
			}
			want := differentConfigValue(t, reflect.ValueOf(def).FieldByIndex(target.index))

			partial := reflect.New(reflect.TypeFor[PartialConfig]()).Elem()
			field := partial.FieldByIndex(set.index)
			field.Set(reflect.New(field.Type().Elem()))
			field.Elem().Set(want)

			got := reflect.ValueOf(partial.Interface().(PartialConfig).Apply(def))
			for _, leaf := range concrete {
				gotValue := got.FieldByIndex(leaf.index)
				if leaf.jsonPath == set.jsonPath {
					if !gotValue.Equal(want) {
						t.Errorf(
							"Apply left %s at %v though the partial set %v:"+
								" the merge has no branch for this field",
							leaf.goPath, gotValue, want,
						)
					}
					continue
				}
				if defValue := reflect.ValueOf(def).
					FieldByIndex(leaf.index); !gotValue.Equal(
					defValue,
				) {
					t.Errorf(
						"Apply changed %s from %v to %v while only %s was set",
						leaf.goPath, defValue, gotValue, set.jsonPath,
					)
				}
			}
		})
	}
}

// differentConfigValue returns a value the field does not already hold, so a
// merge that skips the field is visible rather than accidentally right.
func differentConfigValue(t *testing.T, current reflect.Value) reflect.Value {
	t.Helper()
	switch current.Kind() {
	case reflect.String:
		return reflect.ValueOf(current.String() + "-overridden").Convert(current.Type())
	case reflect.Bool:
		return reflect.ValueOf(!current.Bool()).Convert(current.Type())
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		// time.Duration lands here too, as the named int64 it is.
		return reflect.ValueOf(current.Int() + 1).Convert(current.Type())
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return reflect.ValueOf(current.Uint() + 1).Convert(current.Type())
	default:
		t.Fatalf("no override value for a %s field: teach this test how to vary one",
			current.Type())
		return reflect.Value{}
	}
}

func TestPartialConfig_envTagsMatchTheIsolatedVariables(t *testing.T) {
	want := []string{ENV_RUN_IN_POPUP_CONF}
	for _, leaf := range walkConfigTree(t, reflect.TypeFor[PartialConfig](), true) {
		want = append(want, EnvPrefix+leaf.env)
	}
	slices.Sort(want)

	got := slices.Sorted(slices.Values(configEnvVars))
	if !slices.Equal(got, want) {
		t.Errorf(
			"configEnvVars is %v, but the env layer reads %v:"+
				" a variable missing from the list leaks the developer's environment into the tests",
			got, want,
		)
	}
}
