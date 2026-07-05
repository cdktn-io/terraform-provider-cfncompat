// Copyright (c) 2026 cdktn-io
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"strconv"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/function"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// Ensure ToJsonStringFunction satisfies the function.Function interface.
var _ function.Function = ToJsonStringFunction{}

// NewToJsonStringFunction returns a new instance of the
// provider::cfncompat::to_json_string function.
func NewToJsonStringFunction() function.Function {
	return ToJsonStringFunction{}
}

// ToJsonStringFunction implements CloudFormation's Fn::ToJsonString intrinsic
// function: it converts an object or array (or, as a provider-function
// deviation, any dynamic value) to its corresponding compact JSON string.
//
// See: https://docs.aws.amazon.com/AWSCloudFormation/latest/TemplateReference/intrinsic-function-reference-ToJsonString.html
type ToJsonStringFunction struct{}

func (f ToJsonStringFunction) Metadata(_ context.Context, _ function.MetadataRequest, resp *function.MetadataResponse) {
	resp.Name = "to_json_string"
}

func (f ToJsonStringFunction) Definition(_ context.Context, _ function.DefinitionRequest, resp *function.DefinitionResponse) {
	resp.Definition = function.Definition{
		Summary: "Converts an object or array to a JSON string, matching CloudFormation's Fn::ToJsonString.",
		Description: "Serializes value (a map/object, list/tuple, or primitive, arbitrarily nested) to a compact JSON " +
			"string. Object keys are sorted deterministically. Numbers are rendered as canonical decimals with no " +
			"trailing zeros and no scientific notation for typical values.",
		MarkdownDescription: "Implements CloudFormation's " +
			"[`Fn::ToJsonString`](https://docs.aws.amazon.com/AWSCloudFormation/latest/TemplateReference/intrinsic-function-reference-ToJsonString.html) " +
			"intrinsic function (`AWS::LanguageExtensions`) / AWS CDK's `Fn.toJsonString()`.\n\n" +
			"**Deviation from CFN template syntax:** in a CloudFormation template, `Fn::ToJsonString` requires the " +
			"`AWS::LanguageExtensions` transform to be declared, and its documented syntax only shows an `Object` or " +
			"`Array` as the top-level argument. As a provider-defined function this is a pure serializer with no " +
			"transform to declare, so `value` may be any dynamic value — object/map, list/tuple, or primitive " +
			"(string/number/bool), including `null`, arbitrarily nested — matching AWS CDK's " +
			"`Fn.toJsonString(value: any)`, which likewise accepts any value. When CDK synthesizes a CloudFormation " +
			"template that calls `Fn.toJsonString()`, CDK automatically adds the `AWS::LanguageExtensions` transform " +
			"to the template on your behalf; this function is the synth-time equivalent used before that template " +
			"exists.\n\n" +
			"`value` is serialized to compact JSON (no extraneous whitespace). Object/map keys are sorted " +
			"lexicographically byte-wise so output is deterministic regardless of input key order (CloudFormation " +
			"does not document a specific key order for `Fn::ToJsonString`, and Go maps are unordered, so this " +
			"function imposes and documents sorted-key output). Numbers are rendered as canonical decimal literals " +
			"(shortest round-tripping representation, no trailing zeros, no scientific notation) for typical values; " +
			"extremely large or small magnitudes may still require many digits since JSON has no exponent-free size " +
			"limit guarantee.\n\n" +
			"Matches the official example: `{\"key1\": \"value1\", \"key2\": <resolved-value>}` serializes to " +
			"`\"{\\\"key1\\\":\\\"value1\\\",\\\"key2\\\":\\\"resolved-value\\\"}\"`.",
		Parameters: []function.Parameter{
			function.DynamicParameter{
				Name:                "value",
				Description:         "The value (object/map, list/tuple, or primitive, arbitrarily nested) to convert to a JSON string.",
				MarkdownDescription: "The value (object/map, list/tuple, or primitive, arbitrarily nested) to convert to a JSON string.",
			},
		},
		Return: function.StringReturn{},
	}
}

func (f ToJsonStringFunction) Run(ctx context.Context, req function.RunRequest, resp *function.RunResponse) {
	var value types.Dynamic

	resp.Error = req.Arguments.Get(ctx, &value)
	if resp.Error != nil {
		return
	}

	if value.IsUnknown() {
		return
	}

	converted, unknown, err := toJsonStringConvert(value, "value")
	if err != nil {
		resp.Error = function.NewArgumentFuncError(0, fmt.Sprintf("to_json_string: %s", err))
		return
	}

	if unknown {
		return
	}

	encoded, err := toJsonStringEncode(converted)
	if err != nil {
		resp.Error = function.NewArgumentFuncError(0, fmt.Sprintf("to_json_string: %s", err))
		return
	}

	resp.Error = resp.Result.Set(ctx, encoded)
}

// toJsonStringConvert recursively converts an attr.Value into a plain Go
// value tree (string, bool, json.Number, []any, map[string]any, or nil for
// JSON null) suitable for encoding/json to serialize. path is a
// human-readable pointer to the current position, used in error messages.
//
// It returns unknown=true if v (or anything nested within it) is not yet
// known; callers should propagate this by not setting a result, mirroring
// how other cfncompat functions handle unknown values during planning.
func toJsonStringConvert(v attr.Value, path string) (result any, unknown bool, err error) {
	switch val := v.(type) {
	case types.Dynamic:
		if val.IsNull() {
			return nil, false, nil
		}
		if val.IsUnknown() {
			return nil, true, nil
		}
		if val.IsUnderlyingValueNull() {
			return nil, false, nil
		}
		if val.IsUnderlyingValueUnknown() {
			return nil, true, nil
		}
		return toJsonStringConvert(val.UnderlyingValue(), path)
	case types.String:
		if val.IsUnknown() {
			return nil, true, nil
		}
		if val.IsNull() {
			return nil, false, nil
		}
		return val.ValueString(), false, nil
	case types.Bool:
		if val.IsUnknown() {
			return nil, true, nil
		}
		if val.IsNull() {
			return nil, false, nil
		}
		return val.ValueBool(), false, nil
	case types.Int64:
		if val.IsUnknown() {
			return nil, true, nil
		}
		if val.IsNull() {
			return nil, false, nil
		}
		return json.Number(strconv.FormatInt(val.ValueInt64(), 10)), false, nil
	case types.Int32:
		if val.IsUnknown() {
			return nil, true, nil
		}
		if val.IsNull() {
			return nil, false, nil
		}
		return json.Number(strconv.FormatInt(int64(val.ValueInt32()), 10)), false, nil
	case types.Number:
		if val.IsUnknown() {
			return nil, true, nil
		}
		if val.IsNull() {
			return nil, false, nil
		}
		num, numErr := toJsonStringCanonicalNumber(val.ValueBigFloat())
		if numErr != nil {
			return nil, false, fmt.Errorf("%s: %w", path, numErr)
		}
		return num, false, nil
	case types.List:
		if val.IsUnknown() {
			return nil, true, nil
		}
		if val.IsNull() {
			return nil, false, nil
		}
		return toJsonStringConvertSlice(val.Elements(), path)
	case types.Set:
		if val.IsUnknown() {
			return nil, true, nil
		}
		if val.IsNull() {
			return nil, false, nil
		}
		return toJsonStringConvertSlice(val.Elements(), path)
	case types.Tuple:
		if val.IsUnknown() {
			return nil, true, nil
		}
		if val.IsNull() {
			return nil, false, nil
		}
		return toJsonStringConvertSlice(val.Elements(), path)
	case types.Map:
		if val.IsUnknown() {
			return nil, true, nil
		}
		if val.IsNull() {
			return nil, false, nil
		}
		return toJsonStringConvertMap(val.Elements(), path)
	case types.Object:
		if val.IsUnknown() {
			return nil, true, nil
		}
		if val.IsNull() {
			return nil, false, nil
		}
		return toJsonStringConvertMap(val.Attributes(), path)
	default:
		return nil, false, fmt.Errorf("%s: unsupported value type %T", path, v)
	}
}

// toJsonStringConvertSlice converts the ordered elements of a list, set, or
// tuple into a []any, recursing into each element.
func toJsonStringConvertSlice(elems []attr.Value, path string) (any, bool, error) {
	result := make([]any, 0, len(elems))

	for i, elem := range elems {
		converted, unknown, err := toJsonStringConvert(elem, fmt.Sprintf("%s[%d]", path, i))
		if err != nil {
			return nil, false, err
		}
		if unknown {
			return nil, true, nil
		}

		result = append(result, converted)
	}

	return result, false, nil
}

// toJsonStringConvertMap converts the entries of a map or object into a
// map[string]any, recursing into each value. Keys are copied as-is; sorting
// for deterministic output happens at encode time via encoding/json's
// built-in sorted map-key marshaling.
func toJsonStringConvertMap(elems map[string]attr.Value, path string) (any, bool, error) {
	result := make(map[string]any, len(elems))

	for key, elem := range elems {
		converted, unknown, err := toJsonStringConvert(elem, fmt.Sprintf("%s.%s", path, key))
		if err != nil {
			return nil, false, err
		}
		if unknown {
			return nil, true, nil
		}

		result[key] = converted
	}

	return result, false, nil
}

// toJsonStringCanonicalNumber renders a big.Float as a canonical JSON number
// literal: fixed-point (never scientific notation), and the shortest decimal
// representation that round-trips (no trailing zeros).
func toJsonStringCanonicalNumber(bf *big.Float) (json.Number, error) {
	if bf == nil {
		return json.Number("0"), nil
	}

	if bf.IsInf() {
		return "", fmt.Errorf("cannot represent an infinite number as JSON")
	}

	// 'f' format never uses scientific notation; precision -1 selects the
	// smallest number of digits necessary to represent the value uniquely
	// (i.e. no trailing zeros).
	text := bf.Text('f', -1)

	return json.Number(text), nil
}

// toJsonStringEncode marshals a converted value tree to a compact JSON
// string without HTML-escaping (matching the official AWS doc examples,
// which contain unescaped characters like un-encoded ASCII quotes only).
func toJsonStringEncode(v any) (string, error) {
	var buf bytes.Buffer

	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)

	if err := encoder.Encode(v); err != nil {
		return "", fmt.Errorf("failed to encode value as JSON: %w", err)
	}

	// json.Encoder.Encode appends a trailing newline; strip it since CFN's
	// Fn::ToJsonString returns a single-line string with no trailing newline.
	return string(bytes.TrimRight(buf.Bytes(), "\n")), nil
}
